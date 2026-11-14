CREATE TABLE "connectors" (
    "id" uuid PRIMARY KEY,
    "space_id" uuid NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "name" text NOT NULL,
    "kind" text NOT NULL,
    "url" text NOT NULL,
    "command" jsonb NOT NULL,
    "auth" text NOT NULL,
    "header_name" text NOT NULL,
    "oauth" jsonb NOT NULL,
    "tools" jsonb NOT NULL,
    "icon" text NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_connectors_kind" CHECK (kind IN ('mcp_remote','mcp_stdio','composio')),
    CONSTRAINT "chk_connectors_auth" CHECK (auth IN ('none','oauth','header'))
);

CREATE TABLE "oauth_states" (
    "id" uuid PRIMARY KEY,
    "state" text NOT NULL,
    "connector_id" uuid NOT NULL REFERENCES "connectors"("id") ON DELETE CASCADE,
    "label" text NOT NULL,
    "verifier" text NOT NULL,
    "user_id" uuid NOT NULL,
    "expires_at" timestamptz NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW()
);

CREATE TABLE "connector_accounts" (
    "id" uuid PRIMARY KEY,
    "connector_id" uuid NOT NULL REFERENCES "connectors"("id") ON DELETE CASCADE,
    "space_id" uuid NOT NULL,
    "label" text NOT NULL,
    "token_ciphertext" text NOT NULL,
    "scopes" text NOT NULL,
    "expires_at" timestamptz,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS "uq_connectors_space_name" ON "connectors" ("space_id", "name");
CREATE INDEX IF NOT EXISTS "idx_connector_accounts_connector" ON "connector_accounts" ("connector_id");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_oauth_states_state" ON "oauth_states" ("state");
