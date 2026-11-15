CREATE TABLE "space_invites" (
    "id" uuid PRIMARY KEY,
    "space_id" uuid NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "token_hash" text NOT NULL,
    "role" text NOT NULL,
    "created_by" uuid NOT NULL,
    "expires_at" timestamptz NOT NULL,
    "accepted_by" uuid,
    "accepted_at" timestamptz,
    "revoked_at" timestamptz,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_space_invites_role" CHECK (role IN ('owner','member'))
);
CREATE INDEX IF NOT EXISTS "idx_space_invites_space" ON "space_invites" ("space_id");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_space_invites_hash" ON "space_invites" ("token_hash");