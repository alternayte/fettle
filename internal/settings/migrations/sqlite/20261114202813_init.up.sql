CREATE TABLE "settings" (
    "id" TEXT PRIMARY KEY,
    "scope" TEXT NOT NULL CHECK(scope IN ('instance','space')),
    "space_id" TEXT REFERENCES "spaces"("id") ON DELETE CASCADE,
    "space_key" TEXT NOT NULL,
    "key" TEXT NOT NULL,
    "value" TEXT NOT NULL,
    "revision" INTEGER NOT NULL,
    "updated_by" TEXT,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE "settings_history" (
    "id" TEXT PRIMARY KEY,
    "setting_id" TEXT NOT NULL REFERENCES "settings"("id") ON DELETE CASCADE,
    "revision" INTEGER NOT NULL,
    "old_value" TEXT,
    "new_value" TEXT NOT NULL,
    "updated_by" TEXT,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS "uq_settings_scope_key" ON "settings" ("scope", "space_key", "key");
CREATE INDEX IF NOT EXISTS "idx_settings_history_setting" ON "settings_history" ("setting_id", "revision");
