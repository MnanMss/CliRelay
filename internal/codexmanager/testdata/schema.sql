-- SQLite fixture for Codex-Manager testing
-- Contains minimal data shapes for accounts, tokens, and usage_snapshots
-- Schema aligned with Codex-Manager core storage (accounts.rs reference)
-- All tokens are FAKE and should never appear in production or logs

-- Enable foreign keys
PRAGMA foreign_keys = ON;

-- Accounts table: aligned with CM storage schema
-- Reference: lengcy/Codex-Manager/crates/core/src/storage/accounts.rs
-- Columns: id, label, issuer, chatgpt_account_id, workspace_id, group_name, sort, status, created_at, updated_at
CREATE TABLE IF NOT EXISTS accounts (
    id TEXT PRIMARY KEY,
    label TEXT,
    issuer TEXT,
    chatgpt_account_id TEXT,
    workspace_id TEXT,
    group_name TEXT,
    sort INTEGER DEFAULT 0,
    status TEXT DEFAULT 'active',
    created_at INTEGER DEFAULT (strftime('%s', 'now')),
    updated_at INTEGER DEFAULT (strftime('%s', 'now'))
);

-- Tokens table: aligned with CM storage schema
-- Reference: token_select_columns in accounts.rs
-- Columns: account_id, id_token, access_token, refresh_token, api_key_access_token, last_refresh
CREATE TABLE IF NOT EXISTS tokens (
    account_id TEXT PRIMARY KEY,
    id_token TEXT,
    access_token TEXT,
    refresh_token TEXT,
    api_key_access_token TEXT,
    last_refresh INTEGER,
    FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
);

-- Usage snapshots table: aligned with CM storage schema
-- Reference: latest_usage_cte_sql in accounts.rs
-- Columns: account_id, used_percent, window_minutes, secondary_used_percent, secondary_window_minutes, captured_at
CREATE TABLE IF NOT EXISTS usage_snapshots (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    used_percent REAL,
    window_minutes INTEGER,
    secondary_used_percent REAL,
    secondary_window_minutes INTEGER,
    captured_at INTEGER,
    FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
);

-- Insert fake test accounts (NO REAL DATA)
-- Using CM schema fields only
INSERT INTO accounts (id, label, issuer, chatgpt_account_id, workspace_id, group_name, sort, status) VALUES
('test-account-001', 'Test Account Alpha', 'https://auth.openai.com', 'chatgpt-id-001', 'ws-001', 'test-group-a', 1, 'active'),
('test-account-002', 'Test Account Beta', 'https://auth.openai.com', 'chatgpt-id-002', 'ws-002', 'test-group-b', 2, 'active'),
('test-account-003', 'Test Account Gamma', 'https://auth.openai.com', 'chatgpt-id-003', NULL, 'test-group-a', 3, 'limited');

-- Insert fake tokens (ALL VALUES ARE FAKE AND NON-FUNCTIONAL)
-- These are intentionally fake values for testing log sanitization
INSERT INTO tokens (account_id, id_token, access_token, refresh_token, api_key_access_token, last_refresh) VALUES
('test-account-001', 'fake_id_token_abc123_not_real', 'fake_access_token_abc123xyz789_not_real', 'fake_refresh_token_def456uvw012_not_real', NULL, 1700000000),
('test-account-002', 'fake_id_token_def456_not_real', 'fake_access_token_jkl012mno345_not_real', 'fake_refresh_token_pqr678stu901_not_real', NULL, 1700000000),
('test-account-003', 'fake_id_token_ghi789_not_real', 'fake_access_token_cde890fgh123_not_real', 'fake_refresh_token_ijk456lmn789_not_real', NULL, 1700000000);

-- Insert fake usage snapshots
INSERT INTO usage_snapshots (id, account_id, used_percent, window_minutes, secondary_used_percent, secondary_window_minutes, captured_at) VALUES
('usage-test-001', 'test-account-001', 42.5, 60, NULL, NULL, 1699913600),
('usage-test-002', 'test-account-002', 95.0, 60, 80.0, 60, 1699913600),
('usage-test-003', 'test-account-003', 15.0, 180, NULL, NULL, 1699913600);

-- Create indexes for common queries (aligned with CM usage patterns)
CREATE INDEX IF NOT EXISTS idx_accounts_status ON accounts(status);
CREATE INDEX IF NOT EXISTS idx_accounts_group ON accounts(group_name);
CREATE INDEX IF NOT EXISTS idx_accounts_sort ON accounts(sort);
CREATE INDEX IF NOT EXISTS idx_tokens_account ON tokens(account_id);
CREATE INDEX IF NOT EXISTS idx_usage_account ON usage_snapshots(account_id);
CREATE INDEX IF NOT EXISTS idx_usage_captured ON usage_snapshots(captured_at);
