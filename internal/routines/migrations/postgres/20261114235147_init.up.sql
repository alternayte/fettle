CREATE TABLE "routines" (
    "id" uuid PRIMARY KEY,
    "space_id" uuid NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "bot_id" uuid NOT NULL,
    "name" text NOT NULL,
    "prompt" text NOT NULL,
    "trigger" text NOT NULL,
    "cron" text NOT NULL,
    "tz" text NOT NULL,
    "webhook_secret" text NOT NULL,
    "webhook_secret_hash" text NOT NULL,
    "enabled" boolean NOT NULL,
    "notify" text NOT NULL,
    "next_at" timestamptz,
    "send_approved" boolean NOT NULL,
    "created_by" text NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_routines_trigger" CHECK (trigger IN ('cron','webhook')),
    CONSTRAINT "chk_routines_notify" CHECK (notify IN ('always','on_failure','never'))
);

CREATE TABLE "routine_runs" (
    "id" uuid PRIMARY KEY,
    "routine_id" uuid NOT NULL REFERENCES "routines"("id") ON DELETE CASCADE,
    "envelope_id" uuid,
    "due_at" timestamptz NOT NULL,
    "started_at" timestamptz,
    "coalesced" boolean NOT NULL,
    "missed" boolean NOT NULL,
    "source" text NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_routine_runs_source" CHECK (source IN ('cron','webhook','manual'))
);

CREATE INDEX IF NOT EXISTS "idx_routines_bot_id" ON "routines" ("bot_id");
CREATE INDEX IF NOT EXISTS "idx_routines_due" ON "routines" ("enabled", "next_at");
CREATE INDEX IF NOT EXISTS "idx_routine_runs_routine" ON "routine_runs" ("routine_id", "due_at");
