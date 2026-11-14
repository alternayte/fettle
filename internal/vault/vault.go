package vault

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/alternayte/drel"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/audit"
)

// ErrNotFound means the vault item does not exist in this space.
var ErrNotFound = errors.New("vault item not found")

// Kinds of vault items.
const (
	KindLogin  = "login"
	KindSecret = "secret"
	KindTOTP   = "totp"
)

// Vault encrypts items with per-space data keys wrapped by the master key (D-14).
type Vault struct {
	e      *drel.Engine
	audit  *audit.Log
	master []byte

	mu   sync.Mutex
	keys map[uuid.UUID]spaceKey // latest unwrapped data key per space

	Redactor *Redactor
}

type spaceKey struct {
	version int
	key     []byte
}

// New returns a vault that uses master to wrap data keys.
func New(e *drel.Engine, a *audit.Log, master []byte) *Vault {
	v := &Vault{e: e, audit: a, master: master, keys: map[uuid.UUID]spaceKey{}}
	v.Redactor = newRedactor(v)
	return v
}

func dataKeyAAD(spaceID uuid.UUID, version int) string {
	return fmt.Sprintf("fettle:data-key:%s:%d", spaceID, version)
}

// dataKey returns the latest data key of a space and creates one when the space has none.
func (v *Vault) dataKey(ctx context.Context, spaceID uuid.UUID) (spaceKey, error) {
	v.mu.Lock()
	k, ok := v.keys[spaceID]
	v.mu.Unlock()
	if ok {
		return k, nil
	}
	var row *DataKey
	var err error
	// Inside a transaction, read on its connection: a second connection can deadlock a
	// full pool (the journal redacts and seals inside its transaction).
	if tx, ok := drel.FromContext(ctx); ok {
		row, err = drel.NewTxRepository(tx, DataKeyMeta).Where(DataKeys.SpaceID.Eq(spaceID)).OrderBy(DataKeys.KeyVersion.Desc()).FirstOrNil(ctx)
	} else {
		row, err = drel.NewRepository(v.e, DataKeyMeta).Where(DataKeys.SpaceID.Eq(spaceID)).OrderBy(DataKeys.KeyVersion.Desc()).FirstOrNil(ctx)
	}
	if err != nil {
		return spaceKey{}, err
	}
	if row == nil {
		raw := NewKey()
		wrapped, err := wrapKey(v.master, raw, dataKeyAAD(spaceID, 1))
		if err != nil {
			return spaceKey{}, err
		}
		err = v.e.WithTx(ctx, func(ctx context.Context) error {
			drel.Repo(drel.MustFromContext(ctx), DataKeyMeta).Add(&DataKey{SpaceID: spaceID, WrappedKey: wrapped, KeyVersion: 1})
			return nil
		})
		if errors.Is(err, drel.ErrUniqueViolation) {
			return v.dataKey(ctx, spaceID)
		}
		if err != nil {
			return spaceKey{}, err
		}
		k = spaceKey{version: 1, key: raw}
	} else {
		raw, err := unwrapKey(v.master, row.WrappedKey, dataKeyAAD(spaceID, row.KeyVersion))
		if err != nil {
			return spaceKey{}, fmt.Errorf("unwrap data key of space %s: %w", spaceID, err)
		}
		k = spaceKey{version: row.KeyVersion, key: raw}
	}
	v.mu.Lock()
	v.keys[spaceID] = k
	v.mu.Unlock()
	return k, nil
}

// Seal encrypts data for a space, bound to aad. The output carries the key version.
func (v *Vault) Seal(ctx context.Context, spaceID uuid.UUID, plain []byte, aad string) ([]byte, error) {
	k, err := v.dataKey(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	n, ct, err := seal(k.key, plain, []byte(aad))
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, 1+len(n)+len(ct))
	out = append(out, byte(k.version))
	out = append(out, n...)
	return append(out, ct...), nil
}

