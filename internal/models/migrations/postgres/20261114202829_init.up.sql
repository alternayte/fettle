CREATE TABLE "model_providers" (
    "id" uuid PRIMARY KEY,
    "space_id" uuid NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "name" text NOT NULL,
    "preset" text NOT NULL,
    "api_type" text NOT NULL,
    "base_url" text NOT NULL,
    "auth_style" text NOT NULL,
    "auth_header" text NOT NULL,
    "secret_item_id" uuid REFERENCES "vault_items"("id") ON DELETE SET NULL,
    "headers" jsonb NOT NULL,
    "models" jsonb NOT NULL,
    "smoke" jsonb NOT NULL,
    "smoke_at" timestamptz,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_model_providers_api_type" CHECK (api_type IN ('openai-chat','openai-responses','anthropic-messages')),
    CONSTRAINT "chk_model_providers_auth_style" CHECK (auth_style IN ('bearer','x_api_key','header','none'))
);

CREATE TABLE "model_roles" (
    "id" uuid PRIMARY KEY,
    "space_id" uuid NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "bot_id" uuid,
    "bot_key" text NOT NULL,
    "role" text NOT NULL,
    "targets" jsonb NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_model_roles_role" CHECK (role IN ('router','owner','verifier','computer_use','summarizer'))
);

CREATE UNIQUE INDEX IF NOT EXISTS "uq_model_providers_space_name" ON "model_providers" ("space_id", "name");
CREATE UNIQUE INDEX IF NOT EXISTS "uq_model_roles" ON "model_roles" ("space_id", "bot_key", "role");
