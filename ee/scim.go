//go:build ee

package ee

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alternayte/fettle/internal/app"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/ee/licensekey"
	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/auth"
	"github.com/alternayte/fettle/internal/settings"
	"github.com/alternayte/fettle/internal/spaces"
)

func init() {
	settings.Register(settings.Def{Key: "ee.scim_token_hash", Scope: "instance", Default: "", Doc: "The SHA-256 of the SCIM bearer token (set by Fettle)."})
	settings.Register(settings.Def{Key: "ee.scim_groups", Scope: "instance", Default: "", Doc: "The SCIM groups and their members as JSON (set by Fettle)."})
	settings.Register(settings.Def{Key: "ee.group_roles", Scope: "instance", Default: "", Doc: "Group-to-role mapping as JSON: a list of {group, space, role}."})
	settings.Register(settings.Def{Key: "ee.scim_managed", Scope: "instance", Default: "", Doc: "The space memberships that the group mapping made, as JSON (set by Fettle)."})
}

const (
	scimUser  = "urn:ietf:params:scim:schemas:core:2.0:User"
	scimGroup = "urn:ietf:params:scim:schemas:core:2.0:Group"
	scimList  = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	scimPatch = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
	scimError = "urn:ietf:params:scim:api:messages:2.0:Error"
)

// GroupRole maps a group to a role in a space (§18.3).
type GroupRole struct {
	Group string `json:"group"`
	Space string `json:"space"`
	Role  string `json:"role" enum:"owner,member"`
}

type scimGroupRow struct {
	ID          string   `json:"id"`
	DisplayName string   `json:"displayName"`
	Members     []string `json:"members"`
}

var scimMu sync.Mutex

func scimErr(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/scim+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"schemas": []string{scimError}, "status": strconv.Itoa(status), "detail": detail})
}

func scimJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/scim+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// scimAuth checks the SCIM bearer token and the licence.
func (x *Enterprise) scimAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		want := x.a.Settings.String(ctx, nil, "ee.scim_token_hash")
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		h := sha256.Sum256([]byte(tok))
		if want == "" || !ok || subtle.ConstantTimeCompare([]byte(hex.EncodeToString(h[:])), []byte(want)) != 1 {
			scimErr(w, http.StatusUnauthorized, "The SCIM token is missing or wrong.")
			return
		}
		write := r.Method != http.MethodGet
		if !x.Has(ctx, licensekey.FeatureSCIM, write) {
			scimErr(w, http.StatusForbidden, ErrFeature.Error())
			return
		}
		if x.a.SignIn == nil {
			scimErr(w, http.StatusConflict, "SCIM needs server mode.")
			return
		}
		next(w, r)
	}
}

func (x *Enterprise) publicSCIM(r chi.Router) {
	r.Route("/scim/v2", func(s chi.Router) {
		s.Get("/Users", x.scimAuth(x.scimListUsers))
		s.Post("/Users", x.scimAuth(x.scimCreateUser))
		s.Get("/Users/{id}", x.scimAuth(x.scimGetUser))
		s.Put("/Users/{id}", x.scimAuth(x.scimReplaceUser))
		s.Patch("/Users/{id}", x.scimAuth(x.scimPatchUser))
		s.Delete("/Users/{id}", x.scimAuth(x.scimDeleteUser))
		s.Get("/Groups", x.scimAuth(x.scimListGroups))
		s.Post("/Groups", x.scimAuth(x.scimCreateGroup))
		s.Get("/Groups/{id}", x.scimAuth(x.scimGetGroup))
		s.Put("/Groups/{id}", x.scimAuth(x.scimReplaceGroup))
		s.Patch("/Groups/{id}", x.scimAuth(x.scimPatchGroup))
		s.Delete("/Groups/{id}", x.scimAuth(x.scimDeleteGroup))
	})
}

// --- users

type scimUserIn struct {
	UserName    string `json:"userName"`
	DisplayName string `json:"displayName"`
	Name        struct {
		Formatted  string `json:"formatted"`
		GivenName  string `json:"givenName"`
		FamilyName string `json:"familyName"`
	} `json:"name"`
	Emails []struct {
		Value   string `json:"value"`
		Primary bool   `json:"primary"`
	} `json:"emails"`
	Active *bool `json:"active"`
}

