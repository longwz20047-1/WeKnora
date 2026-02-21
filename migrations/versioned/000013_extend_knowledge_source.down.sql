-- Revert knowledges.source column back to VARCHAR(128)
ALTER TABLE knowledges ALTER COLUMN source TYPE VARCHAR(128);
