CREATE TABLE "zones" (
    "id" uuid PRIMARY KEY,
    "space_id" uuid NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "name" text NOT NULL,
    "provider_id" uuid,
    "network" text NOT NULL,
    "allow_hosts" jsonb NOT NULL,
    "size" text NOT NULL,
    "idle_minutes" integer NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_zones_network" CHECK (network IN ('open','allowlist')),
    CONSTRAINT "chk_zones_size" CHECK (size IN ('small','browser','desktop'))
);

CREATE TABLE "zone_grants" (
    "id" uuid PRIMARY KEY,
    "zone_id" uuid NOT NULL REFERENCES "zones"("id") ON DELETE CASCADE,
    "kind" text NOT NULL,
    "target_id" uuid NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_zone_grants_kind" CHECK (kind IN ('vault_item','connector_account'))
);

CREATE UNIQUE INDEX IF NOT EXISTS "uq_zones_space_name" ON "zones" ("space_id", "name");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_zone_grants" ON "zone_grants" ("zone_id", "kind", "target_id");
