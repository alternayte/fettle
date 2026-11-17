ALTER TABLE "usage_events" DROP CONSTRAINT IF EXISTS "chk_usage_events_unit";
ALTER TABLE "usage_events" ADD CONSTRAINT "chk_usage_events_unit" CHECK (unit IN ('tokens','seconds'));
ALTER TABLE "usage_events" DROP CONSTRAINT IF EXISTS "chk_usage_events_kind";
ALTER TABLE "usage_events" ADD CONSTRAINT "chk_usage_events_kind" CHECK (kind IN ('model','computer'));