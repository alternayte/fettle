CREATE TABLE "chats" (
    "id" uuid PRIMARY KEY,
    "space_id" uuid NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "kind" text NOT NULL,
    "name" text NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_chats_kind" CHECK (kind IN ('direct','group'))
);

CREATE TABLE "chat_members" (
    "id" uuid PRIMARY KEY,
    "chat_id" uuid NOT NULL REFERENCES "chats"("id") ON DELETE CASCADE,
    "bot_id" uuid NOT NULL REFERENCES "bots"("id") ON DELETE CASCADE,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW()
);

CREATE TABLE "messages" (
    "id" uuid PRIMARY KEY,
    "space_id" uuid NOT NULL,
    "chat_id" uuid NOT NULL REFERENCES "chats"("id") ON DELETE CASCADE,
    "author_kind" text NOT NULL,
    "author_id" text NOT NULL,
    "body" text NOT NULL,
    "attachments" jsonb NOT NULL,
    "envelope_id" uuid,
    "card" jsonb,
    "reply_to" uuid,
    "reactions" jsonb NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_messages_author_kind" CHECK (author_kind IN ('user','bot','system'))
);

CREATE INDEX IF NOT EXISTS "idx_chats_space" ON "chats" ("space_id");
CREATE INDEX IF NOT EXISTS "idx_chat_members_bot_id" ON "chat_members" ("bot_id");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_chat_members" ON "chat_members" ("chat_id", "bot_id");
CREATE INDEX IF NOT EXISTS "idx_messages_chat_time" ON "messages" ("chat_id", "created_at");
CREATE INDEX IF NOT EXISTS "idx_messages_envelope_id" ON "messages" ("envelope_id");
