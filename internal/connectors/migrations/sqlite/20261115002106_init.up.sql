CREATE TABLE "connectors" (
    "id" TEXT PRIMARY KEY,
    "space_id" TEXT NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "name" TEXT NOT NULL,
    "kind" TEXT NOT NULL CHECK(kind IN ('mcp_remote','mcp_stdio','composio')),
    "url" TEXT NOT NULL,
    "command" TEXT NOT NULL,
    "auth" TEXT NOT NULL CHECK(auth IN ('none','oauth','header')),
    "header_name" TEXT NOT NULL,
    "oauth" TEXT NOT NULL,
    "tools" TEXT NOT NULL,
    "icon" TEXT NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE "oauth_states" (
    "id" TEXT PRIMARY KEY,
    "state" TEXT NOT NULL,
    "connector_id" TEXT NOT NULL REFERENCES "connectors"("id") ON DELETE CASCADE,
    "label" TEXT NOT NULL,
    "verifier" TEXT NOT NULL,
    "user_id" TEXT NOT NULL,
    "expires_at" DATETIME NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE "connector_accounts" (
    "id" TEXT PRIMARY KEY,
    "connector_id" TEXT NOT NULL REFERENCES "connectors"("id") ON DELETE CASCADE,
    "space_id" TEXT NOT NULL,
    "label" TEXT NOT NULL,
    "token_ciphertext" TEXT NOT NULL,
    "scopes" TEXT NOT NULL,
    "expires_at" DATETIME,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS "uq_connectors_space_name" ON "connectors" ("space_id", "name");
CREATE INDEX IF NOT EXISTS "idx_connector_accounts_connector" ON "connector_accounts" ("connector_id");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_oauth_states_state" ON "oauth_states" ("state");
