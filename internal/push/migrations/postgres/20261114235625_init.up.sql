CREATE TABLE "push_subscriptions" (
    "id" uuid PRIMARY KEY,
    "user_id" uuid NOT NULL,
    "endpoint" text NOT NULL,
    "p256dh" text NOT NULL,
    "auth" text NOT NULL,
    "user_agent" text NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS "idx_push_subscriptions_user_id" ON "push_subscriptions" ("user_id");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_push_subscriptions_endpoint" ON "push_subscriptions" ("endpoint");