// Open decrypts the output of Seal.
func (v *Vault) Open(ctx context.Context, spaceID uuid.UUID, sealed []byte, aad string) ([]byte, error) {
	if len(sealed) < 13 {
		return nil, ErrDecrypt
	}
	k, err := v.dataKey(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	if int(sealed[0]) != k.version {
		return nil, fmt.Errorf("vault: sealed with key version %d, current is %d", sealed[0], k.version)
	}
	return open(k.key, sealed[1:13], sealed[13:], []byte(aad))
}

// secretPayload is the encrypted part of an item.
type secretPayload struct {
	Value string `json:"value"`
}

// Input creates or changes an item. Value is the password, the secret or the TOTP seed.
type Input struct {
	Name     string
	Kind     string
	Site     string
	Username string
	Value    string
}

func last4(s string) string {
	r := []rune(s)
	if len(r) <= 4 {
		return ""
	}
	return string(r[len(r)-4:])
}

func itemAAD(spaceID, itemID uuid.UUID) string {
	return fmt.Sprintf("fettle:vault-item:%s:%s", spaceID, itemID)
}

func (in Input) validate() error {
	switch in.Kind {
	case KindLogin, KindSecret, KindTOTP:
	default:
		return fmt.Errorf("kind must be login, secret or totp")
	}
	if strings.TrimSpace(in.Name) == "" {
		return errors.New("name is required")
	}
	if in.Value == "" {
		return errors.New("value is required")
	}
	if in.Kind == KindLogin && strings.TrimSpace(in.Site) == "" {
		return errors.New("a login needs a site")
	}
	return nil
}

// Create stores a new item.
func (v *Vault) Create(ctx context.Context, actor audit.Actor, spaceID uuid.UUID, in Input) (*VaultItem, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	k, err := v.dataKey(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	item := &VaultItem{SpaceID: spaceID, Name: strings.TrimSpace(in.Name), Kind: in.Kind, Site: in.Site, Username: in.Username, KeyVersion: k.version, Last4: last4(in.Value)}
	if u, err := uuid.Parse(actor.ID); err == nil && actor.Kind == "user" {
		item.UpdatedBy = &u
	}
	err = v.e.WithTx(ctx, func(ctx context.Context) error {
		repo := drel.Repo(drel.MustFromContext(ctx), VaultItemMeta)
		repo.Add(item) // stamps the UUIDv7 id
		if err := v.encryptInto(item, k, in.Value); err != nil {
			return err
		}
		return v.audit.Record(ctx, actor, &spaceID, "vault.create", "vault_item:"+item.ID().String(), map[string]string{"name": item.Name, "kind": item.Kind})
	})
	if err != nil {
		return nil, err
	}
	v.Redactor.invalidate(spaceID)
	return item, nil
}

func (v *Vault) encryptInto(item *VaultItem, k spaceKey, value string) error {
	plain, _ := json.Marshal(secretPayload{Value: value})
	n, ct, err := seal(k.key, plain, []byte(itemAAD(item.SpaceID, item.ID())))
	if err != nil {
		return err
	}
	item.Nonce, item.Ciphertext, item.KeyVersion = b64(n), b64(ct), k.version
	return nil
}

// Update changes the fields of an item. An empty Value keeps the stored value.
func (v *Vault) Update(ctx context.Context, actor audit.Actor, spaceID, id uuid.UUID, in Input) (*VaultItem, error) {
	var out *VaultItem
	err := v.e.WithTx(ctx, func(ctx context.Context) error {
		repo := drel.Repo(drel.MustFromContext(ctx), VaultItemMeta)
		item, err := repo.Where(VaultItems.ID.Eq(id)).Where(VaultItems.SpaceID.Eq(spaceID)).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		if item == nil {
			return ErrNotFound
		}
		if in.Name != "" {
			item.Name = strings.TrimSpace(in.Name)
		}
		item.Site, item.Username = in.Site, in.Username
		if in.Value != "" {
			k, err := v.dataKey(ctx, spaceID)
			if err != nil {
				return err
			}
			if err := v.encryptInto(item, k, in.Value); err != nil {
				return err
			}
			item.Last4 = last4(in.Value)
		}
		if u, err := uuid.Parse(actor.ID); err == nil && actor.Kind == "user" {
			item.UpdatedBy = &u
		}
		out = item
		return v.audit.Record(ctx, actor, &spaceID, "vault.update", "vault_item:"+id.String(), map[string]string{"name": item.Name, "value_changed": fmt.Sprint(in.Value != "")})
	})
	if err != nil {
		return nil, err
	}
	v.Redactor.invalidate(spaceID)
	return out, nil
}

// Delete removes an item.
func (v *Vault) Delete(ctx context.Context, actor audit.Actor, spaceID, id uuid.UUID) error {
	err := v.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		n, err := tx.Exec(ctx, "DELETE FROM vault_items WHERE id = $1 AND space_id = $2", id, spaceID)
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return v.audit.Record(ctx, actor, &spaceID, "vault.delete", "vault_item:"+id.String(), nil)
	})
	if err == nil {
		v.Redactor.invalidate(spaceID)
	}
	return err
}

