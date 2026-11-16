PRAGMA defer_foreign_keys = ON;
CREATE TABLE "model_roles__drel_new" (
    "id" TEXT PRIMARY KEY,
    "space_id" TEXT NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "bot_id" TEXT,
    "bot_key" TEXT NOT NULL,
    "role" TEXT NOT NULL CHECK(role IN ('router','owner','verifier','computer_use','summarizer')),
    "targets" TEXT NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO "model_roles__drel_new" ("id", "space_id", "bot_id", "bot_key", "role", "targets", "created_at", "updated_at") SELECT "id", "space_id", "bot_id", "bot_key", "role", "targets", "created_at", "updated_at" FROM "model_roles";
DROP TABLE "model_roles";
ALTER TABLE "model_roles__drel_new" RENAME TO "model_roles";
CREATE UNIQUE INDEX IF NOT EXISTS "uq_model_roles" ON "model_roles" ("space_id", "bot_key", "role");
PRAGMA foreign_key_check;