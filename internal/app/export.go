package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"
	"golang.org/x/crypto/scrypt"

	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/vault"
)

// ExportFormat names the space archive format (§17).
const ExportFormat = "fettle-space-export"

// exportTable is one table of the space archive: its rows are those of the space.
type exportTable struct {
	name  string
	where string // condition on the space ID, as $1
}

// exportTables are the tables of a space archive in import order: bots, chats, memory,
// skills, routines, zones without grants, workspaces and the space settings (§17).
var exportTables = []exportTable{
	{"spaces", "id = $1"},
	{"zones", "space_id = $1"},
	{"bots", "space_id = $1"},
	{"bot_instructions", "bot_id IN (SELECT id FROM bots WHERE space_id = $1)"},
	{"bot_memory", "space_id = $1"},
	{"open_work", "bot_id IN (SELECT id FROM bots WHERE space_id = $1)"},
	{"chats", "space_id = $1"},
	{"chat_members", "chat_id IN (SELECT id FROM chats WHERE space_id = $1)"},
	{"messages", "space_id = $1"},
	{"skills", "space_id = $1"},
	{"skill_versions", "skill_id IN (SELECT id FROM skills WHERE space_id = $1)"},
	{"bot_skills", "bot_id IN (SELECT id FROM bots WHERE space_id = $1)"},
	{"routines", "space_id = $1"},
	{"workspaces", "space_id = $1"},
	{"settings", "space_id = $1"},
}

// ExportTableNames lists the tables of a space archive.
func ExportTableNames() []string {
	out := make([]string, len(exportTables))
	for i, t := range exportTables {
		out[i] = t.name
	}
	return out
}

// exportManifest is manifest.json of a space archive.
type exportManifest struct {
	Format    string    `json:"format"`
	Version   int       `json:"version"`
	SpaceID   string    `json:"space_id"`
	SpaceName string    `json:"space_name"`
	Users     []string  `json:"users"` // the members of the source space; import maps them to the importer
	Vault     bool      `json:"vault"`
	CreatedAt time.Time `json:"created_at"`
	FettleVer string    `json:"fettle_version"`
}

// exportVaultItem is one vault item inside the passphrase-sealed vault.json.
type exportVaultItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Site     string `json:"site"`
	Username string `json:"username"`
	Value    string `json:"value"`
}

// normalize turns a database value into a JSON value that every dialect reads back.
func normalize(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		return string(x)
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	case [16]byte:
		return uuid.UUID(x).String()
	case uuid.UUID:
		return x.String()
	case map[string]any, []any:
		b, _ := json.Marshal(x)
		return string(b)
	}
	return v
}

