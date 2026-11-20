package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/alternayte/fettle/internal/auth"
	"github.com/alternayte/fettle/internal/settings"
)

// InstanceSettingView is one setting of the server for instance admins. A secret has no
// value in the view, only whether it is set.
type InstanceSettingView struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value" doc:"The stored value; null for a secret."`
	Default   json.RawMessage `json:"default" doc:"The value that applies when nothing is stored."`
	IsDefault bool            `json:"is_default"`
	Secret    bool            `json:"secret"`
	Set       bool            `json:"set" doc:"A secret has a value."`
	Locked    bool            `json:"locked" doc:"An environment variable sets this value, so the API cannot change it."`
	Env       string          `json:"env" doc:"The environment variable that sets or would lock this value."`
	Doc       string          `json:"doc"`
}

// adminDefs returns the settings of the "This server" section: desktop-only settings appear
// only when the desktop app runs the server.
func (a *App) adminDefs() []settings.Def {
	var out []settings.Def
	for _, d := range settings.Defs() {
		if d.Scope == "instance" && d.Admin && (!d.Desktop || a.Cfg.HostSettings) {
			out = append(out, d)
		}
	}
	return out
}

func (a *App) adminDef(key string) (settings.Def, bool) {
	for _, d := range a.adminDefs() {
		if d.Key == key {
			return d, true
		}
	}
	return settings.Def{}, false
}

// instanceDefault is the value that applies when a setting holds nothing: for the image,
// the image of this build.
func (a *App) instanceDefault(d settings.Def) any {
	if d.Key == "computers.image" {
		return BuiltinImage(a.Cfg.ComputerImage)
	}
	return d.Default
}

func (a *App) instanceSettingView(ctx context.Context, d settings.Def) (InstanceSettingView, error) {
	def, _ := json.Marshal(a.instanceDefault(d))
	v := InstanceSettingView{Key: d.Key, Default: def, Secret: d.Secret, Doc: d.Doc}
	v.Env = d.Env
	name, locked := a.envLockOf(d)
	if locked {
		v.Locked, v.Env = true, name
	}
	if d.Secret {
		b, err := a.Vault.LookupInstanceSecret(ctx, secretName(d.Key))
		if err != nil {
			return v, err
		}
		v.Value, v.Set, v.IsDefault = json.RawMessage("null"), len(b) > 0, len(b) == 0
		return v, nil
	}
	if locked {
		// The view shows what applies: the value of the variable, not the stored one.
		v.Value, _ = json.Marshal(lockedValue(name))
		return v, nil
	}
	cur, err := a.Settings.Get(ctx, nil, d.Key)
	if err != nil {
		return v, err
	}
	v.Value, v.IsDefault = cur.Value, cur.IsDefault || string(cur.Value) == `""`
	if d.Key == "computers.image" {
		var s string
		if json.Unmarshal(cur.Value, &s) == nil && StaleRelease(a.Cfg.ComputerImage, s) {
			// A pin to another release does not apply, so the view shows the default.
			v.Value, v.IsDefault = json.RawMessage(`""`), true
		}
	}
	return v, nil
}

func (a *App) registerInstanceSettings(api huma.API) {
	requireAdmin := func(ctx context.Context) (*auth.Principal, error) {
		p := auth.From(ctx)
		if p == nil || !p.IsAdmin {
			return nil, huma.Error403Forbidden("Only an instance admin can do this.")
		}
		return p, nil
	}
	type listOut struct {
		Body struct {
			Settings []InstanceSettingView `json:"settings"`
		}
	}
	huma.Register(api, op("list-instance-settings", http.MethodGet, "/admin/settings", "The settings of this server: computer image, certificates and proxy (instance admins)", "admin"), func(ctx context.Context, _ *struct{}) (*listOut, error) {
		if _, err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		out := &listOut{}
		out.Body.Settings = []InstanceSettingView{}
		for _, d := range a.adminDefs() {
			v, err := a.instanceSettingView(ctx, d)
			if err != nil {
				return nil, err
			}
			out.Body.Settings = append(out.Body.Settings, v)
		}
		return out, nil
	})

	huma.Register(api, op("set-instance-setting", http.MethodPut, "/admin/settings/{key}", "Change a setting of this server. An empty text resets it; a secret is write-only (instance admins)", "admin"), func(ctx context.Context, in *struct {
		Key  string `path:"key"`
		Body struct {
			Value any `json:"value"`
		}
	}) (*struct{ Body InstanceSettingView }, error) {
		p, err := requireAdmin(ctx)
		if err != nil {
			return nil, err
		}
		d, ok := a.adminDef(in.Key)
		if !ok {
			return nil, huma.Error404NotFound("This server has no setting " + in.Key + ".")
		}
		if name, locked := a.envLockOf(d); locked {
			return nil, huma.Error409Conflict("The environment variable " + name + " sets this value. Change the variable, or remove it and restart Fettle.")
		}
		if err := checkInstanceSetting(d.Key, in.Body.Value); err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		if s, ok := in.Body.Value.(string); ok && d.Key == "computers.image" && StaleRelease(a.Cfg.ComputerImage, s) {
			return nil, huma.Error422UnprocessableEntity("This is the image of another Fettle release. This version uses " + a.Cfg.ComputerImage + ". Leave the field empty to use it.")
		}
		if d.Secret {
			s, _ := in.Body.Value.(string)
			state := "set"
			if s == "" {
				state = "cleared"
				err = a.Vault.DeleteInstanceSecret(ctx, secretName(d.Key))
			} else {
				err = a.Vault.SetInstanceSecret(ctx, secretName(d.Key), []byte(s))
				a.Vault.Redactor.RegisterGlobal(d.Key, s)
			}
			if err != nil {
				return nil, err
			}
			if err := a.Audit.Record(ctx, actorOf(p), nil, "settings.change", "setting:"+d.Key, map[string]string{"secret": state}); err != nil {
				return nil, err
			}
		} else {
			value := in.Body.Value
			if s, ok := value.(string); ok {
				value = strings.TrimSpace(s)
			}
			if _, err := a.Settings.Set(ctx, actorOf(p), nil, d.Key, value); err != nil {
				return nil, huma.Error422UnprocessableEntity(err.Error())
			}
		}
		a.afterInstanceSetting(ctx, d.Key)
		v, err := a.instanceSettingView(ctx, d)
		if err != nil {
			return nil, err
		}
		return &struct{ Body InstanceSettingView }{Body: v}, nil
	})
}

// afterInstanceSetting applies a changed setting without a restart: the image to the next
// computer, the roots and proxy to the server HTTP client and the next computer.
func (a *App) afterInstanceSetting(ctx context.Context, key string) {
	switch key {
	case "computers.image":
		a.applyImage(ctx)
	default:
		if err := a.applyNetwork(ctx); err != nil {
			a.Log.Warn("network settings", "error", err)
		}
	}
}
