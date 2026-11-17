-- +goose Up
ALTER TABLE users ADD COLUMN email TEXT;
