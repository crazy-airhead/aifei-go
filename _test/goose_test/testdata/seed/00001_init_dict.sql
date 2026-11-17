-- +goose Up
CREATE TABLE dict (id INTEGER PRIMARY KEY, name TEXT);
INSERT INTO dict (id, name) VALUES (1, 'alpha');
INSERT INTO dict (id, name) VALUES (2, 'beta');

-- +goose Down
DROP TABLE dict;
