CREATE TABLE "approvals" (
    "id" TEXT PRIMARY KEY,
    "space_id" TEXT NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "run_id" TEXT NOT NULL,
    "call_id" TEXT NOT NULL,
    "card_id" TEXT NOT NULL,
    "tool" TEXT NOT NULL,
    "class" TEXT NOT NULL,
    "args_preview" TEXT NOT NULL,
    "status" TEXT NOT NULL CHECK(status IN ('pending','allowed','denied','expired')),
    "hard_ask" INTEGER NOT NULL,
    "decided_by" TEXT NOT NULL,
    "rule_id" TEXT,
    "reason" TEXT NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE "policy_rules" (
    "id" TEXT PRIMARY KEY,
    "space_id" TEXT NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "tool" TEXT NOT NULL,
    "connector" TEXT NOT NULL,
    "site_pattern" TEXT NOT NULL,
    "recipient_pattern" TEXT NOT NULL,
    "effect" TEXT NOT NULL CHECK(effect IN ('allow','ask','block')),
    "created_from_card" TEXT NOT NULL,
    "created_by" TEXT NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS "idx_approvals_run" ON "approvals" ("run_id");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_approvals_card_id" ON "approvals" ("card_id");
CREATE INDEX IF NOT EXISTS "idx_policy_rules_space" ON "policy_rules" ("space_id");
