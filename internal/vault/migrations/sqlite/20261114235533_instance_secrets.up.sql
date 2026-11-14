CREATE TABLE "instance_secrets" (
    "id" TEXT PRIMARY KEY,
    "name" TEXT NOT NULL,
    "wrapped" TEXT NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS "uq_instance_secrets_name" ON "instance_secrets" ("name");