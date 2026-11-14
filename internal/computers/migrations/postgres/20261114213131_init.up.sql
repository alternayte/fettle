CREATE TABLE "computers" (
    "id" uuid PRIMARY KEY,
    "zone_id" uuid NOT NULL REFERENCES "zones"("id") ON DELETE CASCADE,
    "provider_id" uuid NOT NULL,
    "provider_ref" text NOT NULL,
    "state" text NOT NULL,
    "agentd_url" text NOT NULL,
    "token_hash" text NOT NULL,
    "cert_pem" text NOT NULL,
    "image_digest" text NOT NULL,
    "workspace_rev" bigint NOT NULL,
    "last_active_at" timestamptz,
    "error" text NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_computers_state" CHECK (state IN ('none','starting','running','sleeping','stopping','error','quarantined'))
);

CREATE TABLE "providers" (
    "id" uuid PRIMARY KEY,
    "space_id" uuid,
    "kind" text NOT NULL,
    "name" text NOT NULL,
    "config" jsonb NOT NULL,
    "secret_item_id" uuid,
    "caps" jsonb NOT NULL,
    "enabled" boolean NOT NULL,
    "tested_at" timestamptz,
    "test_result" text NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_providers_kind" CHECK (kind IN ('host','docker','applecontainer','kiln','e2b'))
);

CREATE TABLE "workspaces" (
    "id" uuid PRIMARY KEY,
    "bot_id" uuid,
    "zone_id" uuid,
    "owner_key" text NOT NULL,
    "kind" text NOT NULL,
    "rev" bigint NOT NULL,
    "object_key" text NOT NULL,
    "size" bigint NOT NULL,
    "sha256" text NOT NULL,
    "space_id" uuid NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_workspaces_kind" CHECK (kind IN ('bot_home','zone_shared'))
);

CREATE INDEX IF NOT EXISTS "idx_computers_zone" ON "computers" ("zone_id");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_workspaces_owner" ON "workspaces" ("owner_key", "kind");
