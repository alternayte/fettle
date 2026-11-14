CREATE TABLE "data_keys" (
    "id" uuid PRIMARY KEY,
    "space_id" uuid NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "wrapped_key" text NOT NULL,
    "key_version" integer NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW()
);

CREATE TABLE "vault_items" (
    "id" uuid PRIMARY KEY,
    "space_id" uuid NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "name" text NOT NULL,
    "kind" text NOT NULL,
    "site" text NOT NULL,
    "username" text NOT NULL,
    "ciphertext" text NOT NULL,
    "nonce" text NOT NULL,
    "key_version" integer NOT NULL,
    "last4" text NOT NULL,
    "updated_by" uuid,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_vault_items_kind" CHECK (kind IN ('login','secret','totp'))
);

CREATE UNIQUE INDEX IF NOT EXISTS "uq_data_keys_space_version" ON "data_keys" ("space_id", "key_version");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_vault_items_space_name" ON "vault_items" ("space_id", "name");
