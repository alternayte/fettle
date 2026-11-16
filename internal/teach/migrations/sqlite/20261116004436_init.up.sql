CREATE TABLE "teach_sessions" (
    "id" TEXT PRIMARY KEY,
    "space_id" TEXT NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "bot_id" TEXT NOT NULL REFERENCES "bots"("id") ON DELETE CASCADE,
    "user_id" TEXT NOT NULL,
    "chat_id" TEXT NOT NULL,
    "title" TEXT NOT NULL,
    "status" TEXT NOT NULL CHECK(status IN ('recording','asking','drafting','review','saved','discarded','failed')),
    "takeover_card" TEXT NOT NULL,
    "pulled" INTEGER NOT NULL,
    "questions" TEXT NOT NULL,
    "draft" TEXT NOT NULL,
    "card_id" TEXT NOT NULL,
    "error" TEXT NOT NULL,
    "skill_id" TEXT,
    "skill_version" INTEGER NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE "teach_steps" (
    "id" TEXT PRIMARY KEY,
    "session_id" TEXT NOT NULL REFERENCES "teach_sessions"("id") ON DELETE CASCADE,
    "seq" INTEGER NOT NULL,
    "kind" TEXT NOT NULL CHECK(kind IN ('browser','command','note')),
    "url" TEXT NOT NULL,
    "action" TEXT NOT NULL,
    "role" TEXT NOT NULL,
    "name" TEXT NOT NULL,
    "value" TEXT NOT NULL,
    "secret" TEXT NOT NULL,
    "command" TEXT NOT NULL,
    "cwd" TEXT NOT NULL,
    "exit_code" INTEGER NOT NULL,
    "output" TEXT NOT NULL,
    "notes" TEXT NOT NULL,
    "screenshot" TEXT NOT NULL,
    "vault_item" TEXT NOT NULL,
    "removed" INTEGER NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS "idx_teach_sessions_bot" ON "teach_sessions" ("bot_id", "created_at");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_teach_steps" ON "teach_steps" ("session_id", "seq");
