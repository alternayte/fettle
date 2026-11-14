CREATE TABLE "model_providers" (
    "id" TEXT PRIMARY KEY,
    "space_id" TEXT NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "name" TEXT NOT NULL,
    "preset" TEXT NOT NULL,
    "api_type" TEXT NOT NULL CHECK(api_type IN ('openai-chat','openai-responses','anthropic-messages')),
    "base_url" TEXT NOT NULL,
    "auth_style" TEXT NOT NULL CHECK(auth_style IN ('bearer','x_api_key','header','none')),
    "auth_header" TEXT NOT NULL,
    "secret_item_id" TEXT REFERENCES "vault_items"("id") ON DELETE SET NULL,
    "headers" TEXT NOT NULL,
    "models" TEXT NOT NULL,
    "smoke" TEXT NOT NULL,
    "smoke_at" DATETIME,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE "model_roles" (
    "id" TEXT PRIMARY KEY,
    "space_id" TEXT NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "bot_id" TEXT,
    "bot_key" TEXT NOT NULL,
    "role" TEXT NOT NULL CHECK(role IN ('router','owner','verifier','computer_use','summarizer')),
    "targets" TEXT NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS "uq_model_providers_space_name" ON "model_providers" ("space_id", "name");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_model_roles" ON "model_roles" ("space_id", "bot_key", "role");
