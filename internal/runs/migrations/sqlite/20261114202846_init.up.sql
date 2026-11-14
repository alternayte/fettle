CREATE TABLE "runs" (
    "id" TEXT PRIMARY KEY,
    "space_id" TEXT NOT NULL,
    "envelope_id" TEXT NOT NULL REFERENCES "envelopes"("id") ON DELETE CASCADE,
    "bot_id" TEXT NOT NULL,
    "parent_run_id" TEXT,
    "kind" TEXT NOT NULL CHECK(kind IN ('owner','subagent','verifier')),
    "status" TEXT NOT NULL CHECK(status IN ('queued','running','waiting','verifying','done','failed','escalated','cancelled')),
    "wait_reason" TEXT NOT NULL,
    "job_id" INTEGER,
    "fence" INTEGER NOT NULL,
    "last_seq" INTEGER NOT NULL,
    "wake_at" DATETIME,
    "tokens_in" INTEGER NOT NULL,
    "tokens_out" INTEGER NOT NULL,
    "cost_usd" REAL NOT NULL,
    "started_at" DATETIME,
    "finished_at" DATETIME,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE "run_events" (
    "id" TEXT PRIMARY KEY,
    "run_id" TEXT NOT NULL REFERENCES "runs"("id") ON DELETE CASCADE,
    "seq" INTEGER NOT NULL,
    "at" DATETIME NOT NULL,
    "type" TEXT NOT NULL,
    "data" TEXT NOT NULL,
    "payload_key" TEXT,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS "idx_runs_bot_id" ON "runs" ("bot_id");
CREATE INDEX IF NOT EXISTS "idx_runs_envelope_id" ON "runs" ("envelope_id");
CREATE INDEX IF NOT EXISTS "idx_runs_parent_run_id" ON "runs" ("parent_run_id");
CREATE INDEX IF NOT EXISTS "idx_runs_status" ON "runs" ("status");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_run_events_seq" ON "run_events" ("run_id", "seq");
