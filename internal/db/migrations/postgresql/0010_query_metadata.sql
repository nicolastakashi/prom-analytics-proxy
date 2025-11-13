-- +goose Up
-- Add metadata column to queries table

ALTER TABLE queries
  ADD COLUMN IF NOT EXISTS metadata JSONB;
