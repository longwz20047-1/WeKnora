-- Extend knowledges.source column from VARCHAR(128) to VARCHAR(2048)
-- to support long URLs from browser capture and other sources.
DO $$ BEGIN RAISE NOTICE '[Migration 000013] Extending knowledges.source to VARCHAR(2048)'; END $$;
ALTER TABLE knowledges ALTER COLUMN source TYPE VARCHAR(2048);
