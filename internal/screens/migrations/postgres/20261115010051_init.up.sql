CREATE TABLE "screen_leases" (
    "id" uuid PRIMARY KEY,
    "bot_id" uuid NOT NULL,
    "space_id" uuid NOT NULL,
    "holder_kind" text NOT NULL,
    "holder_id" text NOT NULL,
    "expires_at" timestamptz NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS "uq_screen_leases_bot_id" ON "screen_leases" ("bot_id");
