CREATE TABLE "web_search_providers" (
    "id" uuid PRIMARY KEY,
    "space_id" uuid NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "kind" text NOT NULL,
    "base_url" text NOT NULL,
    "secret_item_id" uuid REFERENCES "vault_items"("id") ON DELETE SET NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_web_search_providers_kind" CHECK (kind IN ('tavily','exa','searxng','brave'))
);

CREATE UNIQUE INDEX IF NOT EXISTS "uq_web_search_providers_space" ON "web_search_providers" ("space_id");
