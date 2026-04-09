package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestGetUsageLogsPrefersCurrentAuthChannelNameByAuthIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "usage.db")
	if err := usage.InitDB(dbPath, config.RequestLogStorageConfig{}, time.UTC); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() {
		usage.CloseDB()
		_ = os.Remove(dbPath)
		_ = os.Remove(dbPath + "-wal")
		_ = os.Remove(dbPath + "-shm")
	})

	store := &memoryAuthStore{}
	manager := coreauth.NewManager(store, nil, nil)
	auth, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "oauth-auth-logs",
		FileName: "codex-test.json",
		Provider: "codex",
		Label:    "GPT1",
		Metadata: map[string]any{
			"label": "GPT1",
			"email": "pcamtu927@gmail.com",
		},
	})
	if err != nil {
		t.Fatalf("register auth: %v", err)
	}

	usage.InsertLog(
		"", "", "gpt-5.4", "pcamtu927@gmail.com", "pcamtu927@gmail.com", auth.Index,
		false, time.Now().UTC(), 123, 45,
		usage.TokenStats{InputTokens: 1, OutputTokens: 2, TotalTokens: 3},
		"", "",
	)

	h := &Handler{
		cfg:         &config.Config{},
		authManager: manager,
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/usage/logs?days=7&page=1&size=50", nil)

	h.GetUsageLogs(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d, body=%s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var payload struct {
		Items []struct {
			ChannelName  string `json:"channel_name"`
			AuthIndex    string `json:"auth_index"`
			FirstTokenMs int64  `json:"first_token_ms"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(payload.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(payload.Items))
	}
	if payload.Items[0].AuthIndex != auth.Index {
		t.Fatalf("auth_index = %q, want %q", payload.Items[0].AuthIndex, auth.Index)
	}
	if payload.Items[0].ChannelName != "GPT1" {
		t.Fatalf("channel_name = %q, want %q", payload.Items[0].ChannelName, "GPT1")
	}
	if payload.Items[0].FirstTokenMs != 45 {
		t.Fatalf("first_token_ms = %d, want %d", payload.Items[0].FirstTokenMs, 45)
	}
}

func TestGetUsageLogs_EmptyDB_DoesNotReturnNullSlices(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "usage.db")
	if err := usage.InitDB(dbPath, config.RequestLogStorageConfig{}, time.UTC); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() {
		usage.CloseDB()
		_ = os.Remove(dbPath)
		_ = os.Remove(dbPath + "-wal")
		_ = os.Remove(dbPath + "-shm")
	})

	h := &Handler{
		cfg: &config.Config{},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/usage/logs?days=7&page=1&size=50", nil)

	h.GetUsageLogs(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d, body=%s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var payload struct {
		Items []any `json:"items"`
		Filters struct {
			APIKeys     []string          `json:"api_keys"`
			APIKeyNames map[string]string `json:"api_key_names"`
			Models      []string          `json:"models"`
			Channels    []string          `json:"channels"`
		} `json:"filters"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if payload.Items == nil {
		t.Fatalf("items is null; expected []")
	}
	if payload.Filters.APIKeys == nil {
		t.Fatalf("filters.api_keys is null; expected []")
	}
	if payload.Filters.Models == nil {
		t.Fatalf("filters.models is null; expected []")
	}
	if payload.Filters.Channels == nil {
		t.Fatalf("filters.channels is null; expected []")
	}
	if payload.Filters.APIKeyNames == nil {
		t.Fatalf("filters.api_key_names is null; expected {}")
	}
}

func TestGetPublicUsageLogs_EmptyDB_DoesNotReturnNullModels(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "usage.db")
	if err := usage.InitDB(dbPath, config.RequestLogStorageConfig{}, time.UTC); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() {
		usage.CloseDB()
		_ = os.Remove(dbPath)
		_ = os.Remove(dbPath + "-wal")
		_ = os.Remove(dbPath + "-shm")
	})

	h := &Handler{
		cfg: &config.Config{},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(
		http.MethodGet,
		"/v0/management/public/usage/logs?api_key=sk-test&days=7&page=1&size=50",
		nil,
	)

	h.GetPublicUsageLogs(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d, body=%s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var payload struct {
		Filters struct {
			Models []string `json:"models"`
		} `json:"filters"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if payload.Filters.Models == nil {
		t.Fatalf("filters.models is null; expected []")
	}
}
