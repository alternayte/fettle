CREATE TABLE "bots" (
    "id" TEXT PRIMARY KEY,
    "space_id" TEXT NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "name" TEXT NOT NULL,
    "job" TEXT NOT NULL,
    "instructions" TEXT NOT NULL,
    "instructions_rev" INTEGER NOT NULL,
    "zone_id" TEXT,
    "avatar" TEXT NOT NULL,
    "is_primary" INTEGER NOT NULL,
    "public_desc" TEXT NOT NULL,
    "status" TEXT NOT NULL CHECK(status IN ('active','archived')),
    "created_by_kind" TEXT NOT NULL CHECK(created_by_kind IN ('user','bot','system')),
    "created_by" TEXT NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE "bot_instructions" (
    "id" TEXT PRIMARY KEY,
    "bot_id" TEXT NOT NULL REFERENCES "bots"("id") ON DELETE CASCADE,
    "rev" INTEGER NOT NULL,
    "instructions" TEXT NOT NULL,
    "author_kind" TEXT NOT NULL,
    "author_id" TEXT NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE "bot_memory" (
    "id" TEXT PRIMARY KEY,
    "space_id" TEXT NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "bot_id" TEXT REFERENCES "bots"("id") ON DELETE CASCADE,
    "bot_key" TEXT NOT NULL,
    "content" TEXT NOT NULL,
    "rev" INTEGER NOT NULL,
    "updated_by_kind" TEXT NOT NULL,
    "updated_by" TEXT NOT NULL,
    "origin" TEXT NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE "open_work" (
    "id" TEXT PRIMARY KEY,
    "bot_id" TEXT NOT NULL REFERENCES "bots"("id") ON DELETE CASCADE,
    "text" TEXT NOT NULL,
    "due_at" DATETIME,
    "done_at" DATETIME,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS "idx_bots_space" ON "bots" ("space_id", "status");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_bot_instructions" ON "bot_instructions" ("bot_id", "rev");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_bot_memory" ON "bot_memory" ("space_id", "bot_key");
CREATE INDEX IF NOT EXISTS "idx_open_work_due" ON "open_work" ("due_at");
