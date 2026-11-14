CREATE TABLE "envelopes" (
    "id" uuid PRIMARY KEY,
    "parent_id" uuid,
    "root_id" uuid NOT NULL,
    "space_id" uuid NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "kind" text NOT NULL,
    "origin" text NOT NULL,
    "requester" jsonb NOT NULL,
    "owner_bot_id" uuid,
    "chat_id" uuid,
    "message_id" uuid,
    "goal" text NOT NULL,
    "acceptance" jsonb NOT NULL,
    "context_refs" jsonb NOT NULL,
    "capabilities" jsonb NOT NULL,
    "budget" jsonb NOT NULL,
    "step" text,
    "idempotency_key" text NOT NULL,
    "deadline" timestamptz,
    "status" text NOT NULL,
    "decision" jsonb NOT NULL,
    "result" jsonb,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_envelopes_kind" CHECK (kind IN ('message','routine','webhook','handoff','part','verify')),
    CONSTRAINT "chk_envelopes_origin" CHECK (origin IN ('user','routine','webhook','bot')),
    CONSTRAINT "chk_envelopes_status" CHECK (status IN ('created','routed','running','verifying','done','failed','escalated','cancelled'))
);

CREATE INDEX IF NOT EXISTS "idx_envelopes_root_id" ON "envelopes" ("root_id");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_envelopes_idem" ON "envelopes" ("space_id", "idempotency_key");
