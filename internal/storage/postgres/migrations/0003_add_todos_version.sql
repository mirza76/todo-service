-- Optimistic concurrency control. Existing rows start at version 1.
-- Backward compatible during a rolling update: pods running the previous
-- release neither read nor write the column, and the default covers their
-- inserts.
ALTER TABLE todos ADD COLUMN version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1);
