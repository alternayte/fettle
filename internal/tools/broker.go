package tools

import (
	"context"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // TOTP (RFC 6238) uses HMAC-SHA1
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/alternayte/fettle/agentd/client"
	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/envelopes"
	"github.com/alternayte/fettle/internal/vault"
	"github.com/alternayte/fettle/internal/zones"
)

// Broker gives bots vault values without putting them in model context (§11.4). It
// releases an item only to a bot whose zone holds a grant for it (SI-03).
type Broker struct {
	*Browser
	Vault *vault.Vault
	Zones *zones.Service
	Now   func() time.Time
}

// ErrNoGrant means the bot's zone has no grant for the item.
var ErrNoGrant = errors.New("your zone has no access to this vault item; ask the owner to give it to your zone")

// item finds a vault item by name and checks the grant of the bot's zone.
func (b *Broker) item(ctx context.Context, env *agent.ToolEnv, name string) (*vault.VaultItem, string, error) {
	bot, err := b.Bots.Get(ctx, env.SpaceID, env.BotID)
	if err != nil {
		return nil, "", err
	}
	items, err := b.Vault.List(ctx, env.SpaceID)
	if err != nil {
		return nil, "", err
	}
	var granted []string
	var found *vault.VaultItem
	for _, it := range items {
		ok := false
		if bot.ZoneID != nil {
			ok, _ = b.Zones.HasGrant(ctx, *bot.ZoneID, "vault_item", it.ID())
		}
		if ok {
			granted = append(granted, it.Name)
		}
		if strings.EqualFold(it.Name, strings.TrimSpace(name)) {
			if !ok {
				return nil, "", ErrNoGrant
			}
			found = it
		}
	}
	if found == nil {
		return nil, "", fmt.Errorf("no vault item %q; your zone holds: %s", name, orNone(granted))
	}
	value, err := b.Vault.Reveal(ctx, env.SpaceID, found.ID())
	return found, value, err
}

func orNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// TOTP returns the RFC 6238 code (SHA-1, 30 s, 6 digits) of a base32 seed at t.
func TOTP(seed string, t time.Time) (string, error) {
	s := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(seed), " ", ""))
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return "", errors.New("the TOTP seed is not base32")
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(t.Unix()/30))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	code := (binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff) % 1_000_000
	return fmt.Sprintf("%06d", code), nil
}

var envName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)

// RegisterBroker adds fill_login, totp_code and use_secret (Appendix E).
func (b *Broker) RegisterBroker(r *agent.Registry) {
	r.Add(agent.Tool{
		Name:          "fill_login",
		Screen:        true,
		Description:   "Fill a login from the vault into the current page: the user name into one field ref and the password into another. Fettle fills only a page on the login's site. You never see the password.",
		NeedsComputer: true,
		Class:         envelopes.ClassWriteLocal,
		Schema:        schema(`{"type":"object","properties":{"item":{"type":"string","description":"The vault item name."},"username_ref":{"type":"string"},"password_ref":{"type":"string"}},"required":["item","password_ref"],"additionalProperties":false}`),
		Run: func(ctx context.Context, env *agent.ToolEnv, args json.RawMessage) (agent.ToolOutput, error) {
			in, err := decode[struct {
				Item        string `json:"item"`
				UsernameRef string `json:"username_ref"`
				PasswordRef string `json:"password_ref"`
			}](args)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			it, secret, err := b.item(ctx, env, in.Item)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			if it.Kind != vault.KindLogin {
				return agent.ToolOutput{}, fmt.Errorf("%s is not a login", it.Name)
			}
			fields := []client.FillField{{Ref: in.PasswordRef, Value: secret}}
			if in.UsernameRef != "" {
				fields = append([]client.FillField{{Ref: in.UsernameRef, Value: it.Username}}, fields...)
			}
			l, _, err := b.open(ctx, env)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			defer l.Release()
			if err := l.Client.BrowserFill(ctx, browserID(env), client.FillRequest{Site: it.Site, Fields: fields}); err != nil {
				return agent.ToolOutput{}, err
			}
			return agent.ToolOutput{Text: fmt.Sprintf("Filled the login %s. Now press the sign-in button.", it.Name)}, nil
		},
	})
	r.Add(agent.Tool{
		Name:          "totp_code",
		Screen:        true,
		Description:   "Fill the current one-time code of a TOTP vault item into a field ref of the current page.",
		NeedsComputer: true,
		Class:         envelopes.ClassWriteLocal,
		Schema:        schema(`{"type":"object","properties":{"item":{"type":"string"},"ref":{"type":"string"}},"required":["item","ref"],"additionalProperties":false}`),
		Run: func(ctx context.Context, env *agent.ToolEnv, args json.RawMessage) (agent.ToolOutput, error) {
			in, err := decode[struct{ Item, Ref string }](args)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			it, seed, err := b.item(ctx, env, in.Item)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			if it.Kind != vault.KindTOTP {
				return agent.ToolOutput{}, fmt.Errorf("%s is not a TOTP item", it.Name)
			}
			code, err := TOTP(seed, b.Now())
			if err != nil {
				return agent.ToolOutput{}, err
			}
			l, _, err := b.open(ctx, env)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			defer l.Release()
			if err := l.Client.BrowserFill(ctx, browserID(env), client.FillRequest{Site: it.Site, Fields: []client.FillField{{Ref: in.Ref, Value: code}}}); err != nil {
				return agent.ToolOutput{}, err
			}
			return agent.ToolOutput{Text: "Filled the one-time code of " + it.Name + "."}, nil
		},
	})
	r.Add(agent.Tool{
		Name:          "use_secret",
		Description:   "Run one command with a vault secret in one environment variable. You never see the value; it is hidden in the output.",
		NeedsComputer: true,
		Class:         envelopes.ClassWriteLocal,
		Schema:        schema(`{"type":"object","properties":{"item":{"type":"string"},"env_name":{"type":"string"},"command":{"type":"string"}},"required":["item","env_name","command"],"additionalProperties":false}`),
		Run: func(ctx context.Context, env *agent.ToolEnv, args json.RawMessage) (agent.ToolOutput, error) {
			in, err := decode[struct {
				Item    string `json:"item"`
				EnvName string `json:"env_name"`
				Command string `json:"command"`
			}](args)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			if !envName.MatchString(in.EnvName) {
				return agent.ToolOutput{}, errors.New("env_name must be upper case letters, digits and _")
			}
			_, value, err := b.item(ctx, env, in.Item)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			l, home, err := b.lease(ctx, env)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			defer l.Release()
			var out strings.Builder
			res, err := l.Client.Exec(ctx, client.ExecRequest{Shell: in.Command, Cwd: home, Env: map[string]string{in.EnvName: value}, TimeoutMS: 300000}, func(_, data string) { out.WriteString(data) })
			if err != nil {
				return agent.ToolOutput{}, err
			}
			// The journal and the logs redact vault values (§11.4); this hides it from the model too.
			text := strings.ReplaceAll(out.String(), value, "[secret:"+in.Item+"]")
			return agent.ToolOutput{Text: fmt.Sprintf("exit code %d\n%s", res.ExitCode, offload(ctx, l.Client, home, env, text))}, nil
		},
	})
}
