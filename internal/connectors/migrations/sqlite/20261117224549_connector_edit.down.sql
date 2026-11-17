UPDATE "connectors" SET "oauth" = json_set(json_remove("oauth", '$.client_secret_plain'), '$.client_secret_sealed', json_extract("oauth", '$.client_secret_plain')) WHERE json_extract("oauth", '$.client_secret_plain') <> '';
ALTER TABLE "oauth_states" DROP COLUMN "account_id";
ALTER TABLE "connector_accounts" DROP COLUMN "secret_env_names";
ALTER TABLE "connectors" DROP COLUMN "variables";
ALTER TABLE "connectors" DROP COLUMN "env";