func (in scimUserIn) email() string {
	for _, e := range in.Emails {
		if e.Primary {
			return e.Value
		}
	}
	if len(in.Emails) > 0 {
		return in.Emails[0].Value
	}
	return in.UserName
}

func (in scimUserIn) name() string {
	switch {
	case in.DisplayName != "":
		return in.DisplayName
	case in.Name.Formatted != "":
		return in.Name.Formatted
	}
	return strings.TrimSpace(in.Name.GivenName + " " + in.Name.FamilyName)
}

func (x *Enterprise) userResource(u *auth.User) map[string]any {
	return map[string]any{
		"schemas":     []string{scimUser},
		"id":          u.ID().String(),
		"userName":    u.Email,
		"displayName": u.Name,
		"name":        map[string]string{"formatted": u.Name},
		"emails":      []map[string]any{{"value": u.Email, "primary": true}},
		"active":      u.Status == "active",
		"meta":        map[string]string{"resourceType": "User", "created": u.CreatedAt().UTC().Format(time.RFC3339), "lastModified": u.UpdatedAt().UTC().Format(time.RFC3339), "location": "/scim/v2/Users/" + u.ID().String()},
	}
}

func (x *Enterprise) scimUser(r *http.Request) (*auth.User, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return nil, errors.New("not found")
	}
	u, err := x.a.SignIn.Users().Get(r.Context(), id)
	if err != nil || u == nil {
		return nil, errors.New("not found")
	}
	return u, nil
}