// List returns the items of a space. Callers must not expose Ciphertext or Nonce.
func (v *Vault) List(ctx context.Context, spaceID uuid.UUID) ([]*VaultItem, error) {
	if tx, ok := drel.FromContext(ctx); ok {
		return drel.NewTxRepository(tx, VaultItemMeta).Where(VaultItems.SpaceID.Eq(spaceID)).OrderBy(VaultItems.Name.Asc()).All(ctx)
	}
	return drel.NewRepository(v.e, VaultItemMeta).Where(VaultItems.SpaceID.Eq(spaceID)).OrderBy(VaultItems.Name.Asc()).All(ctx)
}

// Get returns one item of a space.
func (v *Vault) Get(ctx context.Context, spaceID, id uuid.UUID) (*VaultItem, error) {
	item, err := drel.NewRepository(v.e, VaultItemMeta).Where(VaultItems.ID.Eq(id)).Where(VaultItems.SpaceID.Eq(spaceID)).FirstOrNil(ctx)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrNotFound
	}
	return item, nil
}

// Reveal decrypts an item for server-side use only: the broker and model adapters.
// The plaintext never goes to an API response, a log or a model context.
func (v *Vault) Reveal(ctx context.Context, spaceID, id uuid.UUID) (string, error) {
	item, err := v.Get(ctx, spaceID, id)
	if err != nil {
		return "", err
	}
	return v.decrypt(ctx, item)
}

func (v *Vault) decrypt(ctx context.Context, item *VaultItem) (string, error) {
	k, err := v.dataKey(ctx, item.SpaceID)
	if err != nil {
		return "", err
	}
	if item.KeyVersion != k.version {
		return "", fmt.Errorf("vault: item key version %d, current %d", item.KeyVersion, k.version)
	}
	n, err := unb64(item.Nonce)
	if err != nil {
		return "", err
	}
	ct, err := unb64(item.Ciphertext)
	if err != nil {
		return "", err
	}
	plain, err := open(k.key, n, ct, []byte(itemAAD(item.SpaceID, item.ID())))
	if err != nil {
		return "", err
	}
	var p secretPayload
	if err := json.Unmarshal(plain, &p); err != nil {
		return "", err
	}
	return p.Value, nil
}