// ExportSpace writes the portable archive of a space (REQ-CORE-011). The vault goes in
// only with a passphrase, sealed with a key from it; grants and runs stay out.
func (a *App) ExportSpace(ctx context.Context, spaceID uuid.UUID, w io.Writer, passphrase string) error {
	sp, err := a.Spaces.Get(ctx, spaceID)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	put := func(name string, b []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(b)), ModTime: time.Now()}); err != nil {
			return err
		}
		_, err := tw.Write(b)
		return err
	}
	man := exportManifest{Format: ExportFormat, Version: 1, SpaceID: spaceID.String(), SpaceName: sp.Name, Vault: passphrase != "", CreatedAt: time.Now().UTC(), FettleVer: a.Cfg.Version}
	members, err := a.Spaces.Members(ctx, spaceID)
	if err != nil {
		return err
	}
	for _, m := range members {
		man.Users = append(man.Users, m.UserID.String())
	}
	var objects []string
	var sealedObjects []string // whole-object seals with the key as AAD
	for _, t := range exportTables {
		types, err := a.columnTypes(ctx, t.name)
		if err != nil {
			return err
		}
		cols := make([]string, 0, len(types))
		for c := range types {
			cols = append(cols, c)
		}
		sort.Strings(cols)
		quoted := make([]string, len(cols))
		for i, c := range cols {
			quoted[i] = `"` + c + `"`
		}
		rows, err := a.DB.Engine.Query(ctx, "SELECT "+strings.Join(quoted, ", ")+" FROM "+t.name+" WHERE "+t.where, spaceID)
		if err != nil {
			return fmt.Errorf("export %s: %w", t.name, err)
		}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				return err
			}
			row := map[string]any{}
			for i, c := range cols {
				row[c] = normalize(vals[i])
			}
			switch t.name {
			case "routines":
				// The webhook secret is sealed with this instance's key: export it open, so the
				// importer seals it again.
				if s, _ := row["webhook_secret"].(string); s != "" {
					sealed, err := base64.StdEncoding.DecodeString(s)
					if err != nil {
						rows.Close()
						return err
					}
					plain, err := a.Vault.Open(ctx, spaceID, sealed, "routine:"+fmt.Sprint(row["id"]))
					if err != nil {
						rows.Close()
						return err
					}
					row["webhook_secret"] = "plain:" + base64.StdEncoding.EncodeToString(plain)
				}
			case "skill_versions":
				if k, _ := row["object_key"].(string); k != "" {
					sealedObjects = append(sealedObjects, k)
				}
			case "messages":
				var atts []struct {
					ObjectKey string `json:"object_key"`
				}
				if s, _ := row["attachments"].(string); s != "" && json.Unmarshal([]byte(s), &atts) == nil {
					for _, at := range atts {
						if at.ObjectKey != "" {
							sealedObjects = append(sealedObjects, at.ObjectKey)
						}
					}
				}
			case "workspaces":
				objects = append(objects, fmt.Sprintf("%v|%v|%v", row["object_key"], row["id"], row["rev"]))
			}
			if err := enc.Encode(row); err != nil {
				rows.Close()
				return err
			}
		}
		rows.Close()
		if err := put("tables/"+t.name+".ndjson", buf.Bytes()); err != nil {
			return err
		}
	}
	// Objects, open: the importer seals them with its own keys.
	for _, k := range sealedObjects {
		b, err := a.readObject(ctx, k)
		if err != nil {
			return fmt.Errorf("object %s: %w", k, err)
		}
		plain, err := a.Vault.Open(ctx, spaceID, b, k)
		if err != nil {
			return fmt.Errorf("object %s: %w", k, err)
		}
		if err := put("objects/"+k, plain); err != nil {
			return err
		}
	}
	for _, o := range objects {
		parts := strings.SplitN(o, "|", 3)
		key, id, rev := parts[0], parts[1], parts[2]
		if key == "" || key == "<nil>" {
			continue
		}
		rc, err := a.Objects.Get(ctx, key)
		if err != nil {
			return fmt.Errorf("workspace %s: %w", key, err)
		}
		r, err := a.Vault.OpenStream(ctx, spaceID, rc, fmt.Sprintf("fettle:workspace:%s:%s", id, rev))
		if err != nil {
			_ = rc.Close()
			return err
		}
		plain, err := io.ReadAll(r)
		_ = rc.Close()
		if err != nil {
			return fmt.Errorf("workspace %s: %w", key, err)
		}
		if err := put("workspaces/"+key, plain); err != nil {
			return err
		}
	}
	if passphrase != "" {
		items, err := a.Vault.List(ctx, spaceID)
		if err != nil {
			return err
		}
		var list []exportVaultItem
		for _, it := range items {
			v, err := a.Vault.Reveal(ctx, spaceID, it.ID())
			if err != nil {
				return err
			}
			list = append(list, exportVaultItem{ID: it.ID().String(), Name: it.Name, Kind: it.Kind, Site: it.Site, Username: it.Username, Value: v})
		}
		plain, _ := json.Marshal(list)
		sealed, err := sealWithPassphrase(passphrase, plain)
		if err != nil {
			return err
		}
		if err := put("vault.sealed", sealed); err != nil {
			return err
		}
	}
	mb, _ := json.MarshalIndent(man, "", "  ")
	if err := put("manifest.json", mb); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func (a *App) readObject(ctx context.Context, key string) ([]byte, error) {
	rc, err := a.Objects.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

// sealWithPassphrase seals data with AES-256-GCM under a key from scrypt.
func sealWithPassphrase(pass string, plain []byte) ([]byte, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key, err := scrypt.Key([]byte(pass), salt, 1<<15, 8, 1, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append(append(salt, nonce...), gcm.Seal(nil, nonce, plain, []byte(ExportFormat))...)
	return out, nil
}

// ErrPassphrase means the passphrase does not open the archive's vault.
var ErrPassphrase = errors.New("the passphrase does not open the vault of this archive")

func openWithPassphrase(pass string, sealed []byte) ([]byte, error) {
	if len(sealed) < 16+12 {
		return nil, ErrPassphrase
	}
	key, err := scrypt.Key([]byte(pass), sealed[:16], 1<<15, 8, 1, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	n := gcm.NonceSize()
	plain, err := gcm.Open(nil, sealed[16:16+n], sealed[16+n:], []byte(ExportFormat))
	if err != nil {
		return nil, ErrPassphrase
	}
	return plain, nil
}

// ImportResult says what an import made.
type ImportResult struct {
	SpaceID string         `json:"space_id"`
	Rows    map[string]int `json:"rows"`
	Objects int            `json:"objects"`
	Vault   int            `json:"vault"`
}

// ErrSpaceExists means the instance already holds the space of the archive.
var ErrSpaceExists = errors.New("this instance already has the space of the archive")

// columnTypes returns the column types of a table in this database, in lower case.
func (a *App) columnTypes(ctx context.Context, table string) (map[string]string, error) {
	out := map[string]string{}
	query := a.DB.Engine.Query
	if tx, ok := drel.FromContext(ctx); ok {
		query = tx.Query
	}
	if a.DB.Postgres {
		rows, err := query(ctx, "SELECT column_name, data_type FROM information_schema.columns WHERE table_name = $1 AND table_schema = current_schema()", table)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var c, t string
			if err := rows.Scan(&c, &t); err != nil {
				return nil, err
			}
			out[c] = strings.ToLower(t)
		}
		return out, rows.Err()
	}
	rows, err := query(ctx, "SELECT name, type FROM pragma_table_info('"+table+"')")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c, t string
		if err := rows.Scan(&c, &t); err != nil {
			return nil, err
		}
		out[c] = strings.ToLower(t)
	}
	return out, rows.Err()
}

// convert turns an archive value into a value for a column of this database.
func (a *App) convert(v any, typ string) (any, error) {
	if v == nil {
		return nil, nil
	}
	switch {
	case strings.Contains(typ, "timestamp") || typ == "datetime" || typ == "date":
		s := fmt.Sprint(v)
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999", "2006-01-02T15:04:05.999999999"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t.UTC(), nil
			}
		}
		return nil, fmt.Errorf("the time %q", s)
	case typ == "boolean":
		switch x := v.(type) {
		case bool:
			if a.DB.Postgres {
				return x, nil
			}
			if x {
				return int64(1), nil
			}
			return int64(0), nil
		case json.Number:
			n, _ := x.Int64()
			if a.DB.Postgres {
				return n != 0, nil
			}
			return n, nil
		}
	case typ == "integer" || typ == "bigint" || typ == "smallint":
		if n, ok := v.(json.Number); ok {
			return n.Int64()
		}
		if b, ok := v.(bool); ok {
			if b {
				return int64(1), nil
			}
			return int64(0), nil
		}
	case typ == "real" || strings.Contains(typ, "double") || typ == "numeric":
		if n, ok := v.(json.Number); ok {
			return n.Float64()
		}
	case typ == "jsonb" || typ == "json":
		return []byte(fmt.Sprint(v)), nil
	}
	if n, ok := v.(json.Number); ok {
		if i, err := n.Int64(); err == nil {
			return i, nil
		}
		return n.Float64()
	}
	return v, nil
}

