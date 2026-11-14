CREATE TABLE "chats" (
    "id" TEXT PRIMARY KEY,
    "space_id" TEXT NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "kind" TEXT NOT NULL CHECK(kind IN ('direct','group')),
    "name" TEXT NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE "chat_members" (
    "id" TEXT PRIMARY KEY,
    "chat_id" TEXT NOT NULL REFERENCES "chats"("id") ON DELETE CASCADE,
    "bot_id" TEXT NOT NULL REFERENCES "bots"("id") ON DELETE CASCADE,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE "messages" (
    "id" TEXT PRIMARY KEY,
    "space_id" TEXT NOT NULL,
    "chat_id" TEXT NOT NULL REFERENCES "chats"("id") ON DELETE CASCADE,
    "author_kind" TEXT NOT NULL CHECK(author_kind IN ('user','bot','system')),
    "author_id" TEXT NOT NULL,
    "body" TEXT NOT NULL,
    "attachments" TEXT NOT NULL,
    "envelope_id" TEXT,
    "card" TEXT,
    "reply_to" TEXT,
    "reactions" TEXT NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS "idx_chats_space" ON "chats" ("space_id");
CREATE INDEX IF NOT EXISTS "idx_chat_members_bot_id" ON "chat_members" ("bot_id");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_chat_members" ON "chat_members" ("chat_id", "bot_id");
CREATE INDEX IF NOT EXISTS "idx_messages_chat_time" ON "messages" ("chat_id", "created_at");
CREATE INDEX IF NOT EXISTS "idx_messages_envelope_id" ON "messages" ("envelope_id");