func (x *Enterprise) scimCreateUser(w http.ResponseWriter, r *http.Request) {
	var in scimUserIn
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil || !strings.Contains(in.email(), "@") {
		scimErr(w, http.StatusBadRequest, "userName or emails must hold an email address.")
		return
	}
	ctx := r.Context()
	if existing, _ := x.a.SignIn.Users().ByEmail(ctx, in.email()); existing != nil {
		scimErr(w, http.StatusConflict, "A user with this userName exists.")
		return
	}
	u, err := x.a.SignIn.Provision(ctx, in.email(), in.name())
	if err != nil {
		scimErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.Active != nil && !*in.Active {
		if err := x.a.SignIn.SetActive(ctx, u, false); err != nil {
			scimErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	_ = x.a.Audit.Record(ctx, audit.Actor{Kind: "system", ID: "scim"}, nil, "scim.user.create", "user:"+u.ID().String(), nil)
	u, _ = x.a.SignIn.Users().Get(ctx, u.ID())
	scimJSON(w, http.StatusCreated, x.userResource(u))
}

func (x *Enterprise) scimGetUser(w http.ResponseWriter, r *http.Request) {
	u, err := x.scimUser(r)
	if err != nil {
		scimErr(w, http.StatusNotFound, "No such user.")
		return
	}
	scimJSON(w, http.StatusOK, x.userResource(u))
}

func (x *Enterprise) scimListUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	filter := r.URL.Query().Get("filter")
	var list []*auth.User
	if attr, val, ok := parseEq(filter); ok && strings.EqualFold(attr, "userName") {
		if u, _ := x.a.SignIn.Users().ByEmail(ctx, val); u != nil {
			list = append(list, u)
		}
	} else if filter == "" {
		rows, err := x.a.DB.Engine.Query(ctx, "SELECT id FROM users ORDER BY email LIMIT 1000")
		if err != nil {
			scimErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		var ids []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		for _, id := range ids {
			if u, _ := x.a.SignIn.Users().Get(ctx, id); u != nil {
				list = append(list, u)
			}
		}
	} else {
		scimErr(w, http.StatusBadRequest, "Fettle filters users by userName eq only.")
		return
	}
	res := []map[string]any{}
	for _, u := range list {
		res = append(res, x.userResource(u))
	}
	scimJSON(w, http.StatusOK, map[string]any{"schemas": []string{scimList}, "totalResults": len(res), "startIndex": 1, "itemsPerPage": len(res), "Resources": res})
}

// parseEq reads a SCIM filter `attr eq "value"`.
func parseEq(f string) (string, string, bool) {
	parts := strings.SplitN(strings.TrimSpace(f), " ", 3)
	if len(parts) != 3 || !strings.EqualFold(parts[1], "eq") {
		return "", "", false
	}
	return parts[0], strings.Trim(parts[2], `"`), true
}

func (x *Enterprise) setActive(ctx context.Context, u *auth.User, active bool) error {
	if (u.Status == "active") == active {
		return nil
	}
	if err := x.a.SignIn.SetActive(ctx, u, active); err != nil {
		return err
	}
	action := "scim.user.activate"
	if !active {
		action = "scim.user.deactivate"
	}
	return x.a.Audit.Record(ctx, audit.Actor{Kind: "system", ID: "scim"}, nil, action, "user:"+u.ID().String(), nil)
}

func (x *Enterprise) scimReplaceUser(w http.ResponseWriter, r *http.Request) {
	u, err := x.scimUser(r)
	if err != nil {
		scimErr(w, http.StatusNotFound, "No such user.")
		return
	}
	var in scimUserIn
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		scimErr(w, http.StatusBadRequest, "The body is not a SCIM user.")
		return
	}
	ctx := r.Context()
	if n := in.name(); n != "" && n != u.Name {
		if err := x.a.SignIn.Users().SetName(ctx, u.ID(), n); err != nil {
			scimErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if in.Active != nil {
		if err := x.setActive(ctx, u, *in.Active); err != nil {
			scimErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	u, _ = x.a.SignIn.Users().Get(ctx, u.ID())
	scimJSON(w, http.StatusOK, x.userResource(u))
}

type scimPatchIn struct {
	Operations []struct {
		Op    string          `json:"op"`
		Path  string          `json:"path"`
		Value json.RawMessage `json:"value"`
	} `json:"Operations"`
}

func (x *Enterprise) scimPatchUser(w http.ResponseWriter, r *http.Request) {
	u, err := x.scimUser(r)
	if err != nil {
		scimErr(w, http.StatusNotFound, "No such user.")
		return
	}
	var in scimPatchIn
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		scimErr(w, http.StatusBadRequest, "The body is not a SCIM patch.")
		return
	}
	ctx := r.Context()
	for _, op := range in.Operations {
		if !strings.EqualFold(op.Op, "replace") && !strings.EqualFold(op.Op, "add") {
			continue
		}
		fields := map[string]json.RawMessage{}
		if op.Path != "" {
			fields[op.Path] = op.Value
		} else {
			_ = json.Unmarshal(op.Value, &fields)
		}
		for k, v := range fields {
			switch strings.ToLower(k) {
			case "active":
				var b bool
				if json.Unmarshal(v, &b) != nil {
					var s string
					_ = json.Unmarshal(v, &s)
					b = strings.EqualFold(s, "true")
				}
				if err := x.setActive(ctx, u, b); err != nil {
					scimErr(w, http.StatusInternalServerError, err.Error())
					return
				}
			case "displayname", "name.formatted":
				var s string
				if json.Unmarshal(v, &s) == nil && s != "" {
					_ = x.a.SignIn.Users().SetName(ctx, u.ID(), s)
				}
			}
		}
	}
	u, _ = x.a.SignIn.Users().Get(ctx, u.ID())
	scimJSON(w, http.StatusOK, x.userResource(u))
}

// scimDeleteUser deactivates the user: the account, its audit history and its work stay.
func (x *Enterprise) scimDeleteUser(w http.ResponseWriter, r *http.Request) {
	u, err := x.scimUser(r)
	if err != nil {
		scimErr(w, http.StatusNotFound, "No such user.")
		return
	}
	if err := x.setActive(r.Context(), u, false); err != nil {
		scimErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- groups

func (x *Enterprise) groups(ctx context.Context) []scimGroupRow {
	var g []scimGroupRow
	_ = json.Unmarshal([]byte(x.a.Settings.String(ctx, nil, "ee.scim_groups")), &g)
	return g
}

func (x *Enterprise) saveGroups(ctx context.Context, g []scimGroupRow) error {
	if g == nil {
		g = []scimGroupRow{}
	}
	b, _ := json.Marshal(g)
	_, err := x.a.Settings.Set(ctx, audit.Actor{Kind: "system", ID: "scim"}, nil, "ee.scim_groups", string(b))
	return err
}

func groupResource(g scimGroupRow) map[string]any {
	members := []map[string]string{}
	for _, m := range g.Members {
		members = append(members, map[string]string{"value": m})
	}
	return map[string]any{"schemas": []string{scimGroup}, "id": g.ID, "displayName": g.DisplayName, "members": members, "meta": map[string]string{"resourceType": "Group", "location": "/scim/v2/Groups/" + g.ID}}
}

type scimGroupIn struct {
	DisplayName string `json:"displayName"`
	Members     []struct {
		Value string `json:"value"`
	} `json:"members"`
}

func (in scimGroupIn) memberIDs() []string {
	var out []string
	for _, m := range in.Members {
		if _, err := uuid.Parse(m.Value); err == nil && !slices.Contains(out, m.Value) {
			out = append(out, m.Value)
		}
	}
	return out
}

func (x *Enterprise) scimCreateGroup(w http.ResponseWriter, r *http.Request) {
	var in scimGroupIn
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil || strings.TrimSpace(in.DisplayName) == "" {
		scimErr(w, http.StatusBadRequest, "A group needs a displayName.")
		return
	}
	scimMu.Lock()
	defer scimMu.Unlock()
	ctx := r.Context()
	gs := x.groups(ctx)
	for _, g := range gs {
		if strings.EqualFold(g.DisplayName, in.DisplayName) {
			scimErr(w, http.StatusConflict, "A group with this displayName exists.")
			return
		}
	}
	g := scimGroupRow{ID: uuid.NewString(), DisplayName: in.DisplayName, Members: in.memberIDs()}
	gs = append(gs, g)
	if err := x.saveGroups(ctx, gs); err != nil {
		scimErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := x.applyGroupRoles(ctx); err != nil {
		scimErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	scimJSON(w, http.StatusCreated, groupResource(g))
}

func (x *Enterprise) findGroup(ctx context.Context, id string) (int, []scimGroupRow) {
	gs := x.groups(ctx)
	for i, g := range gs {
		if g.ID == id {
			return i, gs
		}
	}
	return -1, gs
}

func (x *Enterprise) scimGetGroup(w http.ResponseWriter, r *http.Request) {
	i, gs := x.findGroup(r.Context(), chi.URLParam(r, "id"))
	if i < 0 {
		scimErr(w, http.StatusNotFound, "No such group.")
		return
	}
	scimJSON(w, http.StatusOK, groupResource(gs[i]))
}

func (x *Enterprise) scimListGroups(w http.ResponseWriter, r *http.Request) {
	res := []map[string]any{}
	attr, val, filtered := parseEq(r.URL.Query().Get("filter"))
	for _, g := range x.groups(r.Context()) {
		if filtered && (!strings.EqualFold(attr, "displayName") || !strings.EqualFold(g.DisplayName, val)) {
			continue
		}
		res = append(res, groupResource(g))
	}
	scimJSON(w, http.StatusOK, map[string]any{"schemas": []string{scimList}, "totalResults": len(res), "startIndex": 1, "itemsPerPage": len(res), "Resources": res})
}

func (x *Enterprise) scimReplaceGroup(w http.ResponseWriter, r *http.Request) {
	var in scimGroupIn
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		scimErr(w, http.StatusBadRequest, "The body is not a SCIM group.")
		return
	}
	scimMu.Lock()
	defer scimMu.Unlock()
	ctx := r.Context()
	i, gs := x.findGroup(ctx, chi.URLParam(r, "id"))
	if i < 0 {
		scimErr(w, http.StatusNotFound, "No such group.")
		return
	}
	if in.DisplayName != "" {
		gs[i].DisplayName = in.DisplayName
	}
	gs[i].Members = in.memberIDs()
	if err := x.saveGroups(ctx, gs); err == nil {
		err = x.applyGroupRoles(ctx)
		if err != nil {
			scimErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	scimJSON(w, http.StatusOK, groupResource(gs[i]))
}

func (x *Enterprise) scimPatchGroup(w http.ResponseWriter, r *http.Request) {
	var in scimPatchIn
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		scimErr(w, http.StatusBadRequest, "The body is not a SCIM patch.")
		return
	}
	scimMu.Lock()
	defer scimMu.Unlock()
	ctx := r.Context()
	i, gs := x.findGroup(ctx, chi.URLParam(r, "id"))
	if i < 0 {
		scimErr(w, http.StatusNotFound, "No such group.")
		return
	}
	for _, op := range in.Operations {
		var members []struct {
			Value string `json:"value"`
		}
		path := strings.ToLower(op.Path)
		switch {
		case strings.HasPrefix(path, "members[value eq"):
			// remove members[value eq "id"]
			_, val, _ := parseEq(strings.TrimSuffix(strings.TrimPrefix(op.Path, "members["), "]"))
			members = append(members, struct {
				Value string `json:"value"`
			}{val})
		case path == "members":
			_ = json.Unmarshal(op.Value, &members)
		case path == "displayname":
			var s string
			if json.Unmarshal(op.Value, &s) == nil && s != "" {
				gs[i].DisplayName = s
			}
			continue
		default:
			continue
		}
		for _, m := range members {
			if _, err := uuid.Parse(m.Value); err != nil {
				continue
			}
			switch strings.ToLower(op.Op) {
			case "add":
				if !slices.Contains(gs[i].Members, m.Value) {
					gs[i].Members = append(gs[i].Members, m.Value)
				}
			case "remove":
				gs[i].Members = slices.DeleteFunc(gs[i].Members, func(v string) bool { return v == m.Value })
			case "replace":
				gs[i].Members = []string{}
				for _, mm := range members {
					gs[i].Members = append(gs[i].Members, mm.Value)
				}
			}
		}
	}
	if err := x.saveGroups(ctx, gs); err != nil {
		scimErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := x.applyGroupRoles(ctx); err != nil {
		scimErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	scimJSON(w, http.StatusOK, groupResource(gs[i]))
}

func (x *Enterprise) scimDeleteGroup(w http.ResponseWriter, r *http.Request) {
	scimMu.Lock()
	defer scimMu.Unlock()
	ctx := r.Context()
	i, gs := x.findGroup(ctx, chi.URLParam(r, "id"))
	if i < 0 {
		scimErr(w, http.StatusNotFound, "No such group.")
		return
	}
	gs = append(gs[:i], gs[i+1:]...)
	if err := x.saveGroups(ctx, gs); err != nil {
		scimErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := x.applyGroupRoles(ctx); err != nil {
		scimErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// applyGroupRoles makes the memberships of the mapped spaces follow the groups. Each
// member of a mapped group gets the group's role (owner wins over member). A person whom
// the mapping added leaves the space when no mapped group of the space holds them any more.
// People whom an owner added by hand keep their membership.
func (x *Enterprise) applyGroupRoles(ctx context.Context) error {
	x.groupMu.Lock()
	defer x.groupMu.Unlock()
	return x.applyGroupRolesLocked(ctx)
}

// applyGroupRolesLocked is applyGroupRoles for a caller that holds groupMu. The groups are
// the SCIM groups and the identity provider groups of each person at their last single
// sign-on.
func (x *Enterprise) applyGroupRolesLocked(ctx context.Context) error {
	var maps []GroupRole
	_ = json.Unmarshal([]byte(x.a.Settings.String(ctx, nil, "ee.group_roles")), &maps)
	managed := map[string][]string{} // space -> users that the mapping added
	_ = json.Unmarshal([]byte(x.a.Settings.String(ctx, nil, "ee.scim_managed")), &managed)
	groups := map[string][]string{}
	for _, g := range x.groups(ctx) {
		groups[strings.ToLower(g.DisplayName)] = append(groups[strings.ToLower(g.DisplayName)], g.Members...)
	}
	for uid, names := range x.ssoGroups(ctx) {
		for _, n := range names {
			groups[strings.ToLower(n)] = append(groups[strings.ToLower(n)], uid)
		}
	}
	want := map[uuid.UUID]map[uuid.UUID]string{} // space -> user -> role
	for sp := range managed {
		if sid, err := uuid.Parse(sp); err == nil {
			want[sid] = map[uuid.UUID]string{}
		}
	}
	for _, m := range maps {
		sid, err := uuid.Parse(m.Space)
		if err != nil {
			continue
		}
		if want[sid] == nil {
			want[sid] = map[uuid.UUID]string{}
		}
		for _, uid := range groups[strings.ToLower(m.Group)] {
			id, err := uuid.Parse(uid)
			if err != nil {
				continue
			}
			if want[sid][id] != spaces.RoleOwner {
				want[sid][id] = m.Role
			}
		}
	}
	act := audit.Actor{Kind: "system", ID: "scim"}
	next := map[string][]string{}
	for sid, users := range want {
		members, err := x.a.Spaces.Members(ctx, sid)
		if err != nil {
			continue
		}
		have := map[uuid.UUID]string{}
		for _, m := range members {
			have[m.UserID] = m.Role
		}
		for uid, role := range users {
			switch cur, ok := have[uid]; {
			case !ok:
				if err := x.a.Spaces.AddMember(ctx, act, sid, uid, role); err != nil {
					return err
				}
			case cur != role:
				if err := x.a.Spaces.SetRole(ctx, act, sid, uid, role); err != nil && !errors.Is(err, spaces.ErrLastOwner) {
					return err
				}
			}
			next[sid.String()] = append(next[sid.String()], uid.String())
		}
		for _, u := range managed[sid.String()] {
			uid, err := uuid.Parse(u)
			if err != nil {
				continue
			}
			if _, keep := users[uid]; keep {
				continue
			}
			if _, ok := have[uid]; !ok {
				continue
			}
			if err := x.a.Spaces.RemoveMember(ctx, act, sid, uid); err != nil {
				if !errors.Is(err, spaces.ErrLastOwner) {
					return err
				}
				next[sid.String()] = append(next[sid.String()], u)
			}
		}
	}
	for k := range next {
		slices.Sort(next[k])
	}
	b, _ := json.Marshal(next)
	_, err := x.a.Settings.Set(ctx, act, nil, "ee.scim_managed", string(b))
	return err
}

func (x *Enterprise) scimRoutes(api huma.API) {
	huma.Register(api, huma.Operation{OperationID: "create-scim-token", Method: http.MethodPost, Path: app.APIPrefix + "/admin/scim/token", Summary: "Make a new SCIM bearer token; the old one stops working", Tags: []string{"enterprise"}}, func(ctx context.Context, _ *struct{}) (*struct {
		Body struct {
			Token string `json:"token"`
			URL   string `json:"url"`
		}
	}, error) {
		if err := adminOnly(ctx); err != nil {
			return nil, err
		}
		if err := x.writable(ctx, licensekey.FeatureSCIM); err != nil {
			return nil, err
		}
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		tok := "scim_" + base64.RawURLEncoding.EncodeToString(b)
		h := sha256.Sum256([]byte(tok))
		if _, err := x.a.Settings.Set(ctx, actor(ctx), nil, "ee.scim_token_hash", hex.EncodeToString(h[:])); err != nil {
			return nil, err
		}
		out := &struct {
			Body struct {
				Token string `json:"token"`
				URL   string `json:"url"`
			}
		}{}
		out.Body.Token, out.Body.URL = tok, strings.TrimRight(x.a.Cfg.BaseURL, "/")+"/scim/v2"
		return out, nil
	})
	huma.Register(api, huma.Operation{OperationID: "set-group-roles", Method: http.MethodPut, Path: app.APIPrefix + "/admin/group-roles", Summary: "Map SCIM and SSO groups to roles in spaces", Tags: []string{"enterprise"}}, func(ctx context.Context, in *struct {
		Body struct {
			Mappings []GroupRole `json:"mappings"`
		}
	}) (*struct{}, error) {
		if err := adminOnly(ctx); err != nil {
			return nil, err
		}
		if err := x.writable(ctx, licensekey.FeatureSCIM); err != nil {
			return nil, err
		}
		for _, m := range in.Body.Mappings {
			if _, err := uuid.Parse(m.Space); err != nil || m.Group == "" {
				return nil, huma.Error422UnprocessableEntity("each mapping needs a group and a space ID")
			}
		}
		b, _ := json.Marshal(in.Body.Mappings)
		if _, err := x.a.Settings.Set(ctx, actor(ctx), nil, "ee.group_roles", string(b)); err != nil {
			return nil, err
		}
		scimMu.Lock()
		defer scimMu.Unlock()
		return nil, x.applyGroupRoles(ctx)
	})
}
