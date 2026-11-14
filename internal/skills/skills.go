// Package skills keeps the skill library of a space (§13.1): SKILL.md folders with
// versions, zip import and export, bot assignments, the skill index, and Skael sync.
package skills

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/alternayte/drel"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/objects"
	"github.com/alternayte/fettle/internal/vault"
)

// Errors of the skills service.
var (
	ErrNotFound = errors.New("skill not found")
	ErrNoMD     = errors.New("the skill has no SKILL.md")
	ErrBadMD    = errors.New("SKILL.md needs frontmatter with a name and a description")
	ErrTooLarge = errors.New("the skill is larger than 20 MiB")
	ErrSkael    = errors.New("Skael manages this skill; change it in Skael and sync")
)

// MaxBytes is the largest skill folder that Fettle stores.
const MaxBytes = 20 << 20

// Service manages the library.
type Service struct {
	e       *drel.Engine
	audit   *audit.Log
	vault   *vault.Vault
	objects objects.Store
}

// New returns the skills service.
func New(e *drel.Engine, a *audit.Log, v *vault.Vault, o objects.Store) *Service {
	return &Service{e: e, audit: a, vault: v, objects: o}
}

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Front is the frontmatter of SKILL.md.
type Front struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// ParseMD reads the frontmatter of SKILL.md and returns it with the body.
func ParseMD(md string) (Front, string, error) {
	md = strings.TrimPrefix(md, "\ufeff")
	if !strings.HasPrefix(md, "---") {
		return Front{}, "", ErrBadMD
	}
	rest := strings.TrimPrefix(md[3:], "\r")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return Front{}, "", ErrBadMD
	}
	var f Front
	if err := yaml.Unmarshal([]byte(rest[:end]), &f); err != nil {
		return Front{}, "", fmt.Errorf("%w: %w", ErrBadMD, err)
	}
	f.Name, f.Description = strings.TrimSpace(f.Name), strings.TrimSpace(f.Description)
	if !validName.MatchString(f.Name) || f.Description == "" {
		return Front{}, "", ErrBadMD
	}
	body := strings.TrimLeft(rest[end+4:], "\r\n")
	return f, body, nil
}

// Files is a skill folder: slash paths relative to the folder, and their bytes.
type Files map[string][]byte

// clean drops a single top folder (as in a zip of the folder) and refuses paths that leave
// the folder.
func (f Files) clean() (Files, error) {
	tops := map[string]bool{}
	for p := range f {
		tops[strings.SplitN(p, "/", 2)[0]] = true
	}
	strip := ""
	if _, ok := f["SKILL.md"]; !ok && len(tops) == 1 {
		for t := range tops {
			strip = t + "/"
		}
	}
	out := Files{}
	total := 0
	for p, b := range f {
		p = strings.TrimPrefix(p, strip)
		c := path.Clean("/" + p)[1:]
		if c == "" || c != p || strings.HasPrefix(c, "../") {
			return nil, fmt.Errorf("the path %q leaves the skill folder", p)
		}
		total += len(b)
		out[c] = b
	}
	if total > MaxBytes {
		return nil, ErrTooLarge
	}
	if _, ok := out["SKILL.md"]; !ok {
		return nil, ErrNoMD
	}
	return out, nil
}

// TarGz packs files deterministically.
func (f Files) TarGz() ([]byte, string) {
	names := make([]string, 0, len(f))
	for n := range f {
		names = append(names, n)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	h := sha256.New()
	for _, n := range names {
		_ = tw.WriteHeader(&tar.Header{Name: n, Mode: 0o644, Size: int64(len(f[n])), Format: tar.FormatPAX})
		_, _ = tw.Write(f[n])
		h.Write([]byte(n))
		h.Write([]byte{0})
		h.Write(f[n])
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes(), hex.EncodeToString(h.Sum(nil))
}

// FromTarGz reads a tar.gz skill archive.
func FromTarGz(b []byte) (Files, error) {
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	out := Files{}
	total := 0
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, MaxBytes+1))
		if err != nil {
			return nil, err
		}
		total += len(data)
		if total > MaxBytes {
			return nil, ErrTooLarge
		}
		out[strings.TrimPrefix(hdr.Name, "./")] = data
	}
}

// FromZip reads a zip of a skill folder.
func FromZip(b []byte) (Files, error) {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, fmt.Errorf("the file is not a zip: %w", err)
	}
	out := Files{}
	total := uint64(0)
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		total += f.UncompressedSize64
		if total > MaxBytes {
			return nil, ErrTooLarge
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(rc, MaxBytes+1))
		_ = rc.Close()
		if err != nil {
			return nil, err
		}
		out[f.Name] = data
	}
	return out, nil
}

