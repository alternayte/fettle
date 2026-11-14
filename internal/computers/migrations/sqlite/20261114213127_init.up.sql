CREATE TABLE "computers" (
    "id" TEXT PRIMARY KEY,
    "zone_id" TEXT NOT NULL REFERENCES "zones"("id") ON DELETE CASCADE,
    "provider_id" TEXT NOT NULL,
    "provider_ref" TEXT NOT NULL,
    "state" TEXT NOT NULL CHECK(state IN ('none','starting','running','sleeping','stopping','error','quarantined')),
    "agentd_url" TEXT NOT NULL,
    "token_hash" TEXT NOT NULL,
    "cert_pem" TEXT NOT NULL,
    "image_digest" TEXT NOT NULL,
    "workspace_rev" INTEGER NOT NULL,
    "last_active_at" DATETIME,
    "error" TEXT NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE "providers" (
    "id" TEXT PRIMARY KEY,
    "space_id" TEXT,
    "kind" TEXT NOT NULL CHECK(kind IN ('host','docker','applecontainer','kiln','e2b')),
    "name" TEXT NOT NULL,
    "config" TEXT NOT NULL,
    "secret_item_id" TEXT,
    "caps" TEXT NOT NULL,
    "enabled" INTEGER NOT NULL,
    "tested_at" DATETIME,
    "test_result" TEXT NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE "workspaces" (
    "id" TEXT PRIMARY KEY,
    "bot_id" TEXT,
    "zone_id" TEXT,
    "owner_key" TEXT NOT NULL,
    "kind" TEXT NOT NULL CHECK(kind IN ('bot_home','zone_shared')),
    "rev" INTEGER NOT NULL,
    "object_key" TEXT NOT NULL,
    "size" INTEGER NOT NULL,
    "sha256" TEXT NOT NULL,
    "space_id" TEXT NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS "idx_computers_zone" ON "computers" ("zone_id");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_workspaces_owner" ON "workspaces" ("owner_key", "kind");
