package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/auth"
	"github.com/alternayte/fettle/internal/spaces"
)

// importLimit caps an archive upload. ImportSpace reads the archive into memory, so a larger
// space goes through fettle import on the host.
const importLimit = 2 << 30

// spaceExport streams the portable archive of a space to an owner (REQ-CORE-011). The body
// is a form or JSON with an optional passphrase; with one, the vault goes in, sealed with it.
// A plain route, so a browser form downloads the archive without holding it in memory.
func (a *App) spaceExport(w http.ResponseWriter, r *http.Request) {
	p := auth.From(r.Context())
	if p == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "Sign in first."})
		return
	}
	spaceID, err := uuid.Parse(chi.URLParam(r, "space"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "space not found"})
		return
	}
	role, err := a.Spaces.Role(r.Context(), spaceID, p.UserID)
	if errors.Is(err, spaces.ErrNotMember) {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "space not found"})
		return
	}
	if err != nil || role != spaces.RoleOwner {
		writeJSON(w, http.StatusForbidden, map[string]string{"detail": "Only an owner of this space can export it."})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	pass := ""
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var in struct {
			Passphrase string `json:"passphrase"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"detail": "The body is not valid JSON."})
			return
		}
		pass = in.Passphrase
	} else {
		pass = r.PostFormValue("passphrase")
	}
	sp, err := a.Spaces.Get(r.Context(), spaceID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "space not found"})
		return
	}
	name := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(sp.Name), "-"), "-")
	if name == "" {
		name = "space"
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.fettle.tar.gz"`, name))
	w.Header().Set("Cache-Control", "no-store")
	if err := a.ExportSpace(r.Context(), spaceID, w, pass); err != nil {
		a.Log.Warn("space export", "space", spaceID, "error", err)
		// The archive is cut short: end the connection, so the download fails instead of
		// leaving a file that looks complete.
		panic(http.ErrAbortHandler)
	}
	_ = a.Audit.Record(r.Context(), actorOf(p), &spaceID, "space.export", "space:"+spaceID.String(), map[string]string{"vault": boolText(pass != "")})
}

// spaceImport reads an uploaded space archive into a new space of this instance. The person
// who uploads it owns the space. Like creating a space, it is for instance admins (in local
// mode, the owner of the computer). The multipart form has the file and an optional
// passphrase that opens the vault of the archive.
func (a *App) spaceImport(w http.ResponseWriter, r *http.Request) {
	p := auth.From(r.Context())
	if p == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "Sign in first."})
		return
	}
	if !p.IsAdmin {
		writeJSON(w, http.StatusForbidden, map[string]string{"detail": "Only an instance admin can import a space."})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, importLimit)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"detail": "Send the archive as the field file of a form, at most 2 GiB. Use fettle import on the host for a larger space."})
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	f, _, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"detail": "The form has no file."})
		return
	}
	defer func() { _ = f.Close() }()
	res, err := a.ImportSpace(r.Context(), f, p.UserID, r.FormValue("passphrase"))
	switch {
	case errors.Is(err, ErrSpaceExists):
		writeJSON(w, http.StatusConflict, map[string]string{"detail": "This instance already has the space of this archive."})
		return
	case errors.Is(err, ErrPassphrase):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"detail": "The passphrase does not open the vault of this archive."})
		return
	case err != nil:
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"detail": err.Error()})
		return
	}
	if sid, err := uuid.Parse(res.SpaceID); err == nil {
		_ = a.Audit.Record(r.Context(), actorOf(p), &sid, "space.import", "space:"+res.SpaceID, map[string]string{"vault": fmt.Sprint(res.Vault)})
	}
	writeJSON(w, http.StatusCreated, res)
}