// Zip packs a skill folder under its name.
func (f Files) Zip(name string) []byte {
	names := make([]string, 0, len(f))
	for n := range f {
		names = append(names, n)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		w, _ := zw.Create(name + "/" + n)
		_, _ = w.Write(f[n])
	}
	_ = zw.Close()
	return buf.Bytes()
}

func objectKey(skillID uuid.UUID, version int) string {
	return fmt.Sprintf("skills/%s/%d", skillID, version)
}

// Store saves a skill folder as a new version, or returns the current version when the
// content did not change. source is local or skael; upstream is the Skael version.
func (s *Service) Store(ctx context.Context, actor audit.Actor, spaceID uuid.UUID, raw Files, source string, upstream int) (*Skill, *SkillVersion, error) {
	files, err := raw.clean()
	if err != nil {
		return nil, nil, err
	}
	front, _, err := ParseMD(string(files["SKILL.md"]))
	if err != nil {
		return nil, nil, err
	}
	archive, sum := files.TarGz()
	sk, err := drel.NewRepository(s.e, SkillMeta).Where(Skills.SpaceID.Eq(spaceID)).Where(Skills.Name.Eq(front.Name)).FirstOrNil(ctx)
	if err != nil {
		return nil, nil, err
	}
	if sk != nil && sk.Source == "skael" && source != "skael" {
		return nil, nil, ErrSkael
	}
	if sk != nil {
		cur, err := s.Version(ctx, sk, sk.CurrentVersion)
		if err == nil && cur.SHA256 == sum {
			return sk, cur, nil
		}
	} else {
		sk = &Skill{SpaceID: spaceID, Name: front.Name, Source: source}
		sk.SetID(uuid.Must(uuid.NewV7()))
	}
	version := sk.CurrentVersion + 1
	sealed, err := s.vault.Seal(ctx, spaceID, archive, objectKey(sk.ID(), version))
	if err != nil {
		return nil, nil, err
	}
	if err := s.objects.Put(ctx, objectKey(sk.ID(), version), bytes.NewReader(sealed), int64(len(sealed))); err != nil {
		return nil, nil, err
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	_, evals := files["evals.json"]
	v := &SkillVersion{SkillID: sk.ID(), Version: version, SHA256: sum, ObjectKey: objectKey(sk.ID(), version), SkillMD: string(files["SKILL.md"]), Files: names, HasEvals: evals, Upstream: upstream, CreatedBy: actor.Kind + ":" + actor.ID}
	err = s.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		if sk.CurrentVersion == 0 {
			sk.Description, sk.CurrentVersion = front.Description, version
			drel.Repo(tx, SkillMeta).Add(sk)
		} else if _, err := tx.Exec(ctx, "UPDATE skills SET current_version = $1, description = $2, source = $3, updated_at = CURRENT_TIMESTAMP WHERE id = $4", version, front.Description, source, sk.ID()); err != nil {
			return err
		}
		drel.Repo(tx, SkillVersionMeta).Add(v)
		return s.audit.Record(ctx, actor, &spaceID, "skill.version", "skill:"+sk.ID().String(), map[string]string{"name": sk.Name, "version": fmt.Sprint(version), "source": source})
	})
	if err != nil {
		return nil, nil, err
	}
	sk.Description, sk.CurrentVersion, sk.Source = front.Description, version, source
	return sk, v, nil
}

// StoreMD stores a new SKILL.md text. When the skill exists, the new version keeps the
// other files of the current version.
func (s *Service) StoreMD(ctx context.Context, actor audit.Actor, spaceID uuid.UUID, md string) (*Skill, *SkillVersion, error) {
	front, _, err := ParseMD(md)
	if err != nil {
		return nil, nil, err
	}
	files := Files{}
	if sk, err := s.ByName(ctx, spaceID, front.Name); err == nil {
		v, err := s.Version(ctx, sk, sk.CurrentVersion)
		if err != nil {
			return nil, nil, err
		}
		if files, err = s.Files(ctx, sk, v); err != nil {
			return nil, nil, err
		}
	}
	files["SKILL.md"] = []byte(md)
	return s.Store(ctx, actor, spaceID, files, "local", 0)
}

// Get returns a skill of a space.
func (s *Service) Get(ctx context.Context, spaceID, id uuid.UUID) (*Skill, error) {
	sk, err := drel.NewRepository(s.e, SkillMeta).Where(Skills.ID.Eq(id)).Where(Skills.SpaceID.Eq(spaceID)).FirstOrNil(ctx)
	if err != nil {
		return nil, err
	}
	if sk == nil {
		return nil, ErrNotFound
	}
	return sk, nil
}

