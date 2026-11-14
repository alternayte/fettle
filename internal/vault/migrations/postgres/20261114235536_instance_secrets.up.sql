CREATE TABLE "instance_secrets" (
    "id" uuid PRIMARY KEY,
    "name" text NOT NULL,
    "wrapped" text NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS "uq_instance_secrets_name" ON "instance_secrets" ("name");