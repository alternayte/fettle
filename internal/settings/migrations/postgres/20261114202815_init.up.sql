CREATE TABLE "settings" (
    "id" uuid PRIMARY KEY,
    "scope" text NOT NULL,
    "space_id" uuid REFERENCES "spaces"("id") ON DELETE CASCADE,
    "space_key" text NOT NULL,
    "key" text NOT NULL,
    "value" text NOT NULL,
    "revision" integer NOT NULL,
    "updated_by" uuid,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT "chk_settings_scope" CHECK (scope IN ('instance','space'))
);

CREATE TABLE "settings_history" (
    "id" uuid PRIMARY KEY,
    "setting_id" uuid NOT NULL REFERENCES "settings"("id") ON DELETE CASCADE,
    "revision" integer NOT NULL,
    "old_value" text,
    "new_value" text NOT NULL,
    "updated_by" uuid,
    "created_at" timestamptz NOT NULL DEFAULT NOW(),
    "updated_at" timestamptz NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS "uq_settings_scope_key" ON "settings" ("scope", "space_key", "key");
CREATE INDEX IF NOT EXISTS "idx_settings_history_setting" ON "settings_history" ("setting_id", "revision");
