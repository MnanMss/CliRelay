package usage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	log "github.com/sirupsen/logrus"
)

// APIKeyRow mirrors config.APIKeyEntry and is used for SQLite persistence.
type APIKeyRow struct {
	Key              string   `json:"key"`
	Name             string   `json:"name,omitempty"`
	Disabled         bool     `json:"disabled,omitempty"`
	DailyLimit       int      `json:"daily-limit,omitempty"`
	TotalQuota       int      `json:"total-quota,omitempty"`
	SpendingLimit    float64  `json:"spending-limit,omitempty"`
	ConcurrencyLimit int      `json:"concurrency-limit,omitempty"`
	RPMLimit         int      `json:"rpm-limit,omitempty"`
	TPMLimit         int      `json:"tpm-limit,omitempty"`
	AllowedModels    []string `json:"allowed-models,omitempty"`
	SystemPrompt     string   `json:"system-prompt,omitempty"`
	CreatedAt        string   `json:"created-at,omitempty"`
	UpdatedAt        string   `json:"updated-at,omitempty"`
}

// ToConfigEntry converts an APIKeyRow to a config.APIKeyEntry.
func (r *APIKeyRow) ToConfigEntry() config.APIKeyEntry {
	return config.APIKeyEntry{
		Key:              r.Key,
		Name:             r.Name,
		Disabled:         r.Disabled,
		DailyLimit:       r.DailyLimit,
		TotalQuota:       r.TotalQuota,
		SpendingLimit:    r.SpendingLimit,
		ConcurrencyLimit: r.ConcurrencyLimit,
		RPMLimit:         r.RPMLimit,
		TPMLimit:         r.TPMLimit,
		AllowedModels:    r.AllowedModels,
		SystemPrompt:     r.SystemPrompt,
		CreatedAt:        r.CreatedAt,
	}
}

// APIKeyRowFromConfig converts a config.APIKeyEntry to an APIKeyRow.
func APIKeyRowFromConfig(entry config.APIKeyEntry) APIKeyRow {
	return APIKeyRow{
		Key:              entry.Key,
		Name:             entry.Name,
		Disabled:         entry.Disabled,
		DailyLimit:       entry.DailyLimit,
		TotalQuota:       entry.TotalQuota,
		SpendingLimit:    entry.SpendingLimit,
		ConcurrencyLimit: entry.ConcurrencyLimit,
		RPMLimit:         entry.RPMLimit,
		TPMLimit:         entry.TPMLimit,
		AllowedModels:    entry.AllowedModels,
		SystemPrompt:     entry.SystemPrompt,
		CreatedAt:        entry.CreatedAt,
	}
}

const createAPIKeysTableSQL = `
CREATE TABLE IF NOT EXISTS api_keys (
  key               TEXT PRIMARY KEY NOT NULL,
  name              TEXT NOT NULL DEFAULT '',
  disabled          INTEGER NOT NULL DEFAULT 0,
  daily_limit       INTEGER NOT NULL DEFAULT 0,
  total_quota       INTEGER NOT NULL DEFAULT 0,
  spending_limit    REAL NOT NULL DEFAULT 0,
  concurrency_limit INTEGER NOT NULL DEFAULT 0,
  rpm_limit         INTEGER NOT NULL DEFAULT 0,
  tpm_limit         INTEGER NOT NULL DEFAULT 0,
  allowed_models    TEXT NOT NULL DEFAULT '[]',
  system_prompt     TEXT NOT NULL DEFAULT '',
  created_at        TEXT NOT NULL DEFAULT '',
  updated_at        TEXT NOT NULL DEFAULT ''
);
`

func initAPIKeysTable(db *sql.DB) {
	if _, err := db.Exec(createAPIKeysTableSQL); err != nil {
		log.Errorf("usage: create api_keys table: %v", err)
	}
}

