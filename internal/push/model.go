package push

import (
	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// Subscription is one browser or phone that receives Web Push for a user (§15.2).
type Subscription struct {
	drel.Model[uuid.UUID] `db:"table=push_subscriptions"`
	UserID                uuid.UUID `db:"user_id,index"`
	Endpoint              string    `db:"endpoint,unique"`
	P256dh                string    `db:"p256dh"`
	Auth                  string    `db:"auth"`
	UserAgent             string    `db:"user_agent"`
}
