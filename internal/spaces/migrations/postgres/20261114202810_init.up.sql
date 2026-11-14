CREATE TABLE "spaces" (
    "id" uuid PRIMARY KEY,
    "name" text NOT NULL,
    "time_zone" text NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW()
);

CREATE TABLE "space_members" (
    "id" uuid PRIMARY KEY,
    "space_id" uuid NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "user_id" uuid NOT NULL REFERENCES "users"("id") ON DELETE CASCADE,
    "role" text NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_space_members_role" CHECK (role IN ('owner','member'))
);

CREATE INDEX IF NOT EXISTS "idx_space_members_user_id" ON "space_members" ("user_id");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_space_members" ON "space_members" ("space_id", "user_id");
