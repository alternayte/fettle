CREATE TABLE "run_scores" (
    "id" TEXT PRIMARY KEY,
    "space_id" TEXT NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "run_id" TEXT NOT NULL,
    "bot_id" TEXT NOT NULL,
    "step" TEXT NOT NULL,
    "skills" TEXT NOT NULL,
    "model_name" TEXT NOT NULL,
    "verifier_pass" INTEGER NOT NULL,
    "signals" INTEGER NOT NULL,
    "within_budget" INTEGER NOT NULL,
    "escalated" INTEGER NOT NULL,
    "value" INTEGER NOT NULL,
    "cost_usd" REAL NOT NULL,
    "duration_ms" INTEGER NOT NULL,
    "finished_at" DATETIME NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE "steering_signals" (
    "id" TEXT PRIMARY KEY,
    "space_id" TEXT NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "run_id" TEXT,
    "bot_id" TEXT,
    "step" TEXT NOT NULL,
    "skills" TEXT NOT NULL,
    "type" TEXT NOT NULL CHECK(type IN ('correction','draft_edited','approval_denied','rerun','unrequested_takeover','thumbs_down','card_declined','escalation_accepted_anyway','uncertain_confirmed_wrong')),
    "detail" TEXT NOT NULL,
    "ref" TEXT NOT NULL,
    "user_id" TEXT NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS "idx_run_scores_space_time" ON "run_scores" ("space_id", "finished_at");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_run_scores_run" ON "run_scores" ("run_id");
CREATE INDEX IF NOT EXISTS "idx_signals_run" ON "steering_signals" ("run_id");
CREATE INDEX IF NOT EXISTS "idx_signals_space_time" ON "steering_signals" ("space_id", "created_at");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_signals_ref" ON "steering_signals" ("space_id", "type", "ref");
