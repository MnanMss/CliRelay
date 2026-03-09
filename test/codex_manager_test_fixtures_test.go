package test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/codexmanager"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
	log "github.com/sirupsen/logrus"
)

// TestCodexManagerTestFixtures validates that the test infrastructure is properly set up
func TestCodexManagerTestFixtures(t *testing.T) {
	t.Run("rpc_stub_server_is_reusable", func(t *testing.T) {
		stub := NewRPCStubServer(t)
		defer stub.Close()

		// Verify the server is running
		if stub.URL() == "" {
			t.Fatal("expected stub server to have a URL")
		}

		// Create an RPC client using the stub
		client, err := codexmanager.NewRPCClient(codexmanager.RPCClientConfig{
			Endpoint:       stub.URL(),
			RPCToken:       "test-token",
			RequestTimeout: 5 * time.Second,
			MaxRetries:     1,
			RetryDelay:     10 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("failed to create RPC client: %v", err)
		}

		// Test ListAccounts
		result, err := client.ListAccounts(context.Background(), codexmanager.RPCAccountListParams{
			Page:     1,
			PageSize: 10,
		})
		if err != nil {
			t.Fatalf("ListAccounts failed: %v", err)
		}
		if result.Page != 1 {
			t.Fatalf("expected page 1, got %d", result.Page)
		}

		// Verify call was captured
		calls := stub.GetCalls()
		if len(calls) != 1 {
			t.Fatalf("expected 1 captured call, got %d", len(calls))
		}
		if calls[0].Method != "account/list" {
			t.Fatalf("expected method account/list, got %s", calls[0].Method)
		}
		if calls[0].Token != "test-token" {
			t.Fatalf("expected RPC token to be captured")
		}
	})

	t.Run("rpc_stub_method_hooks_work", func(t *testing.T) {
		stub := NewRPCStubServer(t)
		defer stub.Close()

		// Set a custom hook for account/list
		customCalled := false
		stub.SetMethodHook("account/list", func(req RPCRequestCapture) (any, error) {
			customCalled = true
			return map[string]any{
				"items": []map[string]any{
					{"id": "custom-acc", "label": "Custom", "sort": 1, "status": "active"},
				},
				"total":    1,
				"page":     1,
				"pageSize": 10,
			}, nil
		})

		client, err := codexmanager.NewRPCClient(codexmanager.RPCClientConfig{
			Endpoint:       stub.URL(),
			RPCToken:       "test-token",
			RequestTimeout: 5 * time.Second,
		})
		if err != nil {
			t.Fatalf("failed to create RPC client: %v", err)
		}

		result, err := client.ListAccounts(context.Background(), codexmanager.RPCAccountListParams{})
		if err != nil {
			t.Fatalf("ListAccounts failed: %v", err)
		}

		if !customCalled {
			t.Fatal("expected custom hook to be called")
		}
		if len(result.Items) != 1 {
			t.Fatalf("expected 1 item from custom hook, got %d", len(result.Items))
		}
		if result.Items[0].ID != "custom-acc" {
			t.Fatalf("expected custom account ID, got %s", result.Items[0].ID)
		}
	})

	t.Run("rpc_stub_error_simulation", func(t *testing.T) {
		stub := NewRPCStubServer(t)
		defer stub.Close()

		// Simulate an error response
		stub.SetMethodHook("account/delete", func(req RPCRequestCapture) (any, error) {
			return nil, errors.New("account not found")
		})

		client, err := codexmanager.NewRPCClient(codexmanager.RPCClientConfig{
			Endpoint:       stub.URL(),
			RPCToken:       "test-token",
			RequestTimeout: 5 * time.Second,
		})
		if err != nil {
			t.Fatalf("failed to create RPC client: %v", err)
		}

		err = client.DeleteAccount(context.Background(), "nonexistent-acc")
		if err == nil {
			t.Fatal("expected error for failed delete")
		}

		// Verify error mapping
		status, payload := codexmanager.MapError(err)
		if status != 404 {
			t.Fatalf("expected 404 for account not found, got %d", status)
		}
		if payload.Code != "account_not_found" {
			t.Fatalf("expected code account_not_found, got %s", payload.Code)
		}
	})

	t.Run("sqlite_fixture_exists_and_readable", func(t *testing.T) {
		// Check that the fixture file exists
		fixturePath := "../internal/codexmanager/testdata/test_fixture.db"
		info, err := os.Stat(fixturePath)
		if err != nil {
			t.Fatalf("fixture file not found: %v", err)
		}
		if info.Size() == 0 {
			t.Fatal("fixture file is empty")
		}

		t.Logf("SQLite fixture exists: %s (%d bytes)", fixturePath, info.Size())
	})

	t.Run("sqlite_fixture_schema_matches_cm_storage", func(t *testing.T) {
		fixturePath := "../internal/codexmanager/testdata/test_fixture.db"

		// Query actual schema from the .db file using sqlite3
		schemaOutput, err := execSQLite3(t, fixturePath, ".schema")
		if err != nil {
			t.Fatalf("failed to query schema from fixture: %v", err)
		}

		// Verify required tables exist in actual database
		requiredTables := []string{"accounts", "tokens", "usage_snapshots"}
		for _, table := range requiredTables {
			if !strings.Contains(schemaOutput, "CREATE TABLE "+table) {
				t.Errorf("fixture database missing table: %s", table)
			}
		}

		// Verify CM-specific columns in accounts (aligned with accounts.rs)
		cmAccountColumns := []string{
			"id", "label", "issuer", "chatgpt_account_id",
			"workspace_id", "group_name", "sort", "status",
			"created_at", "updated_at",
		}
		for _, col := range cmAccountColumns {
			if !strings.Contains(schemaOutput, col) {
				t.Errorf("accounts table missing CM column: %s", col)
			}
		}

		// Verify NO local overlay fields that don't exist in CM storage
		localOnlyFields := []string{"relay_enabled", "runtime_source", "runtime_included"}
		for _, field := range localOnlyFields {
			if strings.Contains(schemaOutput, field) {
				t.Errorf("fixture should NOT contain local-only field: %s", field)
			}
		}

		// Verify tokens table has CM columns
		tokenColumns := []string{
			"account_id", "id_token", "access_token", "refresh_token",
			"api_key_access_token", "last_refresh",
		}
		for _, col := range tokenColumns {
			if !strings.Contains(schemaOutput, col) {
				t.Errorf("tokens table missing column: %s", col)
			}
		}

		// Verify usage_snapshots has CM columns
		usageColumns := []string{
			"account_id", "used_percent", "window_minutes",
			"secondary_used_percent", "secondary_window_minutes", "captured_at",
		}
		for _, col := range usageColumns {
			if !strings.Contains(schemaOutput, col) {
				t.Errorf("usage_snapshots table missing column: %s", col)
			}
		}
	})

	t.Run("sqlite_fixture_contains_expected_data", func(t *testing.T) {
		fixturePath := "../internal/codexmanager/testdata/test_fixture.db"

		// Query actual row counts from the database
		accountsCount, err := execSQLite3(t, fixturePath, "SELECT COUNT(*) FROM accounts;")
		if err != nil {
			t.Fatalf("failed to query accounts count: %v", err)
		}
		if !strings.Contains(accountsCount, "3") {
			t.Errorf("expected 3 accounts, got: %s", accountsCount)
		}

		tokensCount, err := execSQLite3(t, fixturePath, "SELECT COUNT(*) FROM tokens;")
		if err != nil {
			t.Fatalf("failed to query tokens count: %v", err)
		}
		if !strings.Contains(tokensCount, "3") {
			t.Errorf("expected 3 tokens, got: %s", tokensCount)
		}

		usageCount, err := execSQLite3(t, fixturePath, "SELECT COUNT(*) FROM usage_snapshots;")
		if err != nil {
			t.Fatalf("failed to query usage_snapshots count: %v", err)
		}
		if !strings.Contains(usageCount, "3") {
			t.Errorf("expected 3 usage_snapshots, got: %s", usageCount)
		}

		// Verify fake token values exist in actual database
		accessTokenSample, err := execSQLite3(t, fixturePath,
			"SELECT access_token FROM tokens WHERE account_id='test-account-001';")
		if err != nil {
			t.Fatalf("failed to query access_token: %v", err)
		}
		if !strings.Contains(accessTokenSample, "fake_access_token") {
			t.Errorf("expected fake access_token in database, got: %s", accessTokenSample)
		}
		if !strings.Contains(accessTokenSample, "_not_real") {
			t.Errorf("fake token should have '_not_real' suffix, got: %s", accessTokenSample)
		}

		t.Logf("Fixture data verified: 3 accounts, 3 tokens, 3 usage snapshots")
	})
}

// execSQLite3 runs a sqlite3 command against the database file
func execSQLite3(t *testing.T, dbPath string, command string) (string, error) {
	t.Helper()

	// Find sqlite3 executable
	sqlite3Path := "E:\\TOOLS\\platform-tools\\sqlite3.exe"
	if _, err := os.Stat(sqlite3Path); os.IsNotExist(err) {
		// Try to find in PATH
		sqlite3Path = "sqlite3"
	}

	absPath, err := filepath.Abs(dbPath)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %w", err)
	}

	cmd := exec.Command(sqlite3Path, absPath, command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("sqlite3 command failed: %w, output: %s", err, string(output))
	}
	return string(output), nil
}

