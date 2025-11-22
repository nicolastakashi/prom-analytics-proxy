-- +goose NO TRANSACTION
-- +goose Up
-- Add httpHeaders column to queries table

ALTER TABLE queries
  ADD COLUMN httpHeaders string;
