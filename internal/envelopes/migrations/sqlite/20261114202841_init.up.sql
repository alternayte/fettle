CREATE TABLE "envelopes" (
    "id" TEXT PRIMARY KEY,
    "parent_id" TEXT,
    "root_id" TEXT NOT NULL,
    "space_id" TEXT NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "kind" TEXT NOT NULL CHECK(kind IN ('message','routine','webhook','handoff','part','verify')),
    "origin" TEXT NOT NULL CHECK(origin IN ('user','routine','webhook','bot')),
    "requester" TEXT NOT NULL,
    "owner_bot_id" TEXT,
    "chat_id" TEXT,
    "message_id" TEXT,
    "goal" TEXT NOT NULL,
    "acceptance" TEXT NOT NULL,
    "context_refs" TEXT NOT NULL,
    "capabilities" TEXT NOT NULL,
    "budget" TEXT NOT NULL,
    "step" TEXT,
    "idempotency_key" TEXT NOT NULL,
    "deadline" DATETIME,
    "status" TEXT NOT NULL CHECK(status IN ('created','routed','running','verifying','done','failed','escalated','cancelled')),
    "decision" TEXT NOT NULL,
    "result" TEXT,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS "idx_envelopes_root_id" ON "envelopes" ("root_id");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_envelopes_idem" ON "envelopes" ("space_id", "idempotency_key");
