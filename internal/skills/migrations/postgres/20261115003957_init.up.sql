CREATE TABLE "skills" (
    "id" uuid PRIMARY KEY,
    "space_id" uuid NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "name" text NOT NULL,
    "description" text NOT NULL,
    "source" text NOT NULL,
    "current_version" integer NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_skills_source" CHECK (source IN ('local','skael'))
);

CREATE TABLE "bot_skills" (
    "id" uuid PRIMARY KEY,
    "bot_id" uuid NOT NULL REFERENCES "bots"("id") ON DELETE CASCADE,
    "skill_id" uuid NOT NULL REFERENCES "skills"("id") ON DELETE CASCADE,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW()
);

CREATE TABLE "skill_versions" (
    "id" uuid PRIMARY KEY,
    "skill_id" uuid NOT NULL REFERENCES "skills"("id") ON DELETE CASCADE,
    "version" integer NOT NULL,
    "sha256" text NOT NULL,
    "object_key" text NOT NULL,
    "skill_md" text NOT NULL,
    "files" jsonb NOT NULL,
    "has_evals" boolean NOT NULL,
    "upstream" integer NOT NULL,
    "created_by" text NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS "uq_bot_skills" ON "bot_skills" ("bot_id", "skill_id");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_skills_space_name" ON "skills" ("space_id", "name");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_skill_versions" ON "skill_versions" ("skill_id", "version");
