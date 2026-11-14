CREATE TABLE "approvals" (
    "id" uuid PRIMARY KEY,
    "space_id" uuid NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "run_id" uuid NOT NULL,
    "call_id" text NOT NULL,
    "card_id" text NOT NULL,
    "tool" text NOT NULL,
    "class" text NOT NULL,
    "args_preview" text NOT NULL,
    "status" text NOT NULL,
    "hard_ask" boolean NOT NULL,
    "decided_by" text NOT NULL,
    "rule_id" uuid,
    "reason" text NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_approvals_status" CHECK (status IN ('pending','allowed','denied','expired'))
);

CREATE TABLE "policy_rules" (
    "id" uuid PRIMARY KEY,
    "space_id" uuid NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "tool" text NOT NULL,
    "connector" text NOT NULL,
    "site_pattern" text NOT NULL,
    "recipient_pattern" text NOT NULL,
    "effect" text NOT NULL,
    "created_from_card" text NOT NULL,
    "created_by" text NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_policy_rules_effect" CHECK (effect IN ('allow','ask','block'))
);

CREATE INDEX IF NOT EXISTS "idx_approvals_run" ON "approvals" ("run_id");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_approvals_card_id" ON "approvals" ("card_id");
CREATE INDEX IF NOT EXISTS "idx_policy_rules_space" ON "policy_rules" ("space_id");
