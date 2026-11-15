CREATE TABLE "run_scores" (
    "id" uuid PRIMARY KEY,
    "space_id" uuid NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "run_id" uuid NOT NULL,
    "bot_id" uuid NOT NULL,
    "step" text NOT NULL,
    "skills" jsonb NOT NULL,
    "model_name" text NOT NULL,
    "verifier_pass" boolean NOT NULL,
    "signals" integer NOT NULL,
    "within_budget" boolean NOT NULL,
    "escalated" boolean NOT NULL,
    "value" integer NOT NULL,
    "cost_usd" double precision NOT NULL,
    "duration_ms" bigint NOT NULL,
    "finished_at" timestamptz NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW()
);

CREATE TABLE "steering_signals" (
    "id" uuid PRIMARY KEY,
    "space_id" uuid NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "run_id" uuid,
    "bot_id" uuid,
    "step" text NOT NULL,
    "skills" jsonb NOT NULL,
    "type" text NOT NULL,
    "detail" text NOT NULL,
    "ref" text NOT NULL,
    "user_id" text NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_steering_signals_type" CHECK (type IN ('correction','draft_edited','approval_denied','rerun','unrequested_takeover','thumbs_down','card_declined','escalation_accepted_anyway','uncertain_confirmed_wrong'))
);

CREATE INDEX IF NOT EXISTS "idx_run_scores_space_time" ON "run_scores" ("space_id", "finished_at");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_run_scores_run" ON "run_scores" ("run_id");
CREATE INDEX IF NOT EXISTS "idx_signals_run" ON "steering_signals" ("run_id");
CREATE INDEX IF NOT EXISTS "idx_signals_space_time" ON "steering_signals" ("space_id", "created_at");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_signals_ref" ON "steering_signals" ("space_id", "type", "ref");
