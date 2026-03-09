package test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/api"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/codexmanager"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	sdkaccess "github.com/router-for-me/CLIProxyAPI/v6/sdk/access"
	sdkauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
	"golang.org/x/crypto/bcrypt"
)

const testManagementKey = "test-management-key"

type timeoutNetError struct{}

func (timeoutNetError) Error() string   { return "timeout" }
func (timeoutNetError) Timeout() bool   { return true }
func (timeoutNetError) Temporary() bool { return false }

func reservePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve port: %v", err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port
}

func newCodexManagerRealServer(t *testing.T) string {
	t.Helper()

	tmpDir := t.TempDir()
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatalf("failed to create auth dir: %v", err)
	}

	hashedSecret, err := bcrypt.GenerateFromPassword([]byte(testManagementKey), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("failed to hash management key: %v", err)
	}

	port := reservePort(t)
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("port: 0\n"), 0o644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg := &config.Config{
		SDKConfig: sdkconfig.SDKConfig{
			APIKeys: []string{"test-key"},
		},
		Host:                   "127.0.0.1",
		Port:                   port,
		AuthDir:                authDir,
		Debug:                  true,
		LoggingToFile:          false,
		UsageStatisticsEnabled: false,
		RemoteManagement: config.RemoteManagement{
			SecretKey: string(hashedSecret),
		},
		CodexManager: config.CodexManagerConfig{
			Enabled: true,
		},
	}
	seedCodexManagerProjectionState(t, cfg)

	authManager := sdkauth.NewManager(nil, nil, nil)
	accessManager := sdkaccess.NewManager()
	server := api.NewServer(cfg, authManager, accessManager, configPath)

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start()
	}()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 500 * time.Millisecond}

	ready := false
	for i := 0; i < 80; i++ {
		resp, reqErr := client.Get(baseURL + "/")
		if reqErr == nil {
			_ = resp.Body.Close()
			ready = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("server did not become ready at %s", baseURL)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Stop(ctx)
		select {
		case <-time.After(2 * time.Second):
		case <-errCh:
		}
	})

	return baseURL
}

