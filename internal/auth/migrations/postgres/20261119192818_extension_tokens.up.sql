CREATE TABLE "extension_pairings" (
    "id" uuid PRIMARY KEY,
    "user_id" uuid NOT NULL REFERENCES "users"("id") ON DELETE CASCADE,
    "hash" text NOT NULL,
    "expires_at" timestamptz NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS "uq_extension_pairings_hash" ON "extension_pairings" ("hash");
CREATE TABLE "extension_tokens" (
    "id" uuid PRIMARY KEY,
    "user_id" uuid NOT NULL REFERENCES "users"("id") ON DELETE CASCADE,
    "hash" text NOT NULL,
    "last4" text NOT NULL,
    "last_used_at" timestamptz,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS "idx_extension_tokens_user_id" ON "extension_tokens" ("user_id");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_extension_tokens_hash" ON "extension_tokens" ("hash");