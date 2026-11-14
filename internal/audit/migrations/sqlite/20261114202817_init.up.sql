CREATE TABLE "audit_log" (
    "id" TEXT PRIMARY KEY,
    "actor_kind" TEXT NOT NULL CHECK(actor_kind IN ('user','bot','system')),
    "actor_id" TEXT NOT NULL,
    "space_id" TEXT,
    "action" TEXT NOT NULL,
    "target" TEXT NOT NULL,
    "details" TEXT NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS "idx_audit_log_action" ON "audit_log" ("action");
CREATE INDEX IF NOT EXISTS "idx_audit_space_time" ON "audit_log" ("space_id", "created_at");
