-- +goose Up
CREATE TABLE posts (id INTEGER PRIMARY KEY, user_id INTEGER, title TEXT);

-- +goose Down
DROP TABLE posts;
