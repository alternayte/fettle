DROP INDEX IF EXISTS "idx_messages_chat_sent";
ALTER TABLE "messages" DROP COLUMN "sent_at";