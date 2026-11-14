-- NOTE: adding NOT NULL column "sent_at" to existing table "messages"; ensure the table is empty or add a DEFAULT / backfill before applying
ALTER TABLE "messages" ADD COLUMN "sent_at" timestamptz NOT NULL;
CREATE INDEX IF NOT EXISTS "idx_messages_chat_sent" ON "messages" ("sent_at");