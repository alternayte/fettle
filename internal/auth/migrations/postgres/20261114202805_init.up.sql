CREATE TABLE "users" (
    "id" uuid PRIMARY KEY,
    "email" text NOT NULL,
    "name" text NOT NULL,
    "status" text NOT NULL,
    "is_admin" boolean NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_users_status" CHECK (status IN ('active','disabled'))
);

CREATE UNIQUE INDEX IF NOT EXISTS "uq_users_email" ON "users" ("email");
