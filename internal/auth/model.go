package auth

import (
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// User is a person who signs in. Local mode has one owner row.
type User struct {
	drel.Model[uuid.UUID]
	Email   string `db:"email,unique"`
	Name    string `db:"name"`
	Status  string `db:"status,check=status IN ('active','disabled')"`
	IsAdmin bool   `db:"is_admin"`
}

// ExtensionToken lets the Chrome extension of the local owner share a sign-in, and nothing
// else. Only the SHA-256 hash of the token is stored.
type ExtensionToken struct {
	drel.Model[uuid.UUID]
	UserID     uuid.UUID  `db:"user_id,references=users,on_delete=cascade,index"`
	Hash       string     `db:"hash,unique"`
	Last4      string     `db:"last4"`
	LastUsedAt *time.Time `db:"last_used_at"`
}

// ExtensionPairing is a one-time code that pairs the Chrome extension with an account. Only
// the SHA-256 hash of the code is stored.
type ExtensionPairing struct {
	drel.Model[uuid.UUID]
	UserID    uuid.UUID `db:"user_id,references=users,on_delete=cascade"`
	Hash      string    `db:"hash,unique"`
	ExpiresAt time.Time `db:"expires_at"`
}
