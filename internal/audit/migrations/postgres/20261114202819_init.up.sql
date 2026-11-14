CREATE TABLE "audit_log" (
    "id" uuid PRIMARY KEY,
    "actor_kind" text NOT NULL,
    "actor_id" text NOT NULL,
    "space_id" uuid,
    "action" text NOT NULL,
    "target" text NOT NULL,
    "details" jsonb NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_audit_log_actor_kind" CHECK (actor_kind IN ('user','bot','system'))
);

CREATE INDEX IF NOT EXISTS "idx_audit_log_action" ON "audit_log" ("action");
CREATE INDEX IF NOT EXISTS "idx_audit_space_time" ON "audit_log" ("space_id", "created_at");