// MigrateAPIKeysFromConfig moves API key entries from YAML config into SQLite.
// It only migrates if the api_keys table is empty AND the config has entries.
// After migration, it clears the in-memory config slices so they won't be
// written back to YAML on the next persist.
func MigrateAPIKeysFromConfig(cfg *config.Config) int {
	db := getDB()
	if db == nil || cfg == nil {
		return 0
	}

	// Check if SQLite already has data — skip if so (idempotent)
	var count int64
	if err := db.QueryRow("SELECT COUNT(*) FROM api_keys").Scan(&count); err != nil {
		log.Errorf("usage: migration count api_keys: %v", err)
		return 0
	}
	if count > 0 {
		// Already migrated. Clear config slices to prevent stale data in YAML.
		cfg.APIKeys = nil
		cfg.APIKeyEntries = nil
		return 0
	}

	// Collect entries to migrate
	seen := make(map[string]struct{})
	var rows []APIKeyRow

	// APIKeyEntries first (richer data)
	for _, entry := range cfg.APIKeyEntries {
		trimmed := strings.TrimSpace(entry.Key)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		row := APIKeyRowFromConfig(entry)
		row.Key = trimmed
		if row.CreatedAt == "" {
			row.CreatedAt = time.Now().UTC().Format(time.RFC3339)
		}
		row.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		rows = append(rows, row)
	}

	// Legacy APIKeys (no metadata)
	for _, k := range cfg.APIKeys {
		trimmed := strings.TrimSpace(k)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		rows = append(rows, APIKeyRow{
			Key:       trimmed,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		})
	}

	if len(rows) == 0 {
		return 0
	}

	tx, err := db.Begin()
	if err != nil {
		log.Errorf("usage: begin api_keys migration: %v", err)
		return 0
	}

	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO api_keys
		(key, name, disabled, daily_limit, total_quota, spending_limit,
		 concurrency_limit, rpm_limit, tpm_limit, allowed_models, system_prompt, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		log.Errorf("usage: prepare api_keys migration: %v", err)
		return 0
	}
	defer stmt.Close()

	imported := 0
	for _, row := range rows {
		modelsJSON, _ := json.Marshal(row.AllowedModels)
		if row.AllowedModels == nil {
			modelsJSON = []byte("[]")
		}
		disabledInt := 0
		if row.Disabled {
			disabledInt = 1
		}
		if _, err := stmt.Exec(
			row.Key, row.Name, disabledInt,
			row.DailyLimit, row.TotalQuota, row.SpendingLimit,
			row.ConcurrencyLimit, row.RPMLimit, row.TPMLimit,
			string(modelsJSON), row.SystemPrompt,
			row.CreatedAt, row.UpdatedAt,
		); err != nil {
			_ = tx.Rollback()
			log.Errorf("usage: api_keys migration insert: %v", err)
			return 0
		}
		imported++
	}

	if err := tx.Commit(); err != nil {
		log.Errorf("usage: commit api_keys migration: %v", err)
		return 0
	}

	log.Infof("usage: migrated %d API keys from config to SQLite", imported)

	// Clear config slices so they won't be written back to YAML
	cfg.APIKeys = nil
	cfg.APIKeyEntries = nil

	return imported
}

// ListAPIKeys retrieves all API key entries from SQLite.
func ListAPIKeys() []APIKeyRow {
	db := getDB()
	if db == nil {
		return nil
	}

	rows, err := db.Query(`SELECT key, name, disabled, daily_limit, total_quota,
		spending_limit, concurrency_limit, rpm_limit, tpm_limit,
		allowed_models, system_prompt, created_at, updated_at
		FROM api_keys ORDER BY created_at ASC`)
	if err != nil {
		log.Errorf("usage: list api_keys: %v", err)
		return nil
	}
	defer rows.Close()

	return scanAPIKeyRows(rows)
}

// GetAPIKey retrieves a single API key entry by key string.
func GetAPIKey(key string) *APIKeyRow {
	db := getDB()
	if db == nil {
		return nil
	}

	row := db.QueryRow(`SELECT key, name, disabled, daily_limit, total_quota,
		spending_limit, concurrency_limit, rpm_limit, tpm_limit,
		allowed_models, system_prompt, created_at, updated_at
		FROM api_keys WHERE key = ?`, key)

	return scanSingleAPIKeyRow(row)
}

