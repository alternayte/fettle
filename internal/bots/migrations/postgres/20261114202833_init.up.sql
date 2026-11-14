CREATE TABLE "bots" (
    "id" uuid PRIMARY KEY,
    "space_id" uuid NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "name" text NOT NULL,
    "job" text NOT NULL,
    "instructions" text NOT NULL,
    "instructions_rev" integer NOT NULL,
    "zone_id" uuid,
    "avatar" text NOT NULL,
    "is_primary" boolean NOT NULL,
    "public_desc" text NOT NULL,
    "status" text NOT NULL,
    "created_by_kind" text NOT NULL,
    "created_by" text NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_bots_status" CHECK (status IN ('active','archived')),
    CONSTRAINT "chk_bots_created_by_kind" CHECK (created_by_kind IN ('user','bot','system'))
);

CREATE TABLE "bot_instructions" (
    "id" uuid PRIMARY KEY,
    "bot_id" uuid NOT NULL REFERENCES "bots"("id") ON DELETE CASCADE,
    "rev" integer NOT NULL,
    "instructions" text NOT NULL,
    "author_kind" text NOT NULL,
    "author_id" text NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW()
);

CREATE TABLE "bot_memory" (
    "id" uuid PRIMARY KEY,
    "space_id" uuid NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "bot_id" uuid REFERENCES "bots"("id") ON DELETE CASCADE,
    "bot_key" text NOT NULL,
    "content" text NOT NULL,
    "rev" integer NOT NULL,
    "updated_by_kind" text NOT NULL,
    "updated_by" text NOT NULL,
    "origin" text NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW()
);

CREATE TABLE "open_work" (
    "id" uuid PRIMARY KEY,
    "bot_id" uuid NOT NULL REFERENCES "bots"("id") ON DELETE CASCADE,
    "text" text NOT NULL,
    "due_at" timestamptz,
    "done_at" timestamptz,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS "idx_bots_space" ON "bots" ("space_id", "status");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_bot_instructions" ON "bot_instructions" ("bot_id", "rev");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_bot_memory" ON "bot_memory" ("space_id", "bot_key");
CREATE INDEX IF NOT EXISTS "idx_open_work_due" ON "open_work" ("due_at");
