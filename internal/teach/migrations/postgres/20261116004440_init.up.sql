CREATE TABLE "teach_sessions" (
    "id" uuid PRIMARY KEY,
    "space_id" uuid NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "bot_id" uuid NOT NULL REFERENCES "bots"("id") ON DELETE CASCADE,
    "user_id" uuid NOT NULL,
    "chat_id" uuid NOT NULL,
    "title" text NOT NULL,
    "status" text NOT NULL,
    "takeover_card" text NOT NULL,
    "pulled" integer NOT NULL,
    "questions" jsonb NOT NULL,
    "draft" jsonb NOT NULL,
    "card_id" text NOT NULL,
    "error" text NOT NULL,
    "skill_id" uuid,
    "skill_version" integer NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_teach_sessions_status" CHECK (status IN ('recording','asking','drafting','review','saved','discarded','failed'))
);

CREATE TABLE "teach_steps" (
    "id" uuid PRIMARY KEY,
    "session_id" uuid NOT NULL REFERENCES "teach_sessions"("id") ON DELETE CASCADE,
    "seq" integer NOT NULL,
    "kind" text NOT NULL,
    "url" text NOT NULL,
    "action" text NOT NULL,
    "role" text NOT NULL,
    "name" text NOT NULL,
    "value" text NOT NULL,
    "secret" text NOT NULL,
    "command" text NOT NULL,
    "cwd" text NOT NULL,
    "exit_code" integer NOT NULL,
    "output" text NOT NULL,
    "notes" jsonb NOT NULL,
    "screenshot" text NOT NULL,
    "vault_item" text NOT NULL,
    "removed" boolean NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_teach_steps_kind" CHECK (kind IN ('browser','command','note'))
);

CREATE INDEX IF NOT EXISTS "idx_teach_sessions_bot" ON "teach_sessions" ("bot_id", "created_at");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_teach_steps" ON "teach_steps" ("session_id", "seq");
