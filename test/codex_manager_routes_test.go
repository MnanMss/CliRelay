package test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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

func TestCodexManagerRoutesFeatureFlag(t *testing.T) {
	t.Run("enabled_route_is_visible_on_real_server", func(t *testing.T) {
		baseURL, stopServer := startServerWithCodexManagerFlag(t, true)
		defer stopServer()

		client := &http.Client{Timeout: 3 * time.Second}
		resp := doManagementGET(t, client, baseURL+codexmanager.ManagementNamespace+"/accounts", true)
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected enabled codex-manager route status %d, got %d: %s", http.StatusOK, resp.StatusCode, string(body))
		}

		actionResp := doManagementJSONRequest(t, client, http.MethodPost, baseURL+codexmanager.ManagementNamespace+"/login/start", true, `{}`)
		actionBody := readAndCloseResponseBody(t, actionResp)
		if actionResp.StatusCode == http.StatusNotFound {
			t.Fatalf("expected enabled codex-manager login/start route to be registered, got 404: %s", actionBody)
		}
	})

	t.Run("disabled_route_is_hidden_but_legacy_management_route_still_works", func(t *testing.T) {
		baseURL, stopServer := startServerWithCodexManagerFlag(t, false)
		defer stopServer()

		client := &http.Client{Timeout: 3 * time.Second}
		disabledResp := doManagementGET(t, client, baseURL+codexmanager.ManagementNamespace+"/accounts", true)
		disabledBody := readAndCloseResponseBody(t, disabledResp)

		if disabledResp.StatusCode != http.StatusNotFound && !strings.Contains(strings.ToLower(disabledBody), "disabled") {
			t.Fatalf("expected disabled codex-manager route to return 404 or explicit disabled result, got %d: %s", disabledResp.StatusCode, disabledBody)
		}

		legacyResp := doManagementGET(t, client, baseURL+"/v0/management/config", true)
		defer func() { _ = legacyResp.Body.Close() }()

		if legacyResp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(legacyResp.Body)
			t.Fatalf("expected legacy management config status %d when codex-manager is disabled, got %d: %s", http.StatusOK, legacyResp.StatusCode, string(body))
		}
	})
}

func TestCodexManagerRoutesReuseManagementMiddleware(t *testing.T) {
	baseURL, stopServer := startServerWithCodexManagerFlag(t, true)
	defer stopServer()

	client := &http.Client{Timeout: 3 * time.Second}
	legacyResp := doManagementGET(t, client, baseURL+"/v0/management/config", false)
	legacyBody := readAndCloseResponseBody(t, legacyResp)

	newRouteResp := doManagementGET(t, client, baseURL+codexmanager.ManagementNamespace+"/accounts", false)
	newRouteBody := readAndCloseResponseBody(t, newRouteResp)
	actionRouteResp := doManagementJSONRequest(t, client, http.MethodPost, baseURL+codexmanager.ManagementNamespace+"/login/start", false, `{}`)
	actionRouteBody := readAndCloseResponseBody(t, actionRouteResp)

	if !isManagementMiddlewareRejection(legacyResp.StatusCode) {
		t.Fatalf("expected legacy management route to reject missing key with 401/403, got %d: %s", legacyResp.StatusCode, legacyBody)
	}
	if !isManagementMiddlewareRejection(newRouteResp.StatusCode) {
		t.Fatalf("expected codex-manager route to reject missing key with 401/403, got %d: %s", newRouteResp.StatusCode, newRouteBody)
	}
	if newRouteResp.StatusCode != legacyResp.StatusCode {
		t.Fatalf("expected codex-manager route to reuse management middleware status %d, got %d", legacyResp.StatusCode, newRouteResp.StatusCode)
	}
	if newRouteResp.Header.Get("X-CPA-VERSION") == "" {
		t.Fatal("expected codex-manager route rejection to include management middleware headers")
	}
	if !isManagementMiddlewareRejection(actionRouteResp.StatusCode) {
		t.Fatalf("expected codex-manager action route to reject missing key with 401/403, got %d: %s", actionRouteResp.StatusCode, actionRouteBody)
	}
	if actionRouteResp.StatusCode != legacyResp.StatusCode {
		t.Fatalf("expected codex-manager action route to reuse management middleware status %d, got %d", legacyResp.StatusCode, actionRouteResp.StatusCode)
	}
}

func startServerWithCodexManagerFlag(t *testing.T, enabled bool) (string, func()) {
	t.Helper()

	tmpDir := t.TempDir()
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatalf("failed to create auth directory: %v", err)
	}

	hashedSecret, err := bcrypt.GenerateFromPassword([]byte(testManagementKey), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("failed to hash management secret: %v", err)
	}

	port := reservePort(t)
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("port: 0\n"), 0o644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg := &config.Config{
		SDKConfig:              sdkconfig.SDKConfig{APIKeys: []string{"test-key"}},
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
			Enabled: enabled,
		},
	}

	authManager := sdkauth.NewManager(nil, nil, nil)
	accessManager := sdkaccess.NewManager()
	server := api.NewServer(cfg, authManager, accessManager, configPath)

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start()
	}()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitForServerReady(t, baseURL)

	stopFn := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Stop(ctx)
		select {
		case <-time.After(2 * time.Second):
		case <-errCh:
		}
	}

	return baseURL, stopFn
}

func doManagementGET(t *testing.T, client *http.Client, url string, withKey bool) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("failed to build request for %s: %v", url, err)
	}
	if withKey {
		req.Header.Set("Authorization", "Bearer "+testManagementKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed for %s: %v", url, err)
	}
	return resp
}

func doManagementJSONRequest(t *testing.T, client *http.Client, method, url string, withKey bool, body string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("failed to build request for %s: %v", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if withKey {
		req.Header.Set("Authorization", "Bearer "+testManagementKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed for %s: %v", url, err)
	}
	return resp
}

func readAndCloseResponseBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}
	return string(body)
}

func isManagementMiddlewareRejection(statusCode int) bool {
	return statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden
}