// ByName returns a skill of a space by name.
func (s *Service) ByName(ctx context.Context, spaceID uuid.UUID, name string) (*Skill, error) {
	sk, err := drel.NewRepository(s.e, SkillMeta).Where(Skills.SpaceID.Eq(spaceID)).Where(Skills.Name.Eq(strings.ToLower(strings.TrimSpace(name)))).FirstOrNil(ctx)
	if err != nil {
		return nil, err
	}
	if sk == nil {
		return nil, ErrNotFound
	}
	return sk, nil
}

// List returns the skills of a space.
func (s *Service) List(ctx context.Context, spaceID uuid.UUID) ([]*Skill, error) {
	return drel.NewRepository(s.e, SkillMeta).Where(Skills.SpaceID.Eq(spaceID)).OrderBy(Skills.Name.Asc()).All(ctx)
}

// Versions returns the versions of a skill, newest first.
func (s *Service) Versions(ctx context.Context, skillID uuid.UUID) ([]*SkillVersion, error) {
	return drel.NewRepository(s.e, SkillVersionMeta).Where(SkillVersions.SkillID.Eq(skillID)).OrderBy(SkillVersions.Version.Desc()).All(ctx)
}

// Version returns one version of a skill.
func (s *Service) Version(ctx context.Context, sk *Skill, version int) (*SkillVersion, error) {
	v, err := drel.NewRepository(s.e, SkillVersionMeta).Where(SkillVersions.SkillID.Eq(sk.ID())).Where(SkillVersions.Version.Eq(version)).FirstOrNil(ctx)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, ErrNotFound
	}
	return v, nil
}

// Files returns the folder of a version.
func (s *Service) Files(ctx context.Context, sk *Skill, v *SkillVersion) (Files, error) {
	rc, err := s.objects.Get(ctx, v.ObjectKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	sealed, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	archive, err := s.vault.Open(ctx, sk.SpaceID, sealed, v.ObjectKey)
	if err != nil {
		return nil, err
	}
	return FromTarGz(archive)
}

// Delete removes a skill and its archives.
func (s *Service) Delete(ctx context.Context, actor audit.Actor, spaceID, id uuid.UUID) error {
	sk, err := s.Get(ctx, spaceID, id)
	if err != nil {
		return err
	}
	vs, _ := s.Versions(ctx, sk.ID())
	err = s.e.WithTx(ctx, func(ctx context.Context) error {
		if _, err := drel.MustFromContext(ctx).Exec(ctx, "DELETE FROM skills WHERE id = $1", id); err != nil {
			return err
		}
		return s.audit.Record(ctx, actor, &spaceID, "skill.delete", "skill:"+id.String(), map[string]string{"name": sk.Name})
	})
	if err == nil {
		for _, v := range vs {
			_ = s.objects.Delete(ctx, v.ObjectKey)
		}
	}
	return err
}

// Assign gives a bot a skill, or takes it away.
func (s *Service) Assign(ctx context.Context, actor audit.Actor, spaceID, botID, skillID uuid.UUID, on bool) error {
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		if _, err := tx.Exec(ctx, "DELETE FROM bot_skills WHERE bot_id = $1 AND skill_id = $2", botID, skillID); err != nil {
			return err
		}
		if on {
			drel.Repo(tx, BotSkillMeta).Add(&BotSkill{BotID: botID, SkillID: skillID})
		}
		return s.audit.Record(ctx, actor, &spaceID, "skill.assign", "bot:"+botID.String(), map[string]string{"skill": skillID.String(), "on": fmt.Sprint(on)})
	})
}

// ForBot returns the skills of a bot.
func (s *Service) ForBot(ctx context.Context, spaceID, botID uuid.UUID) ([]*Skill, error) {
	rows, err := s.e.Query(ctx, "SELECT skill_id FROM bot_skills WHERE bot_id = $1", botID)
	if err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if len(ids) == 0 {
		return nil, nil
	}
	return drel.NewRepository(s.e, SkillMeta).Where(Skills.SpaceID.Eq(spaceID)).Where(Skills.ID.In(ids...)).OrderBy(Skills.Name.Asc()).All(ctx)
}

// Loaded is a skill that a run loads: its current SKILL.md and its other files.
type Loaded struct {
	Skill   *Skill
	Version *SkillVersion
	Files   Files
}

// Load returns the current version of a bot's skill by name.
func (s *Service) Load(ctx context.Context, spaceID, botID uuid.UUID, name string) (*Loaded, error) {
	list, err := s.ForBot(ctx, spaceID, botID)
	if err != nil {
		return nil, err
	}
	for _, sk := range list {
		if sk.Name == strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "/")) {
			v, err := s.Version(ctx, sk, sk.CurrentVersion)
			if err != nil {
				return nil, err
			}
			files, err := s.Files(ctx, sk, v)
			if err != nil {
				return nil, err
			}
			return &Loaded{Skill: sk, Version: v, Files: files}, nil
		}
	}
	return nil, ErrNotFound
}