// ImportSpace reads a space archive into this instance with owner as its owner
// (REQ-CORE-011). The members of the source space become owner in every user column.
func (a *App) ImportSpace(ctx context.Context, r io.Reader, owner uuid.UUID, passphrase string) (*ImportResult, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("the file is not a space archive: %w", err)
	}
	files := map[string][]byte{}
	tr := tar.NewReader(gz)
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(tr, 4<<30))
		if err != nil {
			return nil, err
		}
		total += int64(len(b))
		if total > 16<<30 {
			return nil, errors.New("the archive is larger than 16 GiB")
		}
		files[h.Name] = b
	}
	var man exportManifest
	if err := json.Unmarshal(files["manifest.json"], &man); err != nil || man.Format != ExportFormat {
		return nil, errors.New("the file is not a Fettle space archive")
	}
	if man.Version != 1 {
		return nil, fmt.Errorf("this Fettle reads archive version 1, not %d", man.Version)
	}
	spaceID, err := uuid.Parse(man.SpaceID)
	if err != nil {
		return nil, err
	}
	if _, err := a.Spaces.Get(ctx, spaceID); err == nil {
		return nil, ErrSpaceExists
	}
	users := map[string]bool{}
	for _, u := range man.Users {
		users[u] = true
	}
	// The vault opens before anything is written, so a wrong passphrase changes nothing.
	var items []exportVaultItem
	if man.Vault && passphrase != "" {
		plain, err := openWithPassphrase(passphrase, files["vault.sealed"])
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(plain, &items); err != nil {
			return nil, err
		}
	}
	res := &ImportResult{SpaceID: spaceID.String(), Rows: map[string]int{}}
	err = a.DB.Engine.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		for _, t := range exportTables {
			types, err := a.columnTypes(ctx, t.name)
			if err != nil {
				return err
			}
			dec := json.NewDecoder(bytes.NewReader(files["tables/"+t.name+".ndjson"]))
			dec.UseNumber()
			for {
				var row map[string]any
				if err := dec.Decode(&row); errors.Is(err, io.EOF) {
					break
				} else if err != nil {
					return fmt.Errorf("%s: %w", t.name, err)
				}
				if t.name == "zones" {
					row["provider_id"] = nil // providers belong to the instance; the default one runs the zone
				}
				var cols []string
				var args []any
				for c, v := range row {
					typ, ok := types[c]
					if !ok {
						continue // a column this version does not have
					}
					if s, ok := v.(string); ok && users[s] {
						v = owner.String()
					}
					if t.name == "routines" && c == "webhook_secret" {
						if s, _ := v.(string); strings.HasPrefix(s, "plain:") {
							plain, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s, "plain:"))
							if err != nil {
								return err
							}
							sealed, err := a.Vault.Seal(ctx, spaceID, plain, "routine:"+fmt.Sprint(row["id"]))
							if err != nil {
								return err
							}
							v = base64.StdEncoding.EncodeToString(sealed)
						}
					}
					cv, err := a.convert(v, typ)
					if err != nil {
						return fmt.Errorf("%s.%s: %w", t.name, c, err)
					}
					cols = append(cols, `"`+c+`"`)
					args = append(args, cv)
				}
				ph := make([]string, len(cols))
				for i := range cols {
					ph[i] = "$" + strconv.Itoa(i+1)
				}
				if _, err := tx.Exec(ctx, "INSERT INTO "+t.name+" ("+strings.Join(cols, ", ")+") VALUES ("+strings.Join(ph, ", ")+")", args...); err != nil {
					return fmt.Errorf("%s: %w", t.name, err)
				}
				res.Rows[t.name]++
			}
			if t.name == "spaces" {
				if _, err := tx.Exec(ctx, "INSERT INTO space_members (id, space_id, user_id, role, created_at, updated_at) VALUES ($1, $2, $3, 'owner', $4, $5)", uuid.Must(uuid.NewV7()), spaceID, owner, time.Now().UTC(), time.Now().UTC()); err != nil {
					return err
				}
			}
		}
		for _, it := range items {
			if _, err := a.Vault.Create(ctx, audit.Actor{Kind: "user", ID: owner.String()}, spaceID, vault.Input{Name: it.Name, Kind: it.Kind, Site: it.Site, Username: it.Username, Value: it.Value}); err != nil {
				return err
			}
			res.Vault++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Objects, sealed with this instance's keys under the same keys and AADs.
	for name, b := range files {
		switch {
		case strings.HasPrefix(name, "objects/"):
			key := strings.TrimPrefix(name, "objects/")
			sealed, err := a.Vault.Seal(ctx, spaceID, b, key)
			if err != nil {
				return nil, err
			}
			if err := a.Objects.Put(ctx, key, bytes.NewReader(sealed), int64(len(sealed))); err != nil {
				return nil, err
			}
			res.Objects++
		case strings.HasPrefix(name, "workspaces/"):
			key := strings.TrimPrefix(name, "workspaces/")
			var id string
			var rev int64
			if err := a.DB.Engine.QueryRow(ctx, "SELECT id, rev FROM workspaces WHERE object_key = $1", key).Scan(&id, &rev); err != nil {
				return nil, fmt.Errorf("workspace %s: %w", key, err)
			}
			var buf bytes.Buffer
			sw, err := a.Vault.SealStream(ctx, spaceID, &buf, fmt.Sprintf("fettle:workspace:%s:%d", id, rev))
			if err != nil {
				return nil, err
			}
			if _, err := sw.Write(b); err != nil {
				return nil, err
			}
			if err := sw.Close(); err != nil {
				return nil, err
			}
			if err := a.Objects.Put(ctx, key, bytes.NewReader(buf.Bytes()), int64(buf.Len())); err != nil {
				return nil, err
			}
			res.Objects++
		}
	}
	return res, a.Audit.Record(ctx, audit.Actor{Kind: "user", ID: owner.String()}, &spaceID, "space.import", "space:"+spaceID.String(), map[string]string{"archive_version": strconv.Itoa(man.Version)})
}
