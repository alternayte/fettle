ALTER TABLE "connectors" ADD COLUMN "env" TEXT NOT NULL DEFAULT '{}';
ALTER TABLE "connectors" ADD COLUMN "variables" TEXT NOT NULL DEFAULT '[]';
ALTER TABLE "connector_accounts" ADD COLUMN "secret_env_names" TEXT NOT NULL DEFAULT '[]';
ALTER TABLE "oauth_states" ADD COLUMN "account_id" TEXT;
-- The OAuth client secret was stored in plain text under client_secret_sealed. Move it to
-- client_secret_plain; the server seals it into client_secret_sealed at start.
UPDATE "connectors" SET "oauth" = json_set(json_remove("oauth", '$.client_secret_sealed'), '$.client_secret_plain', json_extract("oauth", '$.client_secret_sealed')) WHERE json_extract("oauth", '$.client_secret_sealed') <> '';
