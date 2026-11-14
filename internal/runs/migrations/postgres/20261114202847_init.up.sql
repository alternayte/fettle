CREATE TABLE "runs" (
    "id" uuid PRIMARY KEY,
    "space_id" uuid NOT NULL,
    "envelope_id" uuid NOT NULL REFERENCES "envelopes"("id") ON DELETE CASCADE,
    "bot_id" uuid NOT NULL,
    "parent_run_id" uuid,
    "kind" text NOT NULL,
    "status" text NOT NULL,
    "wait_reason" text NOT NULL,
    "job_id" bigint,
    "fence" bigint NOT NULL,
    "last_seq" bigint NOT NULL,
    "wake_at" timestamptz,
    "tokens_in" bigint NOT NULL,
    "tokens_out" bigint NOT NULL,
    "cost_usd" double precision NOT NULL,
    "started_at" timestamptz,
    "finished_at" timestamptz,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_runs_kind" CHECK (kind IN ('owner','subagent','verifier')),
    CONSTRAINT "chk_runs_status" CHECK (status IN ('queued','running','waiting','verifying','done','failed','escalated','cancelled'))
);

CREATE TABLE "run_events" (
    "id" uuid PRIMARY KEY,
    "run_id" uuid NOT NULL REFERENCES "runs"("id") ON DELETE CASCADE,
    "seq" bigint NOT NULL,
    "at" timestamptz NOT NULL,
    "type" text NOT NULL,
    "data" text NOT NULL,
    "payload_key" text,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS "idx_runs_bot_id" ON "runs" ("bot_id");
CREATE INDEX IF NOT EXISTS "idx_runs_envelope_id" ON "runs" ("envelope_id");
CREATE INDEX IF NOT EXISTS "idx_runs_parent_run_id" ON "runs" ("parent_run_id");
CREATE INDEX IF NOT EXISTS "idx_runs_status" ON "runs" ("status");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_run_events_seq" ON "run_events" ("run_id", "seq");