// Rekey re-wraps every data key with a new master key in one transaction (REQ-VLT-005).
func (v *Vault) Rekey(ctx context.Context, newMaster []byte) (int, error) {
	if len(newMaster) != KeySize {
		return 0, fmt.Errorf("new master key must have %d bytes", KeySize)
	}
	count := 0
	err := v.e.WithTx(ctx, func(ctx context.Context) error {
		repo := drel.Repo(drel.MustFromContext(ctx), DataKeyMeta)
		rows, err := repo.Where(DataKeys.KeyVersion.GTE(0)).All(ctx)
		if err != nil {
			return err
		}
		for _, r := range rows {
			aad := dataKeyAAD(r.SpaceID, r.KeyVersion)
			raw, err := unwrapKey(v.master, r.WrappedKey, aad)
			if err != nil {
				return fmt.Errorf("data key of space %s does not unwrap with the current master key: %w", r.SpaceID, err)
			}
			w, err := wrapKey(newMaster, raw, aad)
			if err != nil {
				return err
			}
			r.WrappedKey = w
			count++
		}
		secrets, err := drel.Repo(drel.MustFromContext(ctx), InstanceSecretMeta).Where(InstanceSecrets.Name.NEQ("")).All(ctx)
		if err != nil {
			return err
		}
		for _, sec := range secrets {
			raw, err := unwrapKey(v.master, sec.Wrapped, instanceAAD(sec.Name))
			if err != nil {
				return fmt.Errorf("instance secret %s does not unwrap with the current master key: %w", sec.Name, err)
			}
			if sec.Wrapped, err = wrapKey(newMaster, raw, instanceAAD(sec.Name)); err != nil {
				return err
			}
		}
		return v.audit.Record(ctx, audit.System, nil, "vault.rekey", "master_key", map[string]string{"data_keys": fmt.Sprint(count), "instance_secrets": fmt.Sprint(len(secrets))})
	})
	if err != nil {
		return 0, err
	}
	v.mu.Lock()
	v.master = newMaster
	v.keys = map[uuid.UUID]spaceKey{}
	v.mu.Unlock()
	return count, nil
}

// CheckMaster verifies that the master key unwraps every data key (doctor).
func (v *Vault) CheckMaster(ctx context.Context) error {
	rows, err := drel.NewRepository(v.e, DataKeyMeta).Where(DataKeys.KeyVersion.GTE(0)).All(ctx)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := unwrapKey(v.master, r.WrappedKey, dataKeyAAD(r.SpaceID, r.KeyVersion)); err != nil {
			return fmt.Errorf("the master key does not unwrap the data key of space %s", r.SpaceID)
		}
	}
	return nil
}

func instanceAAD(name string) string { return "fettle-instance-secret:" + name }

// InstanceSecret returns a secret of the instance. On first use, create makes the value,
// which is stored wrapped by the master key. Two processes that race keep one value.
func (v *Vault) InstanceSecret(ctx context.Context, name string, create func() ([]byte, error)) ([]byte, error) {
	row, err := drel.NewRepository(v.e, InstanceSecretMeta).Where(InstanceSecrets.Name.Eq(name)).FirstOrNil(ctx)
	if err != nil {
		return nil, err
	}
	if row == nil {
		raw, err := create()
		if err != nil {
			return nil, err
		}
		v.mu.Lock()
		master := v.master
		v.mu.Unlock()
		w, err := wrapKey(master, raw, instanceAAD(name))
		if err != nil {
			return nil, err
		}
		err = v.e.WithTx(ctx, func(ctx context.Context) error {
			drel.Repo(drel.MustFromContext(ctx), InstanceSecretMeta).Add(&InstanceSecret{Name: name, Wrapped: w})
			return nil
		})
		if err == nil {
			return raw, nil
		}
		if row, err = drel.NewRepository(v.e, InstanceSecretMeta).Where(InstanceSecrets.Name.Eq(name)).FirstOrNil(ctx); err != nil || row == nil {
			return nil, fmt.Errorf("store instance secret %s: %w", name, err)
		}
	}
	v.mu.Lock()
	master := v.master
	v.mu.Unlock()
	return unwrapKey(master, row.Wrapped, instanceAAD(name))
}
