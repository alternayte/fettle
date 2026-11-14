CREATE TABLE "data_keys" (
    "id" TEXT PRIMARY KEY,
    "space_id" TEXT NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "wrapped_key" TEXT NOT NULL,
    "key_version" INTEGER NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE "vault_items" (
    "id" TEXT PRIMARY KEY,
    "space_id" TEXT NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "name" TEXT NOT NULL,
    "kind" TEXT NOT NULL CHECK(kind IN ('login','secret','totp')),
    "site" TEXT NOT NULL,
    "username" TEXT NOT NULL,
    "ciphertext" TEXT NOT NULL,
    "nonce" TEXT NOT NULL,
    "key_version" INTEGER NOT NULL,
    "last4" TEXT NOT NULL,
    "updated_by" TEXT,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS "uq_data_keys_space_version" ON "data_keys" ("space_id", "key_version");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_vault_items_space_name" ON "vault_items" ("space_id", "name");
