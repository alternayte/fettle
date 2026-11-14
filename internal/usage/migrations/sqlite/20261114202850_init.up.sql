CREATE TABLE "usage_events" (
    "id" TEXT PRIMARY KEY,
    "space_id" TEXT NOT NULL REFERENCES "spaces"("id") ON DELETE CASCADE,
    "kind" TEXT NOT NULL CHECK(kind IN ('model','computer')),
    "bot_id" TEXT,
    "zone_id" TEXT,
    "run_id" TEXT,
    "step" TEXT NOT NULL,
    "role" TEXT NOT NULL,
    "model" TEXT NOT NULL,
    "tokens_in" INTEGER NOT NULL,
    "tokens_out" INTEGER NOT NULL,
    "cache_read" INTEGER NOT NULL,
    "quantity" INTEGER NOT NULL,
    "unit" TEXT NOT NULL CHECK(unit IN ('tokens','seconds')),
    "size" TEXT NOT NULL,
    "cost_usd" REAL NOT NULL,
    "reported" INTEGER NOT NULL,
    "at" DATETIME NOT NULL,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS "idx_usage_events_run_id" ON "usage_events" ("run_id");
CREATE INDEX IF NOT EXISTS "idx_usage_space_time" ON "usage_events" ("space_id", "at");
