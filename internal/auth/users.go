// Package auth handles users, the local token and sessions.
package auth

import (
	"context"
	"strings"

	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// UserStore manages user rows.
type UserStore struct{ e *drel.Engine }

// NewUserStore returns the users store.
func NewUserStore(e *drel.Engine) *UserStore { return &UserStore{e: e} }

// LocalOwnerEmail is the email of the single owner in local mode.
const LocalOwnerEmail = "owner@localhost"

// EnsureLocalOwner returns the local owner and creates it on first start. created reports a new row.
func (u *UserStore) EnsureLocalOwner(ctx context.Context, name string) (*User, bool, error) {
	usr, err := drel.NewRepository(u.e, UserMeta).Where(Users.Email.Eq(LocalOwnerEmail)).FirstOrNil(ctx)
	if err != nil || usr != nil {
		return usr, false, err
	}
	usr = &User{Email: LocalOwnerEmail, Name: strings.TrimSpace(name), Status: "active", IsAdmin: true}
	err = u.e.WithTx(ctx, func(ctx context.Context) error {
		drel.Repo(drel.MustFromContext(ctx), UserMeta).Add(usr)
		return nil
	})
	return usr, err == nil, err
}

// Get returns a user.
func (u *UserStore) Get(ctx context.Context, id uuid.UUID) (*User, error) {
	return drel.NewRepository(u.e, UserMeta).Where(Users.ID.Eq(id)).FirstOrNil(ctx)
}

// SetName changes the display name.
func (u *UserStore) SetName(ctx context.Context, id uuid.UUID, name string) error {
	return u.e.WithTx(ctx, func(ctx context.Context) error {
		usr, err := drel.Repo(drel.MustFromContext(ctx), UserMeta).Where(Users.ID.Eq(id)).FirstOrNil(ctx)
		if err != nil || usr == nil {
			return err
		}
		usr.Name = strings.TrimSpace(name)
		return nil
	})
}

// Ensure returns the Fettle user of a server-mode account and creates it on first use.
// admin is applied only at creation.
func (u *UserStore) Ensure(ctx context.Context, email, name string, admin bool) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	usr, err := drel.NewRepository(u.e, UserMeta).Where(Users.Email.Eq(email)).FirstOrNil(ctx)
	if err != nil || usr != nil {
		return usr, err
	}
	usr = &User{Email: email, Name: strings.TrimSpace(name), Status: "active", IsAdmin: admin}
	err = u.e.WithTx(ctx, func(ctx context.Context) error {
		drel.Repo(drel.MustFromContext(ctx), UserMeta).Add(usr)
		return nil
	})
	if err != nil {
		// Another request created it at the same time.
		if again, gerr := drel.NewRepository(u.e, UserMeta).Where(Users.Email.Eq(email)).FirstOrNil(ctx); gerr == nil && again != nil {
			return again, nil
		}
	}
	return usr, err
}

// SetStatus makes an account active or disabled. The sign-in refuses a disabled account.
func (u *UserStore) SetStatus(ctx context.Context, id uuid.UUID, status string) error {
	return u.e.WithTx(ctx, func(ctx context.Context) error {
		usr, err := drel.Repo(drel.MustFromContext(ctx), UserMeta).Where(Users.ID.Eq(id)).FirstOrNil(ctx)
		if err != nil || usr == nil {
			return err
		}
		usr.Status = status
		return nil
	})
}

// ByEmail returns the user of an address, or nil.
func (u *UserStore) ByEmail(ctx context.Context, email string) (*User, error) {
	return drel.NewRepository(u.e, UserMeta).Where(Users.Email.Eq(strings.ToLower(strings.TrimSpace(email)))).FirstOrNil(ctx)
}
