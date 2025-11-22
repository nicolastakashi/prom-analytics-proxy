-- +goose Up
-- Add httpHeaders column to queries table

ALTER TABLE queries
  ADD COLUMN IF NOT EXISTS httpHeaders JSONB;
