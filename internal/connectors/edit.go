package connectors

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/alternayte/drel"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/alternayte/fettle/internal/audit"
)

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

// Slug returns the form of a connector name or an account label in tool names.
func Slug(s string) string {
	s = strings.Trim(slugRE.ReplaceAllString(strings.ToLower(s), "_"), "_")
	if s == "" {
		s = "x"
	}
	return s
}

// Patch edits a connector. A nil field stays as it is. The kind cannot change.
type Patch struct {
	Name       *string
	Icon       *string
	URL        *string
	HeaderName *string
	// Command replaces the command of a stdio connector.
	Command []string
	// Env replaces all plain variables of a stdio connector.
	Env map[string]string
}

// Update edits a connector. Its accounts, their grants and its tool settings stay. A new URL
// of an OAuth connector registers Fettle again and clears the tokens of its accounts, so
// each account signs in again. A new name rewrites the policy rules that name the connector.
func (s *Service) Update(ctx context.Context, actor audit.Actor, spaceID, id uuid.UUID, p Patch) (*Connector, error) {
	c, err := s.Get(ctx, spaceID, id)
	if err != nil {
		return nil, err
	}
	var changed []string
	details := map[string]string{}
	name := c.Name
	if p.Name != nil {
		name = strings.TrimSpace(*p.Name)
		if name == "" {
			return nil, ErrBadInput
		}
		if name != c.Name {
			changed = append(changed, "name")
			details["previous_name"] = c.Name
		}
	}
	if p.URL != nil && *p.URL != c.URL {
		if c.Kind == "mcp_stdio" {
			return nil, fmt.Errorf("%w: a stdio connector has a command, not a URL", ErrInvalid)
		}
		if err := checkURL(*p.URL); err != nil {
			return nil, err
		}
		changed = append(changed, "url")
	}
	if p.Command != nil {
		if c.Kind != "mcp_stdio" {
			return nil, fmt.Errorf("%w: only a stdio connector has a command", ErrInvalid)
		}
		if len(p.Command) == 0 || p.Command[0] == "" {
			return nil, ErrBadInput
		}
		if !slices.Equal(p.Command, c.Command) {
			changed = append(changed, "command")
		}
	}
	if p.Env != nil {
		if c.Kind != "mcp_stdio" {
			return nil, fmt.Errorf("%w: only a stdio connector has variables; a remote connector uses a header", ErrInvalid)
		}
		if err := checkEnv(p.Env); err != nil {
			return nil, err
		}
		if !maps.Equal(p.Env, c.Env) {
			changed = append(changed, "env")
			details["env"] = strings.Join(envNames(p.Env), ",")
		}
	}
	if p.HeaderName != nil && strings.TrimSpace(*p.HeaderName) != c.HeaderName {
		if c.Auth != "header" {
			return nil, fmt.Errorf("%w: only a connector with a header key has a header name", ErrInvalid)
		}
		if strings.TrimSpace(*p.HeaderName) == "" {
			return nil, fmt.Errorf("%w: the header name is empty", ErrInvalid)
		}
		changed = append(changed, "header_name")
	}
	if p.Icon != nil && *p.Icon != c.Icon {
		changed = append(changed, "icon")
	}
	if len(changed) == 0 {
		return c, nil
	}
	if name != c.Name {
		taken, err := s.nameTaken(ctx, s.e, spaceID, id, name)
		if err != nil {
			return nil, err
		}
		if taken {
			return nil, ErrNameTaken
		}
	}
	var oc *OAuthClient
	if slices.Contains(changed, "url") && c.Auth == "oauth" {
		if oc, err = s.registerSealed(ctx, c, *p.URL); err != nil {
			return nil, err
		}
		details["sign_in_again"] = "true"
	}
	details["fields"] = strings.Join(changed, ",")
	details["name"] = name
	var out *Connector
	err = s.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		tc, err := drel.Repo(tx, ConnectorMeta).Where(Connectors.ID.Eq(id)).Where(Connectors.SpaceID.Eq(spaceID)).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		if tc == nil {
			return ErrNotFound
		}
		old := tc.Name
		tc.Name = name
		if p.URL != nil {
			tc.URL = *p.URL
		}
		if p.Command != nil {
			tc.Command = slices.Clone(p.Command)
		}
		if p.Env != nil {
			tc.Env = maps.Clone(p.Env)
		}
		if p.HeaderName != nil && c.Auth == "header" {
			tc.HeaderName = strings.TrimSpace(*p.HeaderName)
		}
		if p.Icon != nil {
			tc.Icon = *p.Icon
		}
		if oc != nil {
			tc.OAuth = *oc
			if _, err := tx.Exec(ctx, "UPDATE connector_accounts SET token_ciphertext = '', expires_at = NULL, updated_at = $1 WHERE connector_id = $2", s.now(), id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, "DELETE FROM oauth_states WHERE connector_id = $1", id); err != nil {
				return err
			}
		}
		if old != name {
			if err := s.renameRules(ctx, tx, spaceID, id, old, name); err != nil {
				return err
			}
		}
		out = tc
		return s.audit.Record(ctx, actor, &spaceID, "connector.update", "connector:"+id.String(), details)
	})
	if err != nil {
		return nil, err
	}
	s.Changed()
	return out, nil
}

