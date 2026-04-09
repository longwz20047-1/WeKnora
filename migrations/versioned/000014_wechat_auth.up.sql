-- Migration: 000014_wechat_auth
-- Description: Add WeChat Work OAuth login support (wechat_configs + oauth_bindings)
DO $$ BEGIN RAISE NOTICE '[Migration 000014] Starting WeChat OAuth setup...'; END $$;

-- 1. 企业微信配置表
DO $$ BEGIN RAISE NOTICE '[Migration 000014] Creating table: wechat_configs'; END $$;
CREATE TABLE IF NOT EXISTS wechat_configs (
    id SERIAL PRIMARY KEY,
    corp_id VARCHAR(64) NOT NULL UNIQUE,
    corp_name VARCHAR(255) NOT NULL,
    agent_id BIGINT NOT NULL,
    agent_secret VARCHAR(255) NOT NULL,
    tenant_id INTEGER,
    callback_url VARCHAR(512),
    qrcode_redirect_url VARCHAR(512),
    is_enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,

    CONSTRAINT fk_wechat_config_tenant FOREIGN KEY (tenant_id)
        REFERENCES tenants(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_wechat_configs_corp_id ON wechat_configs(corp_id);
CREATE INDEX IF NOT EXISTS idx_wechat_configs_tenant_id ON wechat_configs(tenant_id);
CREATE INDEX IF NOT EXISTS idx_wechat_configs_deleted_at ON wechat_configs(deleted_at);

-- 2. OAuth 绑定关系表
DO $$ BEGIN RAISE NOTICE '[Migration 000014] Creating table: oauth_bindings'; END $$;
CREATE TABLE IF NOT EXISTS oauth_bindings (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id VARCHAR(36) NOT NULL,
    provider VARCHAR(50) NOT NULL,
    provider_user_id VARCHAR(255) NOT NULL,
    corp_id VARCHAR(64) NOT NULL,
    provider_email VARCHAR(255),
    provider_name VARCHAR(255),
    provider_avatar VARCHAR(512),
    extra_data JSONB,
    bind_type VARCHAR(50) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,

    CONSTRAINT fk_oauth_binding_user FOREIGN KEY (user_id)
        REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT unique_provider_user UNIQUE (provider, provider_user_id, corp_id)
);

CREATE INDEX IF NOT EXISTS idx_oauth_bindings_user_id ON oauth_bindings(user_id);
CREATE INDEX IF NOT EXISTS idx_oauth_bindings_provider ON oauth_bindings(provider, provider_user_id);
CREATE INDEX IF NOT EXISTS idx_oauth_bindings_corp_id ON oauth_bindings(corp_id);
CREATE INDEX IF NOT EXISTS idx_oauth_bindings_deleted_at ON oauth_bindings(deleted_at);

DO $$ BEGIN RAISE NOTICE '[Migration 000014] WeChat OAuth setup complete.'; END $$;
