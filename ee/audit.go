//go:build ee

package ee

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alternayte/fettle/internal/app"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/ee/licensekey"
	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/objects"
	"github.com/alternayte/fettle/internal/settings"
)

func init() {
	settings.Register(settings.Def{Key: "ee.audit_retention_days", Scope: "instance", Default: 365, Doc: "Days that exported audit files stay in the audit bucket. Older files are deleted."})
	settings.Register(settings.Def{Key: "ee.audit_cursor", Scope: "instance", Default: "", Doc: "The last audit row that the export sent (set by Fettle)."})
	settings.Register(settings.Def{Key: "ee.audit_sent", Scope: "instance", Default: "", Internal: true, Doc: "The audit rows of the last minute that the export sent, as JSON (set by Fettle)."})
	settings.Register(settings.Def{Key: "ee.audit_retention_at", Scope: "instance", Default: "", Doc: "When the audit retention last ran (set by Fettle)."})
}

// AuditConfig is the audit export of this server, from the environment (§18.3): an
// S3-compatible bucket and a webhook signed with a secret.
type AuditConfig struct {
	S3            *objects.S3Config
	Prefix        string
	WebhookURL    string
	WebhookSecret string
}

// auditConfig reads FETTLE_AUDIT_S3_* and FETTLE_AUDIT_WEBHOOK_*.
func auditConfig() AuditConfig {
	c := AuditConfig{Prefix: os.Getenv("FETTLE_AUDIT_S3_PREFIX"), WebhookURL: os.Getenv("FETTLE_AUDIT_WEBHOOK_URL"), WebhookSecret: os.Getenv("FETTLE_AUDIT_WEBHOOK_SECRET")}
	if c.Prefix == "" {
		c.Prefix = "audit/"
	}
	if b := os.Getenv("FETTLE_AUDIT_S3_BUCKET"); b != "" {
		c.S3 = &objects.S3Config{Endpoint: os.Getenv("FETTLE_AUDIT_S3_ENDPOINT"), Region: os.Getenv("FETTLE_AUDIT_S3_REGION"), Bucket: b,
			AccessKey: os.Getenv("FETTLE_AUDIT_S3_ACCESS_KEY"), SecretKey: os.Getenv("FETTLE_AUDIT_S3_SECRET_KEY"), PathStyle: os.Getenv("FETTLE_AUDIT_S3_PATH_STYLE") == "true"}
	}
	return c
}

// auditLine is one audit row in an export file and in a webhook delivery.
type auditLine struct {
	ID        string            `json:"id"`
	At        time.Time         `json:"at"`
	ActorKind string            `json:"actor_kind"`
	ActorID   string            `json:"actor_id"`
	SpaceID   *uuid.UUID        `json:"space_id,omitempty"`
	Action    string            `json:"action"`
	Target    string            `json:"target"`
	Details   map[string]string `json:"details,omitempty"`
}

var auditMu sync.Mutex

// SignWebhook is the signature of a delivery: hex HMAC-SHA256 of "<timestamp>.<body>".
func SignWebhook(secret, timestamp string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(timestamp + "."))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// exportAudit sends the audit rows after the cursor to the bucket and the webhook, in
// batches of 1000, and applies the retention once a day. A failed delivery keeps the
// cursor, so the next tick sends the batch again under the same file name.
func (x *Enterprise) exportAudit(ctx context.Context, now bool) {
	if !x.Has(ctx, licensekey.FeatureAudit, true) {
		return
	}
	cfg := auditConfig()
	if cfg.S3 == nil && cfg.WebhookURL == "" {
		return
	}
	auditMu.Lock()
	defer auditMu.Unlock()
	var store *objects.S3
	if cfg.S3 != nil {
		s, err := objects.NewS3(ctx, *cfg.S3)
		if err != nil {
			x.a.Log.Warn("audit export", "error", err)
			return
		}
		store = s
	}
	for i := 0; i < 10; i++ {
		n, err := x.exportBatch(ctx, cfg, store)
		if err != nil {
			x.a.Log.Warn("audit export", "error", err)
			return
		}
		if n < 1000 {
			break
		}
	}
	if store != nil {
		x.retention(ctx, cfg, store, now)
	}
}

// auditLag is how far back each export reads again. A row whose transaction commits after
// a newer row is behind the cursor; the export still finds it within this window.
const auditLag = time.Minute

