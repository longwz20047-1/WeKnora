-- Migration: 000014_wechat_auth (rollback)
-- Description: Remove WeChat Work OAuth tables
DO $$ BEGIN RAISE NOTICE '[Migration 000014] Rolling back WeChat OAuth...'; END $$;

DROP TABLE IF EXISTS oauth_bindings;
DROP TABLE IF EXISTS wechat_configs;

DO $$ BEGIN RAISE NOTICE '[Migration 000014] Rollback complete.'; END $$;
