CREATE TABLE "space_invites" (
    "id" TEXT PRIMARY KEY,
    "space_id" TEXT NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "token_hash" TEXT NOT NULL,
    "role" TEXT NOT NULL CHECK(role IN ('owner','member')),
    "created_by" TEXT NOT NULL,
    "expires_at" DATETIME NOT NULL,
    "accepted_by" TEXT,
    "accepted_at" DATETIME,
    "revoked_at" DATETIME,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS "idx_space_invites_space" ON "space_invites" ("space_id");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_space_invites_hash" ON "space_invites" ("token_hash");