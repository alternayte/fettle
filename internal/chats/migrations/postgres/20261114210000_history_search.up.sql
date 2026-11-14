-- Full-text search over a bot's past messages and run events (search_history).
CREATE TABLE history_search (
    id bigserial PRIMARY KEY,
    bot_id uuid NOT NULL,
    kind text NOT NULL,
    ref text NOT NULL,
    at timestamptz NOT NULL,
    body text NOT NULL,
    tsv tsvector GENERATED ALWAYS AS (to_tsvector('simple', body)) STORED
);
CREATE INDEX idx_history_search_tsv ON history_search USING gin (tsv);
CREATE INDEX idx_history_search_bot ON history_search (bot_id, id);
