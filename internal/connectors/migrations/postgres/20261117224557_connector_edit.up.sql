ALTER TABLE "connectors" ADD COLUMN "env" jsonb NOT NULL DEFAULT '{}';
ALTER TABLE "connectors" ADD COLUMN "variables" jsonb NOT NULL DEFAULT '[]';
ALTER TABLE "connector_accounts" ADD COLUMN "secret_env_names" jsonb NOT NULL DEFAULT '[]';
ALTER TABLE "oauth_states" ADD COLUMN "account_id" uuid;
-- The OAuth client secret was stored in plain text under client_secret_sealed. Move it to
-- client_secret_plain; the server seals it into client_secret_sealed at start.
UPDATE "connectors" SET "oauth" = ("oauth" - 'client_secret_sealed') || jsonb_build_object('client_secret_plain', "oauth"->'client_secret_sealed') WHERE "oauth"->>'client_secret_sealed' <> '';