func TestCodexManagerContracts(t *testing.T) {
	t.Run("frozen_constants_and_pagination", func(t *testing.T) {
		if codexmanager.ConfigSectionName != "codex-manager" {
			t.Fatalf("expected config section codex-manager, got %q", codexmanager.ConfigSectionName)
		}
		if codexmanager.ManagementNamespace != "/v0/management/codex-manager" {
			t.Fatalf("expected namespace /v0/management/codex-manager, got %q", codexmanager.ManagementNamespace)
		}

		cfgType := reflect.TypeOf(config.Config{})
		field, ok := cfgType.FieldByName("CodexManager")
		if !ok {
			t.Fatal("Config.CodexManager field not found")
		}
		if yamlTag := field.Tag.Get("yaml"); yamlTag != codexmanager.ConfigSectionName {
			t.Fatalf("expected yaml tag %q, got %q", codexmanager.ConfigSectionName, yamlTag)
		}

		defaults := codexmanager.NormalizePagination(0, 0)
		if defaults.Page != 1 || defaults.PageSize != 20 {
			t.Fatalf("expected default page/pageSize to be 1/20, got %d/%d", defaults.Page, defaults.PageSize)
		}

		maxed := codexmanager.NormalizePagination(7, 999)
		if maxed.Page != 7 || maxed.PageSize != 100 {
			t.Fatalf("expected max pageSize 100, got %d", maxed.PageSize)
		}
	})

	t.Run("real_server_route_and_list_item_contract", func(t *testing.T) {
		baseURL := newCodexManagerRealServer(t)

		unauthResp, err := http.Get(baseURL + codexmanager.ManagementNamespace + "/accounts?page=1&pageSize=20")
		if err != nil {
			t.Fatalf("unauthorized request failed: %v", err)
		}
		_ = unauthResp.Body.Close()
		if unauthResp.StatusCode != http.StatusUnauthorized && unauthResp.StatusCode != http.StatusForbidden {
			t.Fatalf("expected status 401/403 without key, got %d", unauthResp.StatusCode)
		}

		req, err := http.NewRequest(http.MethodGet, baseURL+codexmanager.ManagementNamespace+"/accounts?page=1&pageSize=20", nil)
		if err != nil {
			t.Fatalf("failed to build request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+testManagementKey)

		client := &http.Client{Timeout: 3 * time.Second}
		listResp, err := client.Do(req)
		if err != nil {
			t.Fatalf("authorized request failed: %v", err)
		}
		defer func() { _ = listResp.Body.Close() }()

		if listResp.StatusCode != http.StatusOK {
			t.Fatalf("expected list status %d, got %d", http.StatusOK, listResp.StatusCode)
		}

		var listPayload map[string]any
		if err := json.NewDecoder(listResp.Body).Decode(&listPayload); err != nil {
			t.Fatalf("failed to decode list payload: %v", err)
		}

		for _, key := range []string{"ok", "code", "message", "retryable", "data"} {
			if _, exists := listPayload[key]; !exists {
				t.Fatalf("list response missing key %q", key)
			}
		}

		data, ok := listPayload["data"].(map[string]any)
		if !ok {
			t.Fatalf("expected list data object, got %#v", listPayload["data"])
		}

		items, ok := data["items"].([]any)
		if !ok {
			t.Fatalf("expected data.items array, got %#v", data["items"])
		}
		if len(items) == 0 {
			t.Fatalf("expected non-empty data.items for contract validation")
		}

		firstItem, ok := items[0].(map[string]any)
		if !ok {
			t.Fatalf("expected first item object, got %#v", items[0])
		}

		requiredKeys := []string{"accountId", "label", "groupName", "status", "sort", "relayEnabled", "runtimeSource", "runtimeIncluded", "usageSummary", "lastSyncedAt", "stale"}
		for _, key := range requiredKeys {
			if _, exists := firstItem[key]; !exists {
				t.Fatalf("list item missing key %q", key)
			}
		}

		for _, forbidden := range []string{"auth_index", "authIndex", "token", "accessToken", "refreshToken", "workspaceHeader"} {
			if _, exists := firstItem[forbidden]; exists {
				t.Fatalf("list item leaked forbidden key %q", forbidden)
			}
		}
		if accountID, ok := firstItem["accountId"].(string); !ok || accountID == "" {
			t.Fatalf("expected non-empty accountId, got %#v", firstItem["accountId"])
		}
	})

	t.Run("detail_contract_fields", func(t *testing.T) {
		baseURL := newCodexManagerRealServer(t)

		req, err := http.NewRequest(http.MethodGet, baseURL+codexmanager.ManagementNamespace+"/accounts/"+seedAccountAlpha, nil)
		if err != nil {
			t.Fatalf("failed to build detail request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+testManagementKey)

		client := &http.Client{Timeout: 3 * time.Second}
		detailResp, err := client.Do(req)
		if err != nil {
			t.Fatalf("detail request failed: %v", err)
		}
		defer func() { _ = detailResp.Body.Close() }()

		if detailResp.StatusCode != http.StatusOK {
			t.Fatalf("expected detail status %d, got %d", http.StatusOK, detailResp.StatusCode)
		}

		var detailPayload map[string]any
		if err := json.NewDecoder(detailResp.Body).Decode(&detailPayload); err != nil {
			t.Fatalf("failed to decode detail payload: %v", err)
		}
		detailData, ok := detailPayload["data"].(map[string]any)
		if !ok {
			t.Fatalf("expected detail data object, got %#v", detailPayload["data"])
		}

		requiredKeys := []string{"accountId", "label", "groupName", "status", "sort", "relayEnabled", "runtimeSource", "runtimeIncluded", "usageSummary", "lastSyncedAt", "stale", "usageSnapshot"}
		for _, key := range requiredKeys {
			if _, exists := detailData[key]; !exists {
				t.Fatalf("detail response missing key %q", key)
			}
		}

		for _, forbidden := range []string{"auth_index", "authIndex", "token", "accessToken", "refreshToken", "workspaceHeader"} {
			if _, exists := detailData[forbidden]; exists {
				t.Fatalf("detail response leaked forbidden key %q", forbidden)
			}
		}

		if runtimeSource, ok := detailData["runtimeSource"].(string); !ok || runtimeSource != codexmanager.RuntimeSourceCodexManager {
			t.Fatalf("expected runtimeSource=%q, got %#v", codexmanager.RuntimeSourceCodexManager, detailData["runtimeSource"])
		}
	})
}

func TestCodexManagerErrorMapping(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		status    int
		code      string
		retryable bool
	}{
		{name: "invalid_pagination", err: codexmanager.ErrInvalidPagination, status: http.StatusBadRequest, code: codexmanager.CodeBadRequest, retryable: false},
		{name: "invalid_account_id", err: codexmanager.ErrInvalidAccountID, status: http.StatusBadRequest, code: codexmanager.CodeBadRequest, retryable: false},
		{name: "not_found", err: codexmanager.ErrAccountNotFound, status: http.StatusNotFound, code: codexmanager.CodeAccountNotFound, retryable: false},
		{name: "deadline_exceeded", err: context.DeadlineExceeded, status: http.StatusServiceUnavailable, code: codexmanager.CodeUpstreamTimeout, retryable: true},
		{name: "net_timeout", err: timeoutNetError{}, status: http.StatusServiceUnavailable, code: codexmanager.CodeUpstreamTimeout, retryable: true},
		{name: "upstream_unavailable", err: codexmanager.ErrUpstreamUnavailable, status: http.StatusServiceUnavailable, code: codexmanager.CodeUpstreamUnavailable, retryable: true},
		{name: "coded_error", err: codexmanager.NewCodedError(http.StatusBadGateway, "upstream_rejected", "upstream rejected", false), status: http.StatusBadGateway, code: "upstream_rejected", retryable: false},
		{name: "fallback_internal", err: errors.New("boom"), status: http.StatusInternalServerError, code: codexmanager.CodeInternalError, retryable: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			status, payload := codexmanager.MapError(tc.err)

			if status != tc.status {
				t.Fatalf("expected status %d, got %d", tc.status, status)
			}
			if payload.OK {
				t.Fatalf("expected ok=false for %s", tc.name)
			}
			if payload.Code != tc.code {
				t.Fatalf("expected code %q, got %q", tc.code, payload.Code)
			}
			if payload.Retryable != tc.retryable {
				t.Fatalf("expected retryable=%v, got %v", tc.retryable, payload.Retryable)
			}
			if payload.Message == "" {
				t.Fatalf("expected non-empty message for %s", tc.name)
			}
		})
	}
}
