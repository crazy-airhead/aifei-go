-- +goose Up
CREATE TABLE embed_table (id INTEGER PRIMARY KEY, name TEXT);

-- +goose Down
DROP TABLE embed_table;
