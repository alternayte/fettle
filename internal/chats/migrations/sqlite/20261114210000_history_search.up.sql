-- Full-text search over a bot's past messages and run events (search_history).
CREATE VIRTUAL TABLE history_fts USING fts5(
    body,
    bot_id UNINDEXED,
    kind UNINDEXED,
    ref UNINDEXED,
    at UNINDEXED,
    tokenize = 'unicode61'
);
