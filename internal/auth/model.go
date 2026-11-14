package auth

import (
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
