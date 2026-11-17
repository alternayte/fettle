CREATE TABLE "web_search_providers" (
    "id" TEXT PRIMARY KEY,
    "space_id" TEXT NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "kind" TEXT NOT NULL CHECK(kind IN ('tavily','exa','searxng','brave')),
    "base_url" TEXT NOT NULL,
    "secret_item_id" TEXT REFERENCES "vault_items"("id") ON DELETE SET NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS "uq_web_search_providers_space" ON "web_search_providers" ("space_id");