func (x *Enterprise) exportBatch(ctx context.Context, cfg AuditConfig, store *objects.S3) (int, error) {
	cursor := time.Unix(0, 0).UTC()
	if c := x.a.Settings.String(ctx, nil, "ee.audit_cursor"); c != "" {
		at, _, _ := strings.Cut(c, "|")
		if t, err := time.Parse(time.RFC3339Nano, at); err == nil {
			cursor = t.UTC()
		}
	}
	// sent holds the rows of the window that earlier exports sent: id -> created_at.
	sent := map[string]time.Time{}
	_ = json.Unmarshal([]byte(x.a.Settings.String(ctx, nil, "ee.audit_sent")), &sent)
	from := cursor.Add(-auditLag)
	rows, err := x.a.DB.Engine.Query(ctx, `SELECT id, created_at, actor_kind, actor_id, space_id, action, target, details FROM audit_log
		WHERE created_at > $1 ORDER BY created_at, id LIMIT $2`, from, 1000+len(sent))
	if err != nil {
		return 0, err
	}
	var lines []auditLine
	for rows.Next() && len(lines) < 1000 {
		var l auditLine
		var id uuid.UUID
		var details any
		if err := rows.Scan(&id, &l.At, &l.ActorKind, &l.ActorID, &l.SpaceID, &l.Action, &l.Target, &details); err != nil {
			rows.Close()
			return 0, err
		}
		l.ID, l.At = id.String(), l.At.UTC()
		if _, done := sent[l.ID]; done {
			continue
		}
		switch d := details.(type) {
		case string:
			_ = json.Unmarshal([]byte(d), &l.Details)
		case []byte:
			_ = json.Unmarshal(d, &l.Details)
		case map[string]any:
			l.Details = map[string]string{}
			for k, v := range d {
				l.Details[k] = fmt.Sprint(v)
			}
		}
		lines = append(lines, l)
	}
	rows.Close()
	if len(lines) == 0 {
		return 0, nil
	}
	var body bytes.Buffer
	enc := json.NewEncoder(&body)
	for _, l := range lines {
		if err := enc.Encode(l); err != nil {
			return 0, err
		}
	}
	first := lines[0]
	if store != nil {
		key := fmt.Sprintf("%s%s/%s-%s.ndjson", cfg.Prefix, first.At.Format("2006/01/02"), first.At.Format("150405.000000000"), first.ID)
		if err := store.Put(ctx, key, bytes.NewReader(body.Bytes()), int64(body.Len())); err != nil {
			return 0, fmt.Errorf("audit file %s: %w", key, err)
		}
	}
	if cfg.WebhookURL != "" {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.WebhookURL, bytes.NewReader(body.Bytes()))
		if err != nil {
			return 0, err
		}
		req.Header.Set("Content-Type", "application/x-ndjson")
		req.Header.Set("X-Fettle-Timestamp", ts)
		req.Header.Set("X-Fettle-Signature", SignWebhook(cfg.WebhookSecret, ts, body.Bytes()))
		resp, err := x.a.HTTP.Do(req)
		if err != nil {
			return 0, fmt.Errorf("audit webhook: %w", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			return 0, fmt.Errorf("audit webhook answered %d", resp.StatusCode)
		}
	}
	for _, l := range lines {
		sent[l.ID] = l.At
		if l.At.After(cursor) {
			cursor = l.At
		}
	}
	for id, at := range sent {
		if !at.After(cursor.Add(-auditLag)) {
			delete(sent, id)
		}
	}
	b, _ := json.Marshal(sent)
	if _, err := x.a.Settings.Set(ctx, audit.System, nil, "ee.audit_sent", string(b)); err != nil {
		return 0, err
	}
	last := lines[len(lines)-1]
	if _, err := x.a.Settings.Set(ctx, audit.System, nil, "ee.audit_cursor", cursor.Format(time.RFC3339Nano)+"|"+last.ID); err != nil {
		return 0, err
	}
	return len(lines), nil
}

// retention deletes export files older than the retention, once a day, or at once when
// now is true (an admin runs the export).
func (x *Enterprise) retention(ctx context.Context, cfg AuditConfig, store *objects.S3, now bool) {
	if last, err := time.Parse(time.RFC3339, x.a.Settings.String(ctx, nil, "ee.audit_retention_at")); !now && err == nil && time.Since(last) < 24*time.Hour {
		return
	}
	days := x.a.Settings.Int(ctx, nil, "ee.audit_retention_days")
	if days <= 0 {
		return
	}
	if _, err := x.a.Settings.Set(ctx, audit.System, nil, "ee.audit_retention_at", time.Now().UTC().Format(time.RFC3339)); err != nil {
		return
	}
	cut := time.Now().UTC().AddDate(0, 0, -days)
	keys, err := store.List(ctx, cfg.Prefix)
	if err != nil {
		x.a.Log.Warn("audit retention", "error", err)
		return
	}
	for _, k := range keys {
		rest := strings.TrimPrefix(k, cfg.Prefix)
		if len(rest) < 10 {
			continue
		}
		day, err := time.Parse("2006/01/02", rest[:10])
		if err != nil || !day.Before(cut.Truncate(24*time.Hour)) {
			continue
		}
		if err := store.Delete(ctx, k); err != nil {
			x.a.Log.Warn("audit retention", "key", k, "error", err)
		}
	}
}

// AuditStatus is the state of the audit export.
type AuditStatus struct {
	S3            bool   `json:"s3"`
	Webhook       bool   `json:"webhook"`
	Cursor        string `json:"cursor,omitempty"`
	RetentionDays int    `json:"retention_days"`
}

func (x *Enterprise) auditRoutes(api huma.API) {
	huma.Register(api, huma.Operation{OperationID: "audit-export-status", Method: http.MethodGet, Path: app.APIPrefix + "/admin/audit-export", Summary: "The audit export: its targets, cursor and retention", Tags: []string{"enterprise"}}, func(ctx context.Context, _ *struct{}) (*struct{ Body AuditStatus }, error) {
		if err := adminOnly(ctx); err != nil {
			return nil, err
		}
		c := auditConfig()
		return &struct{ Body AuditStatus }{Body: AuditStatus{S3: c.S3 != nil, Webhook: c.WebhookURL != "", Cursor: x.a.Settings.String(ctx, nil, "ee.audit_cursor"), RetentionDays: x.a.Settings.Int(ctx, nil, "ee.audit_retention_days")}}, nil
	})
	huma.Register(api, huma.Operation{OperationID: "audit-export-run", Method: http.MethodPost, Path: app.APIPrefix + "/admin/audit-export/run", Summary: "Run the audit export now", Tags: []string{"enterprise"}}, func(ctx context.Context, _ *struct{}) (*struct{}, error) {
		if err := adminOnly(ctx); err != nil {
			return nil, err
		}
		if err := x.writable(ctx, licensekey.FeatureAudit); err != nil {
			return nil, err
		}
		x.exportAudit(ctx, true)
		return nil, nil
	})
}
