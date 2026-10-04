-- Supports GET /todos?completed=... : filter on completed, then the same
-- (created_at, id) ordering used for pagination.
CREATE INDEX todos_completed_created_at_id_idx ON todos (completed, created_at, id);
