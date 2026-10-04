CREATE TABLE todos (
    id         UUID        PRIMARY KEY,
    title      TEXT        NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    completed  BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

-- Supports the list query's ORDER BY created_at, id with LIMIT/OFFSET.
CREATE INDEX todos_created_at_id_idx ON todos (created_at, id);