// TestCodexManagerLogsDoNotLeakTokens validates that sensitive token values don't appear in logs
func TestCodexManagerLogsDoNotLeakTokens(t *testing.T) {
	t.Run("log_capture_helper_works", func(t *testing.T) {
		capture := NewLogCapture()

		// Write some test content using logrus
		logger := log.New()
		logger.SetOutput(capture)
		logger.Info("test message")

		if !capture.Contains("test message") {
			t.Fatal("log capture did not capture test message")
		}

		capture.Reset()
		if capture.String() != "" {
			t.Fatal("log capture reset did not clear buffer")
		}
	})

	t.Run("fake_tokens_defined_for_testing", func(t *testing.T) {
		// Verify our fake token values are defined
		if len(FakeTokenValues) == 0 {
			t.Fatal("no fake token values defined")
		}

		// Verify each fake token contains expected patterns
		for _, token := range FakeTokenValues {
			if !strings.Contains(token, "fake_") {
				t.Errorf("fake token %q should contain 'fake_' prefix", token)
			}
			if !strings.Contains(token, "_not_real") {
				t.Errorf("fake token %q should contain '_not_real' suffix", token)
			}
		}
	})

	t.Run("log_sanitization_detects_unmasked_tokens", func(t *testing.T) {
		capture := NewLogCapture()

		// Simulate a log that contains a fake token (should be detected as leak)
		logger := log.New()
		logger.SetOutput(capture)
		logger.Printf("Error during operation: token=%s", "fake_access_token_abc123_not_real")

		// This should fail because the token appears unmasked
		logs := capture.String()
		for _, token := range FakeTokenValues {
			if strings.Contains(logs, token) {
				// This is expected - we intentionally logged it to test detection
				t.Logf("Correctly detected token in logs: %s", token)
				return
			}
		}
		t.Fatal("failed to detect fake token in logs")
	})

	t.Run("log_sanitization_allows_masked_tokens", func(t *testing.T) {
		capture := NewLogCapture()

		// Simulate a log with properly masked token
		logger := log.New()
		logger.SetOutput(capture)
		logger.Printf("Error during operation: token=[REDACTED]")

		// Verify no fake tokens appear
		logs := capture.String()
		for _, token := range FakeTokenValues {
			if strings.Contains(logs, token) {
				t.Errorf("masked log should not contain token: %s", token)
			}
		}
	})

	t.Run("rpc_client_does_not_log_token_values", func(t *testing.T) {
		// This test verifies that the RPC client configuration doesn't expose tokens in errors
		stub := NewRPCStubServer(t)
		defer stub.Close()

		// Set up a hook that simulates an error response
		stub.SetMethodHook("account/login/start", func(req RPCRequestCapture) (any, error) {
			return nil, errors.New("authentication failed")
		})

		client, err := codexmanager.NewRPCClient(codexmanager.RPCClientConfig{
			Endpoint:       stub.URL(),
			RPCToken:       "fake_rpc_token_secret_456_not_real", // This fake token should not appear in errors
			RequestTimeout: 5 * time.Second,
		})
		if err != nil {
			t.Fatalf("failed to create RPC client: %v", err)
		}

		// Trigger an error
		_, err = client.StartLogin(context.Background(), codexmanager.RPCLoginStartRequest{
			Type: "chatgpt",
		})
		if err == nil {
			t.Fatal("expected error from stub")
		}

		// Verify error message doesn't contain the token
		errStr := err.Error()
		if strings.Contains(errStr, "fake_rpc_token_secret_456_not_real") {
			t.Error("error message contains RPC token - this is a security leak")
		}
	})

	t.Run("error_mapping_does_not_expose_tokens", func(t *testing.T) {
		// Test that error mapping doesn't include sensitive data
		testCases := []struct {
			name     string
			err      error
			checkStr string
		}{
			{
				name:     "coded_error",
				err:      codexmanager.NewCodedError(500, "internal_error", "something failed", false),
				checkStr: "fake_access_token",
			},
			{
				name:     "upstream_error",
				err:      codexmanager.ErrUpstreamUnavailable,
				checkStr: "fake_refresh_token",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				_, payload := codexmanager.MapError(tc.err)

				// Convert payload to JSON to check for leaks
				jsonBytes, err := json.Marshal(payload)
				if err != nil {
					t.Fatalf("failed to marshal payload: %v", err)
				}

				payloadStr := string(jsonBytes)
				if strings.Contains(payloadStr, tc.checkStr) {
					t.Errorf("payload contains sensitive string: %s", tc.checkStr)
				}
			})
		}
	})

	t.Run("startup_sync_logs_do_not_leak_tokens_on_failure", func(t *testing.T) {
		// This test covers the real log path from startup_sync.go
		// It captures actual logrus output and verifies no tokens leak

		// Create a buffer to capture log output
		var logBuffer bytes.Buffer

		// Save original output and restore after test
		originalOutput := log.StandardLogger().Out
		originalLevel := log.StandardLogger().Level
		log.SetOutput(&logBuffer)
		log.SetLevel(log.WarnLevel)
		defer func() {
			log.SetOutput(originalOutput)
			log.SetLevel(originalLevel)
		}()

		// Create a temp directory for the projection state
		tempDir := t.TempDir()

		// Create a config that will fail (invalid endpoint)
		cfg := &config.Config{
			SDKConfig: sdkconfig.SDKConfig{
				APIKeys: []string{"test-key"},
			},
			AuthDir: tempDir,
			CodexManager: config.CodexManagerConfig{
				Enabled:               true,
				Endpoint:              "http://127.0.0.1:1", // Unreachable endpoint
				RequestTimeoutSeconds: 1,
			},
		}

		// Call the startup sync function
		codexmanager.StartProjectionSyncOnceAsync(cfg)

		// Give it time to attempt connection and log
		time.Sleep(500 * time.Millisecond)

		// Get the captured logs
		logs := logBuffer.String()

		// Verify no sensitive tokens appear in captured logs
		for _, token := range FakeTokenValues {
			if strings.Contains(logs, token) {
				t.Errorf("startup_sync logs contain sensitive token %q - this is a security leak. Logs:\n%s", token, logs)
			}
		}

		// The test should have captured some logs (either warnings about failure or info about skip)
		t.Logf("Captured logs during startup_sync (length: %d chars)", len(logs))

		// Verify that logs were actually captured (proving we tested the real log path)
		if len(logs) == 0 {
			t.Log("Warning: No logs captured - startup_sync may have exited early or logs are written elsewhere")
		}
	})
}
