CREATE TABLE "screen_leases" (
    "id" TEXT PRIMARY KEY,
    "bot_id" TEXT NOT NULL,
    "space_id" TEXT NOT NULL,
    "holder_kind" TEXT NOT NULL,
    "holder_id" TEXT NOT NULL,
    "expires_at" DATETIME NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS "uq_screen_leases_bot_id" ON "screen_leases" ("bot_id");
