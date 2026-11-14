CREATE TABLE "spaces" (
    "id" TEXT PRIMARY KEY,
    "name" TEXT NOT NULL,
    "time_zone" TEXT NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE "space_members" (
    "id" TEXT PRIMARY KEY,
    "space_id" TEXT NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "user_id" TEXT NOT NULL REFERENCES "users"("id") ON DELETE CASCADE,
    "role" TEXT NOT NULL CHECK(role IN ('owner','member')),
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS "idx_space_members_user_id" ON "space_members" ("user_id");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_space_members" ON "space_members" ("space_id", "user_id");
