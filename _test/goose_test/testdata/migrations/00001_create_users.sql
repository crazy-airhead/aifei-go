-- +goose Up
CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);

-- +goose Down
DROP TABLE users;
