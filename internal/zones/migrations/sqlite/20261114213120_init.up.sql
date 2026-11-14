CREATE TABLE "zones" (
    "id" TEXT PRIMARY KEY,
    "space_id" TEXT NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "name" TEXT NOT NULL,
    "provider_id" TEXT,
    "network" TEXT NOT NULL CHECK(network IN ('open','allowlist')),
    "allow_hosts" TEXT NOT NULL,
    "size" TEXT NOT NULL CHECK(size IN ('small','browser','desktop')),
    "idle_minutes" INTEGER NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE "zone_grants" (
    "id" TEXT PRIMARY KEY,
    "zone_id" TEXT NOT NULL REFERENCES "zones"("id") ON DELETE CASCADE,
    "kind" TEXT NOT NULL CHECK(kind IN ('vault_item','connector_account')),
    "target_id" TEXT NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS "uq_zones_space_name" ON "zones" ("space_id", "name");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_zone_grants" ON "zone_grants" ("zone_id", "kind", "target_id");
