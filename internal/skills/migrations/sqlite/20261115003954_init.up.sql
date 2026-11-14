CREATE TABLE "skills" (
    "id" TEXT PRIMARY KEY,
    "space_id" TEXT NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "name" TEXT NOT NULL,
    "description" TEXT NOT NULL,
    "source" TEXT NOT NULL CHECK(source IN ('local','skael')),
    "current_version" INTEGER NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE "bot_skills" (
    "id" TEXT PRIMARY KEY,
    "bot_id" TEXT NOT NULL REFERENCES "bots"("id") ON DELETE CASCADE,
    "skill_id" TEXT NOT NULL REFERENCES "skills"("id") ON DELETE CASCADE,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE "skill_versions" (
    "id" TEXT PRIMARY KEY,
    "skill_id" TEXT NOT NULL REFERENCES "skills"("id") ON DELETE CASCADE,
    "version" INTEGER NOT NULL,
    "sha256" TEXT NOT NULL,
    "object_key" TEXT NOT NULL,
    "skill_md" TEXT NOT NULL,
    "files" TEXT NOT NULL,
    "has_evals" INTEGER NOT NULL,
    "upstream" INTEGER NOT NULL,
    "created_by" TEXT NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS "uq_bot_skills" ON "bot_skills" ("bot_id", "skill_id");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_skills_space_name" ON "skills" ("space_id", "name");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_skill_versions" ON "skill_versions" ("skill_id", "version");
