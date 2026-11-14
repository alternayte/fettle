CREATE TABLE "routines" (
    "id" TEXT PRIMARY KEY,
    "space_id" TEXT NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "bot_id" TEXT NOT NULL,
    "name" TEXT NOT NULL,
    "prompt" TEXT NOT NULL,
    "trigger" TEXT NOT NULL CHECK(trigger IN ('cron','webhook')),
    "cron" TEXT NOT NULL,
    "tz" TEXT NOT NULL,
    "webhook_secret" TEXT NOT NULL,
    "webhook_secret_hash" TEXT NOT NULL,
    "enabled" INTEGER NOT NULL,
    "notify" TEXT NOT NULL CHECK(notify IN ('always','on_failure','never')),
    "next_at" DATETIME,
    "send_approved" INTEGER NOT NULL,
    "created_by" TEXT NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE "routine_runs" (
    "id" TEXT PRIMARY KEY,
    "routine_id" TEXT NOT NULL REFERENCES "routines"("id") ON DELETE CASCADE,
    "envelope_id" TEXT,
    "due_at" DATETIME NOT NULL,
    "started_at" DATETIME,
    "coalesced" INTEGER NOT NULL,
    "missed" INTEGER NOT NULL,
    "source" TEXT NOT NULL CHECK(source IN ('cron','webhook','manual')),
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS "idx_routines_bot_id" ON "routines" ("bot_id");
CREATE INDEX IF NOT EXISTS "idx_routines_due" ON "routines" ("enabled", "next_at");
CREATE INDEX IF NOT EXISTS "idx_routine_runs_routine" ON "routine_runs" ("routine_id", "due_at");
