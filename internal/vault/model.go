package vault

import (
	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// DataKey is a space data key wrapped by the master key.
type DataKey struct {
	drel.Model[uuid.UUID] `db:"unique_index=uq_data_keys_space_version[space_id,key_version]"`
	SpaceID               uuid.UUID `db:"space_id,references=spaces,on_delete=cascade"`
	WrappedKey            string    `db:"wrapped_key"`
	KeyVersion            int       `db:"key_version"`
}

// VaultItem is an encrypted vault value. Ciphertext and nonce are base64 text.
type VaultItem struct {
	drel.Model[uuid.UUID] `db:"table=vault_items,unique_index=uq_vault_items_space_name[space_id,name]"`
	SpaceID               uuid.UUID  `db:"space_id,references=spaces,on_delete=cascade"`
	Name                  string     `db:"name"`
	Kind                  string     `db:"kind,check=kind IN ('login','secret','totp')"`
	Site                  string     `db:"site"`
	Username              string     `db:"username"`
	Ciphertext            string     `db:"ciphertext"`
	Nonce                 string     `db:"nonce"`
	KeyVersion            int        `db:"key_version"`
	Last4                 string     `db:"last4"`
	UpdatedBy             *uuid.UUID `db:"updated_by"`
}
