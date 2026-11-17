UPDATE "connectors" SET "oauth" = ("oauth" - 'client_secret_plain') || jsonb_build_object('client_secret_sealed', "oauth"->'client_secret_plain') WHERE "oauth"->>'client_secret_plain' <> '';
ALTER TABLE "oauth_states" DROP COLUMN "account_id";
ALTER TABLE "connector_accounts" DROP COLUMN "secret_env_names";
ALTER TABLE "connectors" DROP COLUMN "variables";
ALTER TABLE "connectors" DROP COLUMN "env";
