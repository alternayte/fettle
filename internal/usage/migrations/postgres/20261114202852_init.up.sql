CREATE TABLE "usage_events" (
    "id" uuid PRIMARY KEY,
    "space_id" uuid NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "kind" text NOT NULL,
    "bot_id" uuid,
    "zone_id" uuid,
    "run_id" uuid,
    "step" text NOT NULL,
    "role" text NOT NULL,
    "model" text NOT NULL,
    "tokens_in" bigint NOT NULL,
    "tokens_out" bigint NOT NULL,
    "cache_read" bigint NOT NULL,
    "quantity" bigint NOT NULL,
    "unit" text NOT NULL,
    "size" text NOT NULL,
    "cost_usd" double precision NOT NULL,
    "reported" boolean NOT NULL,
    "at" timestamptz NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_usage_events_kind" CHECK (kind IN ('model','computer')),
    CONSTRAINT "chk_usage_events_unit" CHECK (unit IN ('tokens','seconds'))
);

CREATE INDEX IF NOT EXISTS "idx_usage_events_run_id" ON "usage_events" ("run_id");
CREATE INDEX IF NOT EXISTS "idx_usage_space_time" ON "usage_events" ("space_id", "at");
