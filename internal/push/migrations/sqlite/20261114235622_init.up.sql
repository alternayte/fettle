CREATE TABLE "push_subscriptions" (
    "id" TEXT PRIMARY KEY,
    "user_id" TEXT NOT NULL,
    "endpoint" TEXT NOT NULL,
    "p256dh" TEXT NOT NULL,
    "auth" TEXT NOT NULL,
    "user_agent" TEXT NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS "idx_push_subscriptions_user_id" ON "push_subscriptions" ("user_id");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_push_subscriptions_endpoint" ON "push_subscriptions" ("endpoint");