// renameRules rewrites the policy rules that name a connector, by its name or by the
// prefix of its tool names.
func (s *Service) renameRules(ctx context.Context, tx *drel.Tx, spaceID, id uuid.UUID, old, name string) error {
	if _, err := tx.Exec(ctx, "UPDATE policy_rules SET connector = $1, updated_at = $2 WHERE space_id = $3 AND connector = $4", name, s.now(), spaceID, old); err != nil {
		return err
	}
	if Slug(old) == Slug(name) {
		return nil
	}
	prefixes := map[string]string{Slug(old) + "__": Slug(name) + "__"}
	rows, err := tx.Query(ctx, "SELECT label FROM connector_accounts WHERE connector_id = $1", id)
	if err != nil {
		return err
	}
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err != nil {
			rows.Close()
			return err
		}
		prefixes[Slug(old)+"_"+Slug(label)+"__"] = Slug(name) + "_" + Slug(label) + "__"
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	return s.rewriteToolRules(ctx, tx, spaceID, prefixes)
}

// rewriteToolRules changes the tool prefix of the policy rules of a space, from each key of
// prefixes to its value.
func (s *Service) rewriteToolRules(ctx context.Context, tx *drel.Tx, spaceID uuid.UUID, prefixes map[string]string) error {
	rows, err := tx.Query(ctx, "SELECT id, tool FROM policy_rules WHERE space_id = $1 AND tool <> ''", spaceID)
	if err != nil {
		return err
	}
	type change struct {
		id   uuid.UUID
		tool string
	}
	var changes []change
	for rows.Next() {
		var rid uuid.UUID
		var tool string
		if err := rows.Scan(&rid, &tool); err != nil {
			rows.Close()
			return err
		}
		for from, to := range prefixes {
			if rest, ok := strings.CutPrefix(tool, from); ok {
				changes = append(changes, change{rid, to + rest})
				break
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, ch := range changes {
		if _, err := tx.Exec(ctx, "UPDATE policy_rules SET tool = $1, updated_at = $2 WHERE id = $3", ch.tool, s.now(), ch.id); err != nil {
			return err
		}
	}
	return nil
}

// AccountPatch edits an account. A nil field stays as it is.
type AccountPatch struct {
	Label *string
	// Value replaces the header key.
	Value *string
	// SecretEnv sets the secret variables it names; an empty value removes the variable.
	SecretEnv map[string]string
}

// UpdateAccount renames an account or replaces its key or secret variables. The account
// keeps its grants. No old value leaves the vault.
func (s *Service) UpdateAccount(ctx context.Context, actor audit.Actor, spaceID, accountID uuid.UUID, p AccountPatch) (*ConnectorAccount, error) {
	a, err := s.Account(ctx, spaceID, accountID)
	if err != nil {
		return nil, err
	}
	c, err := s.Get(ctx, spaceID, a.ConnectorID)
	if err != nil {
		return nil, err
	}
	details := map[string]string{"account": a.ID().String()}
	label := a.Label
	if p.Label != nil {
		label = strings.TrimSpace(*p.Label)
		if label == "" {
			return nil, fmt.Errorf("%w: the label is empty", ErrInvalid)
		}
		if label != a.Label {
			details["previous_label"] = a.Label
		}
	}
	details["label"] = label
	var sealed string
	var names []string
	switch {
	case p.Value != nil && p.SecretEnv != nil:
		return nil, fmt.Errorf("%w: give a key or secret variables, not both", ErrInvalid)
	case p.Value != nil:
		if c.Auth != "header" {
			return nil, fmt.Errorf("%w: only an account with a header key has a key", ErrInvalid)
		}
		if *p.Value == "" {
			return nil, fmt.Errorf("%w: the key is empty", ErrInvalid)
		}
		b, err := s.vault.Seal(ctx, c.SpaceID, []byte(*p.Value), aad(a.ID()))
		if err != nil {
			return nil, err
		}
		sealed = base64.StdEncoding.EncodeToString(b)
		details["key"] = "replaced"
	case p.SecretEnv != nil:
		if c.Kind != "mcp_stdio" {
			return nil, fmt.Errorf("%w: only a stdio account has secret variables", ErrInvalid)
		}
		if err := checkEnv(p.SecretEnv); err != nil {
			return nil, err
		}
		env, err := s.secretEnv(ctx, c, a)
		if err != nil {
			return nil, err
		}
		var set, removed []string
		for k, v := range p.SecretEnv {
			if v == "" {
				delete(env, k)
				removed = append(removed, k)
				continue
			}
			env[k] = v
			set = append(set, k)
		}
		slices.Sort(set)
		slices.Sort(removed)
		raw, _ := json.Marshal(env)
		b, err := s.vault.Seal(ctx, c.SpaceID, raw, aad(a.ID()))
		if err != nil {
			return nil, err
		}
		sealed = base64.StdEncoding.EncodeToString(b)
		names = envNames(env)
		details["secret_env_set"] = strings.Join(set, ",")
		details["secret_env_removed"] = strings.Join(removed, ",")
	}
	if sealed == "" && label == a.Label {
		return a, nil
	}
	var out *ConnectorAccount
	err = s.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		ta, err := drel.Repo(tx, ConnectorAccountMeta).Where(ConnectorAccounts.ID.Eq(accountID)).Where(ConnectorAccounts.SpaceID.Eq(spaceID)).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		if ta == nil {
			return ErrNoAccount
		}
		old := ta.Label
		ta.Label = label
		if sealed != "" {
			ta.TokenCiphertext = sealed
		}
		if names != nil {
			ta.SecretEnvNames = names
		}
		if old != label && Slug(old) != Slug(label) {
			from := Slug(c.Name) + "_" + Slug(old) + "__"
			if err := s.rewriteToolRules(ctx, tx, spaceID, map[string]string{from: Slug(c.Name) + "_" + Slug(label) + "__"}); err != nil {
				return err
			}
		}
		out = ta
		return s.audit.Record(ctx, actor, &spaceID, "connector.account_update", "connector:"+c.ID().String(), details)
	})
	if err != nil {
		return nil, err
	}
	s.Changed()
	return out, nil
}

// secretEnv opens the secret variables of a stdio account.
func (s *Service) secretEnv(ctx context.Context, c *Connector, a *ConnectorAccount) (map[string]string, error) {
	env := map[string]string{}
	if a.TokenCiphertext == "" {
		return env, nil
	}
	sealed, err := base64.StdEncoding.DecodeString(a.TokenCiphertext)
	if err != nil {
		return nil, err
	}
	raw, err := s.vault.Open(ctx, c.SpaceID, sealed, aad(a.ID()))
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return env, nil // a stdio account from before secret variables
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	return env, nil
}

// StdioEnv returns the variables of a stdio process for one account: the plain variables
// of the connector, and the secret variables of the account on top. It registers the
// secret values for redaction and fails when a required variable is not set.
func (s *Service) StdioEnv(ctx context.Context, c *Connector, a *ConnectorAccount) (map[string]string, error) {
	env := maps.Clone(c.Env)
	if env == nil {
		env = map[string]string{}
	}
	secrets, err := s.secretEnv(ctx, c, a)
	if err != nil {
		return nil, err
	}
	for k, v := range secrets {
		s.vault.Redactor.Register(c.SpaceID, c.Name+" "+k, v)
		env[k] = v
	}
	for _, v := range c.Variables {
		if v.Required && strings.TrimSpace(env[v.Name]) == "" {
			where := "connector"
			if v.Secret {
				where = "account " + a.Label
			}
			return nil, fmt.Errorf("%w: %s; set it on the %s", ErrMissingVar, v.Name, where)
		}
	}
	return env, nil
}

// dropGone removes the settings of tools that the server no longer lists.
func (s *Service) dropGone(ctx context.Context, c *Connector, listed []*mcp.Tool) error {
	have := map[string]bool{}
	for _, t := range listed {
		have[t.Name] = true
	}
	var gone []string
	for k := range c.Tools {
		if !have[k] {
			gone = append(gone, k)
		}
	}
	if len(gone) == 0 {
		return nil
	}
	slices.Sort(gone)
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		tc, err := drel.Repo(drel.MustFromContext(ctx), ConnectorMeta).Where(Connectors.ID.Eq(c.ID())).FirstOrNil(ctx)
		if err != nil || tc == nil {
			return err
		}
		tools := maps.Clone(tc.Tools)
		for _, k := range gone {
			delete(tools, k)
		}
		tc.Tools = tools
		return s.audit.Record(ctx, audit.System, &c.SpaceID, "connector.tool", "connector:"+c.ID().String(), map[string]string{"dropped": strings.Join(gone, ",")})
	})
	if err != nil {
		return err
	}
	tools := maps.Clone(c.Tools)
	for _, k := range gone {
		delete(tools, k)
	}
	c.Tools = tools
	return nil
}

// SealClientSecrets seals the OAuth client secrets that a migration found in plain text.
func (s *Service) SealClientSecrets(ctx context.Context) error {
	cs, err := drel.NewRepository(s.e, ConnectorMeta).Where(Connectors.Auth.Eq("oauth")).All(ctx)
	if err != nil {
		return err
	}
	for _, c := range cs {
		if c.OAuth.PlainSecret == "" {
			continue
		}
		b, err := s.vault.Seal(ctx, c.SpaceID, []byte(c.OAuth.PlainSecret), oauthAAD(c.ID()))
		if err != nil {
			return err
		}
		err = s.e.WithTx(ctx, func(ctx context.Context) error {
			tc, err := drel.Repo(drel.MustFromContext(ctx), ConnectorMeta).Where(Connectors.ID.Eq(c.ID())).FirstOrNil(ctx)
			if err != nil || tc == nil {
				return err
			}
			oc := tc.OAuth
			oc.ClientSecret = base64.StdEncoding.EncodeToString(b)
			oc.PlainSecret = ""
			tc.OAuth = oc
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