// UpsertAPIKey inserts or updates an API key entry.
func UpsertAPIKey(entry APIKeyRow) error {
	db := getDB()
	if db == nil {
		return fmt.Errorf("database not initialised")
	}

	trimmed := strings.TrimSpace(entry.Key)
	if trimmed == "" {
		return fmt.Errorf("key is required")
	}

	modelsJSON, _ := json.Marshal(entry.AllowedModels)
	if entry.AllowedModels == nil {
		modelsJSON = []byte("[]")
	}
	disabledInt := 0
	if entry.Disabled {
		disabledInt = 1
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if entry.CreatedAt == "" {
		entry.CreatedAt = now
	}

	_, err := db.Exec(`INSERT INTO api_keys
		(key, name, disabled, daily_limit, total_quota, spending_limit,
		 concurrency_limit, rpm_limit, tpm_limit, allowed_models, system_prompt, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			name=excluded.name, disabled=excluded.disabled,
			daily_limit=excluded.daily_limit, total_quota=excluded.total_quota,
			spending_limit=excluded.spending_limit, concurrency_limit=excluded.concurrency_limit,
			rpm_limit=excluded.rpm_limit, tpm_limit=excluded.tpm_limit,
			allowed_models=excluded.allowed_models, system_prompt=excluded.system_prompt,
			updated_at=excluded.updated_at`,
		trimmed, strings.TrimSpace(entry.Name), disabledInt,
		entry.DailyLimit, entry.TotalQuota, entry.SpendingLimit,
		entry.ConcurrencyLimit, entry.RPMLimit, entry.TPMLimit,
		string(modelsJSON), entry.SystemPrompt,
		entry.CreatedAt, now,
	)
	return err
}

// DeleteAPIKey removes an API key entry by key string.
func DeleteAPIKey(key string) error {
	db := getDB()
	if db == nil {
		return fmt.Errorf("database not initialised")
	}
	_, err := db.Exec("DELETE FROM api_keys WHERE key = ?", key)
	return err
}

// ReplaceAllAPIKeys atomically replaces all API keys with the given list.
func ReplaceAllAPIKeys(entries []APIKeyRow) error {
	db := getDB()
	if db == nil {
		return fmt.Errorf("database not initialised")
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}

	if _, err := tx.Exec("DELETE FROM api_keys"); err != nil {
		_ = tx.Rollback()
		return err
	}

	stmt, err := tx.Prepare(`INSERT INTO api_keys
		(key, name, disabled, daily_limit, total_quota, spending_limit,
		 concurrency_limit, rpm_limit, tpm_limit, allowed_models, system_prompt, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	for _, entry := range entries {
		trimmed := strings.TrimSpace(entry.Key)
		if trimmed == "" {
			continue
		}
		modelsJSON, _ := json.Marshal(entry.AllowedModels)
		if entry.AllowedModels == nil {
			modelsJSON = []byte("[]")
		}
		disabledInt := 0
		if entry.Disabled {
			disabledInt = 1
		}
		if entry.CreatedAt == "" {
			entry.CreatedAt = now
		}
		if _, err := stmt.Exec(
			trimmed, strings.TrimSpace(entry.Name), disabledInt,
			entry.DailyLimit, entry.TotalQuota, entry.SpendingLimit,
			entry.ConcurrencyLimit, entry.RPMLimit, entry.TPMLimit,
			string(modelsJSON), entry.SystemPrompt,
			entry.CreatedAt, now,
		); err != nil {
			_ = tx.Rollback()
			return err
		}
	}

	return tx.Commit()
}

// --- internal helpers ---

func scanAPIKeyRows(rows *sql.Rows) []APIKeyRow {
	var result []APIKeyRow
	for rows.Next() {
		r := scanAPIKeyFromRow(rows)
		if r != nil {
			result = append(result, *r)
		}
	}
	return result
}

type scannable interface {
	Scan(dest ...interface{}) error
}

func scanAPIKeyFromRow(row scannable) *APIKeyRow {
	var r APIKeyRow
	var disabledInt int
	var modelsJSON string
	if err := row.Scan(
		&r.Key, &r.Name, &disabledInt,
		&r.DailyLimit, &r.TotalQuota, &r.SpendingLimit,
		&r.ConcurrencyLimit, &r.RPMLimit, &r.TPMLimit,
		&modelsJSON, &r.SystemPrompt,
		&r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return nil
	}
	r.Disabled = disabledInt != 0
	if modelsJSON != "" && modelsJSON != "[]" {
		_ = json.Unmarshal([]byte(modelsJSON), &r.AllowedModels)
	}
	return &r
}

func scanSingleAPIKeyRow(row *sql.Row) *APIKeyRow {
	var r APIKeyRow
	var disabledInt int
	var modelsJSON string
	if err := row.Scan(
		&r.Key, &r.Name, &disabledInt,
		&r.DailyLimit, &r.TotalQuota, &r.SpendingLimit,
		&r.ConcurrencyLimit, &r.RPMLimit, &r.TPMLimit,
		&modelsJSON, &r.SystemPrompt,
		&r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return nil
	}
	r.Disabled = disabledInt != 0
	if modelsJSON != "" && modelsJSON != "[]" {
		_ = json.Unmarshal([]byte(modelsJSON), &r.AllowedModels)
	}
	return &r
}
