package test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/codexmanager"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestCodexManagerWriteThroughActions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	stub := NewRPCStubServer(t)
	defer stub.Close()

	stub.SetMethodHook("account/login/status", func(req RPCRequestCapture) (any, error) {
		params := MustDecodeParamsAsMap(t, req.Params)
		loginID, _ := params["loginId"].(string)
		switch strings.TrimSpace(loginID) {
		case "login-progress":
			return map[string]any{"status": "pending", "updatedAt": int64(1700000001)}, nil
		case "login-success":
			return map[string]any{"status": "success", "updatedAt": int64(1700000002)}, nil
		case "login-failed":
			return map[string]any{"status": "failed", "error": "oauth denied", "updatedAt": int64(1700000003)}, nil
		case "login-cancelled":
			return map[string]any{"status": "cancelled", "updatedAt": int64(1700000004)}, nil
		case "login-timeout":
			return map[string]any{"status": "timeout", "updatedAt": int64(1700000005)}, nil
		default:
			return map[string]any{"status": "unknown", "updatedAt": int64(1700000006)}, nil
		}
	})

	stub.SetMethodHook("account/import", func(req RPCRequestCapture) (any, error) {
		params := MustDecodeParamsAsMap(t, req.Params)
		contents, _ := params["contents"].([]any)
		total := len(contents)
		return map[string]any{"total": total, "created": total, "updated": 0, "failed": 0, "errors": []any{}}, nil
	})

	router := newCodexManagerActionRouter(t, stub)

	t.Run("write_through_calls_login_import_delete_rpc", func(t *testing.T) {
		stub.ResetCalls()

		startStatus, startPayload, startRaw := performCodexActionJSONRequest(
			t,
			router,
			http.MethodPost,
			codexmanager.ManagementNamespace+"/login/start",
			`{"type":"device","openBrowser":false,"note":"note-1","tags":"tag-1","groupName":"group-1","workspaceId":"ws-1"}`,
		)
		if startStatus != http.StatusOK {
			t.Fatalf("expected login/start status %d, got %d: %s", http.StatusOK, startStatus, startRaw)
		}
		assertBoolField(t, startPayload, "ok", true)
		startData := requireObjectField(t, startPayload, "data")
		assertStringField(t, startData, "loginId", "login-1")
		if authURL, _ := startData["authUrl"].(string); strings.TrimSpace(authURL) == "" {
			t.Fatalf("expected non-empty authUrl, got %#v", startData["authUrl"])
		}

		completeStatus, completePayload, completeRaw := performCodexActionJSONRequest(
			t,
			router,
			http.MethodPost,
			codexmanager.ManagementNamespace+"/login/complete",
			`{"state":"state-1","code":"code-1","redirectUri":"http://127.0.0.1:1455/auth/callback"}`,
		)
		if completeStatus != http.StatusOK {
			t.Fatalf("expected login/complete status %d, got %d: %s", http.StatusOK, completeStatus, completeRaw)
		}
		assertBoolField(t, completePayload, "ok", true)
		completeData := requireObjectField(t, completePayload, "data")
		assertStringField(t, completeData, "status", codexmanager.LoginFlowStatusSuccess)
		assertBoolField(t, completeData, "completed", true)

		importStatus, importPayload, importRaw := performCodexActionJSONRequest(
			t,
			router,
			http.MethodPost,
			codexmanager.ManagementNamespace+"/import",
			`{"contents":["{\"accountId\":\"acc-alpha\",\"accessToken\":\"alpha-token\",\"refreshToken\":\"alpha-refresh\",\"email\":\"alpha@example.com\"}"],"content":"{\"accountId\":\"acc-bravo\",\"accessToken\":\"bravo-token\",\"refreshToken\":\"bravo-refresh\",\"email\":\"bravo@example.com\"}"}`,
		)
		if importStatus != http.StatusOK {
			t.Fatalf("expected import status %d, got %d: %s", http.StatusOK, importStatus, importRaw)
		}
		assertBoolField(t, importPayload, "ok", true)
		importData := requireObjectField(t, importPayload, "data")
		assertNumberField(t, importData, "total", 2)
		assertNumberField(t, importData, "created", 2)
		assertNumberField(t, importData, "updated", 0)
		assertNumberField(t, importData, "failed", 0)

		deleteStatus, deletePayload, deleteRaw := performCodexActionJSONRequest(
			t,
			router,
			http.MethodDelete,
			codexmanager.ManagementNamespace+"/accounts/"+seedAccountAlpha,
			"",
		)
		if deleteStatus != http.StatusOK {
			t.Fatalf("expected delete status %d, got %d: %s", http.StatusOK, deleteStatus, deleteRaw)
		}
		assertBoolField(t, deletePayload, "ok", true)
		deleteData := requireObjectField(t, deletePayload, "data")
		assertStringField(t, deleteData, "accountId", seedAccountAlpha)
		assertBoolField(t, deleteData, "removed", true)
		assertBoolField(t, deleteData, "alreadyRemoved", false)
		assertBoolField(t, deleteData, "notFoundButHandled", false)

		calls := stub.GetCalls()
		startCall := findCallByMethod(t, calls, "account/login/start", 1)
		startParams := MustDecodeParamsAsMap(t, startCall.Params)
		AssertParamString(t, startParams, "type", "device")
		AssertParamBool(t, startParams, "openBrowser", false)
		AssertParamString(t, startParams, "note", "note-1")
		AssertParamString(t, startParams, "tags", "tag-1")
		AssertParamString(t, startParams, "groupName", "group-1")
		AssertParamString(t, startParams, "workspaceId", "ws-1")

		completeCall := findCallByMethod(t, calls, "account/login/complete", 1)
		completeParams := MustDecodeParamsAsMap(t, completeCall.Params)
		AssertParamString(t, completeParams, "state", "state-1")
		AssertParamString(t, completeParams, "code", "code-1")
		AssertParamString(t, completeParams, "redirectUri", "http://127.0.0.1:1455/auth/callback")

		importCall := findCallByMethod(t, calls, "account/import", 1)
		importParams := MustDecodeParamsAsMap(t, importCall.Params)
		contents, ok := importParams["contents"].([]any)
		if !ok || len(contents) != 2 {
			t.Fatalf("expected import.contents with 2 entries, got %#v", importParams["contents"])
		}

		deleteCall := findCallByMethod(t, calls, "account/delete", 1)
		deleteParams := MustDecodeParamsAsMap(t, deleteCall.Params)
		AssertParamString(t, deleteParams, "accountId", seedAccountAlpha)
	})

	t.Run("import_falls_back_to_local_state_when_upstream_rejects", func(t *testing.T) {
		stub.ResetCalls()
		stub.SetMethodHook("account/import", func(req RPCRequestCapture) (any, error) {
			return nil, errors.New("codex-manager upstream rejected request (HTTP 404)")
		})

		router, stateDir := newCodexManagerActionRouterWithStateDir(t, stub)
		statusCode, payload, raw := performCodexActionJSONRequest(
			t,
			router,
			http.MethodPost,
			codexmanager.ManagementNamespace+"/import",
			`{"content":"{\"accountId\":\"acc-fallback\",\"accessToken\":\"fallback-token\",\"refreshToken\":\"fallback-refresh\",\"email\":\"fallback@example.com\"}"}`,
		)
		if statusCode != http.StatusOK {
			t.Fatalf("expected fallback import status %d, got %d: %s", http.StatusOK, statusCode, raw)
		}
		assertBoolField(t, payload, "ok", true)
		data := requireObjectField(t, payload, "data")
		assertNumberField(t, data, "total", 1)
		assertNumberField(t, data, "created", 1)
		assertNumberField(t, data, "updated", 0)
		assertNumberField(t, data, "failed", 0)

		store, err := codexmanager.NewCredentialStoreForStateDir(stateDir)
		if err != nil {
			t.Fatalf("create credential store: %v", err)
		}
		record, exists := store.Get("acc-fallback")
		if !exists {
			t.Fatal("expected fallback import to persist local credential")
		}
		if record.AccessToken != "fallback-token" || record.RefreshToken != "fallback-refresh" {
			t.Fatalf("expected fallback credential tokens to persist, got %#v", record)
		}

		runtimeAuths, err := codexmanager.LoadProjectedRuntimeCodexAuths(stateDir)
		if err != nil {
			t.Fatalf("load projected runtime auths: %v", err)
		}
		foundRuntime := false
		for i := range runtimeAuths {
			if runtimeAuths[i].ID == "cm:acc-fallback" {
				foundRuntime = true
				break
			}
		}
		if !foundRuntime {
			t.Fatal("expected fallback import to project a runtime auth for clirelay")
		}

		importedAccount := readAccountFromListByID(t, router, "acc-fallback")
		assertStringField(t, importedAccount, "accountId", "acc-fallback")
		assertStringField(t, importedAccount, "label", "fallback@example.com")

		findCallByMethod(t, stub.GetCalls(), "account/import", 1)
	})

	t.Run("import_falls_back_to_local_state_without_upstream_endpoint", func(t *testing.T) {
		router, stateDir := newCodexManagerActionRouterWithoutUpstream(t)
		statusCode, payload, raw := performCodexActionJSONRequest(
			t,
			router,
			http.MethodPost,
			codexmanager.ManagementNamespace+"/import",
			`{"content":"{\"accountId\":\"acc-local-only\",\"accessToken\":\"local-token\",\"refreshToken\":\"local-refresh\",\"email\":\"local@example.com\"}"}`,
		)
		if statusCode != http.StatusOK {
			t.Fatalf("expected local-only import status %d, got %d: %s", http.StatusOK, statusCode, raw)
		}
		assertBoolField(t, payload, "ok", true)
		data := requireObjectField(t, payload, "data")
		assertNumberField(t, data, "total", 1)
		assertNumberField(t, data, "created", 1)
		assertNumberField(t, data, "updated", 0)
		assertNumberField(t, data, "failed", 0)

		store, err := codexmanager.NewCredentialStoreForStateDir(stateDir)
		if err != nil {
			t.Fatalf("create credential store: %v", err)
		}
		if record, exists := store.Get("acc-local-only"); !exists {
			t.Fatal("expected local-only import to persist local credential")
		} else if record.AccessToken != "local-token" || record.RefreshToken != "local-refresh" {
			t.Fatalf("expected local-only credential tokens to persist, got %#v", record)
		}

		runtimeAuths, err := codexmanager.LoadProjectedRuntimeCodexAuths(stateDir)
		if err != nil {
			t.Fatalf("load projected runtime auths: %v", err)
		}
		foundRuntime := false
		for i := range runtimeAuths {
			if runtimeAuths[i].ID == "cm:acc-local-only" {
				foundRuntime = true
				break
			}
		}
		if !foundRuntime {
			t.Fatal("expected local-only import to project a runtime auth for clirelay")
		}
	})

	t.Run("login_status_flow_covers_terminal_states", func(t *testing.T) {
		cases := []struct {
			loginID        string
			expectedStatus string
			terminal       bool
			errorMessage   string
		}{
			{loginID: "login-progress", expectedStatus: codexmanager.LoginFlowStatusInProgress, terminal: false},
			{loginID: "login-success", expectedStatus: codexmanager.LoginFlowStatusSuccess, terminal: true},
			{loginID: "login-failed", expectedStatus: codexmanager.LoginFlowStatusFailed, terminal: true, errorMessage: "oauth denied"},
			{loginID: "login-cancelled", expectedStatus: codexmanager.LoginFlowStatusCancelled, terminal: true},
			{loginID: "login-timeout", expectedStatus: codexmanager.LoginFlowStatusTimedOut, terminal: true},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.loginID, func(t *testing.T) {
				statusCode, payload, raw := performCodexActionJSONRequest(
					t,
					router,
					http.MethodGet,
					codexmanager.ManagementNamespace+"/login/status/"+tc.loginID,
					"",
				)
				if statusCode != http.StatusOK {
					t.Fatalf("expected login/status status %d, got %d: %s", http.StatusOK, statusCode, raw)
				}

				data := requireObjectField(t, payload, "data")
				assertStringField(t, data, "loginId", tc.loginID)
				assertStringField(t, data, "status", tc.expectedStatus)
				assertBoolField(t, data, "terminal", tc.terminal)
				if updatedAt, ok := data["updatedAt"].(string); !ok || strings.TrimSpace(updatedAt) == "" {
					t.Fatalf("expected login status updatedAt timestamp, got %#v", data["updatedAt"])
				}

				if tc.errorMessage == "" {
					if value, exists := data["error"]; !exists || value != nil {
						t.Fatalf("expected error=null, got %#v", data["error"])
					}
				} else {
					assertStringField(t, data, "error", tc.errorMessage)
				}
			})
		}
	})

	t.Run("rejects_bad_payload_and_bad_login_id", func(t *testing.T) {
		statusCode, payload, raw := performCodexActionJSONRequest(
			t,
			router,
			http.MethodPost,
			codexmanager.ManagementNamespace+"/login/start",
			"{",
		)
		if statusCode != http.StatusBadRequest {
			t.Fatalf("expected invalid login/start payload status %d, got %d: %s", http.StatusBadRequest, statusCode, raw)
		}
		assertBoolField(t, payload, "ok", false)
		assertStringField(t, payload, "code", codexmanager.CodeBadRequest)

		statusCode, payload, raw = performCodexActionJSONRequest(
			t,
			router,
			http.MethodPost,
			codexmanager.ManagementNamespace+"/login/complete",
			`{"state":"","code":""}`,
		)
		if statusCode != http.StatusBadRequest {
			t.Fatalf("expected invalid login/complete payload status %d, got %d: %s", http.StatusBadRequest, statusCode, raw)
		}
		assertBoolField(t, payload, "ok", false)
		assertStringField(t, payload, "code", codexmanager.CodeBadRequest)

		statusCode, payload, raw = performCodexActionJSONRequest(
			t,
			router,
			http.MethodPost,
			codexmanager.ManagementNamespace+"/import",
			`{"contents":[]}`,
		)
		if statusCode != http.StatusBadRequest {
			t.Fatalf("expected invalid import payload status %d, got %d: %s", http.StatusBadRequest, statusCode, raw)
		}
		assertBoolField(t, payload, "ok", false)
		assertStringField(t, payload, "code", codexmanager.CodeBadRequest)

		statusCode, payload, raw = performCodexActionJSONRequest(
			t,
			router,
			http.MethodPost,
			codexmanager.ManagementNamespace+"/import",
			`{"content":"{\"auth\":1}"}`,
		)
		if statusCode != http.StatusInternalServerError {
			t.Fatalf("expected no-executable-credentials import status %d, got %d: %s", http.StatusInternalServerError, statusCode, raw)
		}
		assertBoolField(t, payload, "ok", false)
		assertStringField(t, payload, "code", codexmanager.CodeInternalError)
		assertStringField(t, payload, "message", "codex-manager import succeeded but import contents produced no executable local credentials")

		statusCode, payload, raw = performCodexActionJSONRequest(
			t,
			router,
			http.MethodGet,
			codexmanager.ManagementNamespace+"/login/status/%20",
			"",
		)
		if statusCode != http.StatusBadRequest {
			t.Fatalf("expected invalid loginId status %d, got %d: %s", http.StatusBadRequest, statusCode, raw)
		}
		assertBoolField(t, payload, "ok", false)
		assertStringField(t, payload, "code", codexmanager.CodeBadRequest)
	})
}

func TestCodexManagerRelayStateOverlay(t *testing.T) {
	gin.SetMode(gin.TestMode)

	stub := NewRPCStubServer(t)
	defer stub.Close()

	router := newCodexManagerActionRouter(t, stub)

	t.Run("relay_state_patch_updates_overlay_and_runtime_included_immediately", func(t *testing.T) {
		stub.ResetCalls()

		before := readAccountFromListByID(t, router, seedAccountAlpha)
		assertBoolField(t, before, "relayEnabled", true)
		assertBoolField(t, before, "runtimeIncluded", true)

		patchStatus, patchPayload, patchRaw := performCodexActionJSONRequest(
			t,
			router,
			http.MethodPatch,
			codexmanager.ManagementNamespace+"/accounts/"+seedAccountAlpha+"/relay-state",
			`{"state":"disabled"}`,
		)
		if patchStatus != http.StatusOK {
			t.Fatalf("expected relay-state patch status %d, got %d: %s", http.StatusOK, patchStatus, patchRaw)
		}

		patched := requireObjectField(t, patchPayload, "data")
		assertStringField(t, patched, "accountId", seedAccountAlpha)
		assertStringField(t, patched, "status", "active")
		assertBoolField(t, patched, "relayEnabled", false)
		assertBoolField(t, patched, "runtimeIncluded", false)

		afterDisable := readAccountFromListByID(t, router, seedAccountAlpha)
		assertBoolField(t, afterDisable, "relayEnabled", false)
		assertBoolField(t, afterDisable, "runtimeIncluded", false)

		patchStatus, patchPayload, patchRaw = performCodexActionJSONRequest(
			t,
			router,
			http.MethodPatch,
			codexmanager.ManagementNamespace+"/accounts/"+seedAccountAlpha+"/relay-state",
			`{"state":true}`,
		)
		if patchStatus != http.StatusOK {
			t.Fatalf("expected relay-state re-enable status %d, got %d: %s", http.StatusOK, patchStatus, patchRaw)
		}
		patched = requireObjectField(t, patchPayload, "data")
		assertBoolField(t, patched, "relayEnabled", true)
		assertBoolField(t, patched, "runtimeIncluded", true)

		afterEnable := readAccountFromListByID(t, router, seedAccountAlpha)
		assertBoolField(t, afterEnable, "relayEnabled", true)
		assertBoolField(t, afterEnable, "runtimeIncluded", true)

		if calls := stub.GetCalls(); len(calls) != 0 {
			t.Fatalf("expected relay-state/list flow to avoid rpc calls, got %d call(s)", len(calls))
		}
	})

	t.Run("runtime_included_stays_false_for_tombstone_even_if_enabled", func(t *testing.T) {
		statusCode, payload, raw := performCodexActionJSONRequest(
			t,
			router,
			http.MethodPatch,
			codexmanager.ManagementNamespace+"/accounts/"+seedAccountCharlie+"/relay-state",
			`{"state":"enabled"}`,
		)
		if statusCode != http.StatusOK {
			t.Fatalf("expected tombstone relay-state patch status %d, got %d: %s", http.StatusOK, statusCode, raw)
		}

		data := requireObjectField(t, payload, "data")
		assertStringField(t, data, "accountId", seedAccountCharlie)
		assertBoolField(t, data, "relayEnabled", true)
		assertBoolField(t, data, "stale", true)
		assertBoolField(t, data, "runtimeIncluded", false)
	})

	t.Run("rejects_missing_account_and_bad_state_payload", func(t *testing.T) {
		statusCode, payload, raw := performCodexActionJSONRequest(
			t,
			router,
			http.MethodPatch,
			codexmanager.ManagementNamespace+"/accounts/acc-missing/relay-state",
			`{"state":true}`,
		)
		if statusCode != http.StatusNotFound {
			t.Fatalf("expected missing account status %d, got %d: %s", http.StatusNotFound, statusCode, raw)
		}
		assertBoolField(t, payload, "ok", false)
		assertStringField(t, payload, "code", codexmanager.CodeAccountNotFound)

		statusCode, payload, raw = performCodexActionJSONRequest(
			t,
			router,
			http.MethodPatch,
			codexmanager.ManagementNamespace+"/accounts/"+seedAccountAlpha+"/relay-state",
			`{"state":"not-a-valid-state"}`,
		)
		if statusCode != http.StatusBadRequest {
			t.Fatalf("expected invalid relay-state status %d, got %d: %s", http.StatusBadRequest, statusCode, raw)
		}
		assertBoolField(t, payload, "ok", false)
		assertStringField(t, payload, "code", codexmanager.CodeBadRequest)
	})
}

func TestCodexManagerDeleteAndImportIdempotent(t *testing.T) {
	gin.SetMode(gin.TestMode)

	stub := NewRPCStubServer(t)
	defer stub.Close()

	deleteCalls := 0
	stub.SetMethodHook("account/delete", func(req RPCRequestCapture) (any, error) {
		deleteCalls++
		if deleteCalls == 1 {
			return map[string]any{"ok": true}, nil
		}
		return nil, errors.New("account not found")
	})

	importCalls := 0
	stub.SetMethodHook("account/import", func(req RPCRequestCapture) (any, error) {
		importCalls++
		params := MustDecodeParamsAsMap(t, req.Params)
		contents, _ := params["contents"].([]any)
		total := len(contents)
		if importCalls == 1 {
			return map[string]any{"total": total, "created": total, "updated": 0, "failed": 0, "errors": []any{}}, nil
		}
		return map[string]any{"total": total, "created": 0, "updated": total, "failed": 0, "errors": []any{}}, nil
	})

	router := newCodexManagerActionRouter(t, stub)

	t.Run("repeated_import_returns_stable_summary_without_500", func(t *testing.T) {
		stub.ResetCalls()

		statusCode, payload, raw := performCodexActionJSONRequest(
			t,
			router,
			http.MethodPost,
			codexmanager.ManagementNamespace+"/import",
			`{"content":"{\"accountId\":\"acc-alpha\",\"accessToken\":\"alpha-token\",\"refreshToken\":\"alpha-refresh\",\"email\":\"alpha@example.com\"}"}`,
		)
		if statusCode != http.StatusOK {
			t.Fatalf("expected first import status %d, got %d: %s", http.StatusOK, statusCode, raw)
		}
		first := requireObjectField(t, payload, "data")
		assertNumberField(t, first, "created", 1)
		assertNumberField(t, first, "updated", 0)

		statusCode, payload, raw = performCodexActionJSONRequest(
			t,
			router,
			http.MethodPost,
			codexmanager.ManagementNamespace+"/import",
			`{"content":"{\"accountId\":\"acc-alpha\",\"accessToken\":\"alpha-token\",\"refreshToken\":\"alpha-refresh\",\"email\":\"alpha@example.com\"}"}`,
		)
		if statusCode != http.StatusOK {
			t.Fatalf("expected second import status %d, got %d: %s", http.StatusOK, statusCode, raw)
		}
		second := requireObjectField(t, payload, "data")
		assertNumberField(t, second, "created", 0)
		assertNumberField(t, second, "updated", 1)
		assertNumberField(t, second, "failed", 0)

		calls := stub.GetCalls()
		if got := countCallsByMethod(calls, "account/import"); got != 2 {
			t.Fatalf("expected 2 account/import calls in import subtest, got %d", got)
		}
	})

	t.Run("repeated_delete_is_idempotent_and_never_500", func(t *testing.T) {
		stub.ResetCalls()

		statusCode, payload, raw := performCodexActionJSONRequest(
			t,
			router,
			http.MethodDelete,
			codexmanager.ManagementNamespace+"/accounts/"+seedAccountBravo,
			"",
		)
		if statusCode != http.StatusOK {
			t.Fatalf("expected first delete status %d, got %d: %s", http.StatusOK, statusCode, raw)
		}
		first := requireObjectField(t, payload, "data")
		assertBoolField(t, first, "removed", true)
		assertBoolField(t, first, "alreadyRemoved", false)
		assertBoolField(t, first, "notFoundButHandled", false)

		statusCode, payload, raw = performCodexActionJSONRequest(
			t,
			router,
			http.MethodDelete,
			codexmanager.ManagementNamespace+"/accounts/"+seedAccountBravo,
			"",
		)
		if statusCode != http.StatusOK {
			t.Fatalf("expected second delete status %d, got %d: %s", http.StatusOK, statusCode, raw)
		}
		second := requireObjectField(t, payload, "data")
		assertBoolField(t, second, "removed", false)
		assertBoolField(t, second, "alreadyRemoved", true)
		assertBoolField(t, second, "notFoundButHandled", true)

		calls := stub.GetCalls()
		if got := countCallsByMethod(calls, "account/delete"); got != 2 {
			t.Fatalf("expected 2 account/delete calls in delete subtest, got %d", got)
		}
	})

	t.Run("delete_rejects_blank_account_id", func(t *testing.T) {
		statusCode, payload, raw := performCodexActionJSONRequest(
			t,
			router,
			http.MethodDelete,
			codexmanager.ManagementNamespace+"/accounts/%20",
			"",
		)
		if statusCode != http.StatusBadRequest {
			t.Fatalf("expected blank account id status %d, got %d: %s", http.StatusBadRequest, statusCode, raw)
		}
		assertBoolField(t, payload, "ok", false)
		assertStringField(t, payload, "code", codexmanager.CodeBadRequest)
	})
}

func TestCodexManagerImportClosesRuntimeAuthLoop(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const importedAccountID = "acc-import-fresh"
	const importedEmail = "fresh@example.com"

	stub := NewRPCStubServer(t)
	defer stub.Close()

	stub.SetMethodHook("account/import", func(req RPCRequestCapture) (any, error) {
		params := MustDecodeParamsAsMap(t, req.Params)
		contents, _ := params["contents"].([]any)
		total := len(contents)
		return map[string]any{"total": total, "created": total, "updated": 0, "failed": 0, "errors": []any{}}, nil
	})

	router, stateDir := newCodexManagerActionRouterWithStateDir(t, stub)

	statusCode, payload, raw := performCodexActionJSONRequest(
		t,
		router,
		http.MethodPost,
		codexmanager.ManagementNamespace+"/import",
		`{"content":"{\"accountId\":\"acc-import-fresh\",\"accessToken\":\"fresh-token\",\"refreshToken\":\"fresh-refresh\",\"email\":\"fresh@example.com\"}"}`,
	)
	if statusCode != http.StatusOK {
		t.Fatalf("expected import status %d, got %d: %s", http.StatusOK, statusCode, raw)
	}
	assertBoolField(t, payload, "ok", true)

	projected := readAccountFromListByID(t, router, importedAccountID)
	assertStringField(t, projected, "accountId", importedAccountID)
	assertStringField(t, projected, "label", importedEmail)
	assertStringField(t, projected, "runtimeSource", codexmanager.RuntimeSourceCodexManager)
	assertBoolField(t, projected, "relayEnabled", true)
	assertBoolField(t, projected, "runtimeIncluded", true)
	assertBoolField(t, projected, "stale", false)

	auths, err := codexmanager.LoadProjectedRuntimeCodexAuths(stateDir)
	if err != nil {
		t.Fatalf("load projected runtime codex auths: %v", err)
	}
	if len(auths) != 1 {
		t.Fatalf("expected 1 runtime auth after import, got %d", len(auths))
	}
	auth := auths[0]
	if auth.ID != "cm:"+importedAccountID {
		t.Fatalf("expected runtime auth id cm:%s, got %q", importedAccountID, auth.ID)
	}
	if auth.Provider != "codex" {
		t.Fatalf("expected runtime auth provider codex, got %q", auth.Provider)
	}
	if auth.Attributes["auth_kind"] != "oauth" {
		t.Fatalf("expected runtime auth kind oauth, got %q", auth.Attributes["auth_kind"])
	}
	if accessToken, _ := auth.Metadata["access_token"].(string); accessToken != "fresh-token" {
		t.Fatalf("expected runtime auth access token fresh-token, got %q", accessToken)
	}
}

func TestCodexManagerExportAccountsReturnsBrowserDownloadZip(t *testing.T) {
	gin.SetMode(gin.TestMode)

	stub := NewRPCStubServer(t)
	defer stub.Close()

	router, stateDir := newCodexManagerActionRouterWithStateDir(t, stub)
	seedCodexManagerCredentials(t, stateDir, []codexmanager.CredentialRecord{
		{
			AccountID:        seedAccountAlpha,
			AccessToken:      "alpha-export-token",
			RefreshToken:     "alpha-export-refresh",
			IDToken:          "alpha-export-id",
			Email:            "alpha-export@example.com",
			WorkspaceID:      "ws-alpha",
			ChatGPTAccountID: "chatgpt-alpha",
		},
		{
			AccountID: seedAccountBravo,
			APIKey:    "bravo-api-key",
			BaseURL:   "https://chatgpt.com/backend-api/codex",
			ProxyURL:  "https://proxy.example.com",
			Prefix:    "team-bravo",
		},
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, codexmanager.ManagementNamespace+"/export", nil)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected export status %d, got %d: %s", http.StatusOK, recorder.Code, recorder.Body.String())
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, "application/zip") {
		t.Fatalf("expected application/zip content type, got %q", contentType)
	}
	if disposition := recorder.Header().Get("Content-Disposition"); !strings.Contains(disposition, "attachment;") || !strings.Contains(disposition, ".zip") {
		t.Fatalf("expected attachment zip content disposition, got %q", disposition)
	}

	zipReader, err := zip.NewReader(bytes.NewReader(recorder.Body.Bytes()), int64(recorder.Body.Len()))
	if err != nil {
		t.Fatalf("read export zip: %v", err)
	}
	if len(zipReader.File) != 2 {
		t.Fatalf("expected 2 export files, got %d", len(zipReader.File))
	}

	exportedContents := make([]string, 0, len(zipReader.File))
	seenLegacyShape := false
	for i := range zipReader.File {
		file := zipReader.File[i]
		if !strings.HasSuffix(file.Name, ".json") {
			t.Fatalf("expected export file to be json, got %q", file.Name)
		}
		rc, err := file.Open()
		if err != nil {
			t.Fatalf("open export file %q: %v", file.Name, err)
		}
		raw, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read export file %q: %v", file.Name, err)
		}
		exportedContents = append(exportedContents, string(raw))

		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("decode export json %q: %v", file.Name, err)
		}
		if _, ok := payload["tokens"].(map[string]any); !ok {
			t.Fatalf("expected export payload %q to include tokens object", file.Name)
		}
		meta, ok := payload["meta"].(map[string]any)
		if !ok {
			t.Fatalf("expected export payload %q to include meta object", file.Name)
		}
		if _, hasLabel := meta["label"]; hasLabel {
			seenLegacyShape = true
		}
	}
	if !seenLegacyShape {
		t.Fatal("expected at least one export entry to include legacy meta payload fields")
	}

	records := codexmanager.ParseCredentialRecords(exportedContents)
	if len(records) != 2 {
		t.Fatalf("expected 2 credential records from exported zip, got %d", len(records))
	}
	recordByAccount := make(map[string]codexmanager.CredentialRecord, len(records))
	for i := range records {
		recordByAccount[records[i].AccountID] = records[i]
	}
	alpha, ok := recordByAccount[seedAccountAlpha]
	if !ok {
		t.Fatalf("expected exported alpha credential record")
	}
	if alpha.AccessToken != "alpha-export-token" || alpha.RefreshToken != "alpha-export-refresh" || alpha.IDToken != "alpha-export-id" {
		t.Fatalf("expected alpha oauth tokens to round-trip, got %#v", alpha)
	}
	if alpha.WorkspaceID != "ws-alpha" || alpha.ChatGPTAccountID != "chatgpt-alpha" || alpha.Email != "alpha-export@example.com" {
		t.Fatalf("expected alpha meta fields to round-trip, got %#v", alpha)
	}
	bravo, ok := recordByAccount[seedAccountBravo]
	if !ok {
		t.Fatalf("expected exported bravo credential record")
	}
	if bravo.APIKey != "bravo-api-key" || bravo.BaseURL != "https://chatgpt.com/backend-api/codex" {
		t.Fatalf("expected bravo api-key fields to round-trip, got %#v", bravo)
	}
	if bravo.ProxyURL != "https://proxy.example.com" || bravo.Prefix != "team-bravo" {
		t.Fatalf("expected bravo relay fields to round-trip, got %#v", bravo)
	}
	if calls := stub.GetCalls(); len(calls) != 0 {
		t.Fatalf("expected export to avoid upstream rpc calls, got %d call(s)", len(calls))
	}
}

func TestCodexManagerDeleteUnavailableFreeAccountsCleansLocalState(t *testing.T) {
	gin.SetMode(gin.TestMode)

	stub := NewRPCStubServer(t)
	defer stub.Close()

	stub.SetMethodHook("account/deleteUnavailableFree", func(req RPCRequestCapture) (any, error) {
		return map[string]any{
			"scanned":             3,
			"deleted":             1,
			"skippedAvailable":    1,
			"skippedNonFree":      1,
			"skippedMissingUsage": 0,
			"skippedMissingToken": 0,
			"deletedAccountIds":   []string{seedAccountAlpha},
		}, nil
	})

	router, stateDir := newCodexManagerActionRouterWithStateDir(t, stub)
	seedCodexManagerCredentials(t, stateDir, []codexmanager.CredentialRecord{
		{AccountID: seedAccountAlpha, AccessToken: "alpha-token", RefreshToken: "alpha-refresh", Email: "alpha@example.com"},
		{AccountID: seedAccountBravo, AccessToken: "bravo-token", RefreshToken: "bravo-refresh", Email: "bravo@example.com"},
	})

	statusCode, payload, raw := performCodexActionJSONRequest(
		t,
		router,
		http.MethodPost,
		codexmanager.ManagementNamespace+"/accounts/free/delete-unavailable",
		"",
	)
	if statusCode != http.StatusOK {
		t.Fatalf("expected delete-unavailable status %d, got %d: %s", http.StatusOK, statusCode, raw)
	}
	assertBoolField(t, payload, "ok", true)
	data := requireObjectField(t, payload, "data")
	assertNumberField(t, data, "scanned", 3)
	assertNumberField(t, data, "deleted", 1)
	assertNumberField(t, data, "skippedAvailable", 1)
	assertNumberField(t, data, "skippedNonFree", 1)
	assertNumberField(t, data, "localCredentialsRemoved", 1)
	assertNumberField(t, data, "localProjectionsTombstoned", 1)
	deletedIDs := requireArrayField(t, data, "deletedAccountIds")
	if len(deletedIDs) != 1 || deletedIDs[0] != seedAccountAlpha {
		t.Fatalf("expected deletedAccountIds [%q], got %#v", seedAccountAlpha, deletedIDs)
	}

	store, err := codexmanager.NewCredentialStoreForStateDir(stateDir)
	if err != nil {
		t.Fatalf("create credential store: %v", err)
	}
	if _, exists := store.Get(seedAccountAlpha); exists {
		t.Fatalf("expected local credential %q to be removed", seedAccountAlpha)
	}
	if _, exists := store.Get(seedAccountBravo); !exists {
		t.Fatalf("expected local credential %q to remain", seedAccountBravo)
	}

	alphaAccount := readAccountFromListByID(t, router, seedAccountAlpha)
	assertBoolField(t, alphaAccount, "stale", true)
	assertBoolField(t, alphaAccount, "runtimeIncluded", false)

	bravoAccount := readAccountFromListByID(t, router, seedAccountBravo)
	assertBoolField(t, bravoAccount, "stale", false)

	deleteUnavailableCall := findCallByMethod(t, stub.GetCalls(), "account/deleteUnavailableFree", 1)
	if len(deleteUnavailableCall.Params) != 0 && string(deleteUnavailableCall.Params) != "null" {
		t.Fatalf("expected deleteUnavailableFree params to be empty or null, got %s", string(deleteUnavailableCall.Params))
	}
}

func newCodexManagerActionRouter(t *testing.T, stub *RPCStubServer) *gin.Engine {
	router, _ := newCodexManagerActionRouterWithStateDir(t, stub)
	return router
}

func newCodexManagerActionRouterWithStateDir(t *testing.T, stub *RPCStubServer) (*gin.Engine, string) {
	t.Helper()

	cfg := &config.Config{
		AuthDir: t.TempDir(),
		CodexManager: config.CodexManagerConfig{
			Enabled:               true,
			RequestTimeoutSeconds: 1,
		},
	}
	if stub != nil {
		cfg.CodexManager.Endpoint = stub.URL()
	}
	seedCodexManagerProjectionState(t, cfg)
	stateDir := codexmanager.ProjectionStateDir(cfg)

	handler := codexmanager.NewHandler(cfg)
	router := gin.New()
	routes := router.Group(codexmanager.ManagementNamespace)
	routes.GET("/accounts", handler.ListAccounts)
	routes.GET("/usage", handler.ListUsage)
	routes.GET("/export", handler.ExportAccounts)
	routes.GET("/accounts/:accountId", handler.GetAccount)
	routes.GET("/accounts/:accountId/usage", handler.GetAccountUsage)
	routes.POST("/login/start", handler.StartLogin)
	routes.GET("/login/status/:loginId", handler.GetLoginStatus)
	routes.POST("/login/complete", handler.CompleteLogin)
	routes.POST("/import", handler.ImportAccounts)
	routes.DELETE("/accounts/:accountId", handler.DeleteAccount)
	routes.PATCH("/accounts/:accountId/relay-state", handler.PatchRelayState)
	routes.POST("/accounts/:accountId/usage/refresh", handler.RefreshAccountUsage)
	routes.POST("/accounts/free/delete-unavailable", handler.DeleteUnavailableFreeAccounts)
	routes.POST("/usage/refresh-batch", handler.RefreshUsageBatch)

	return router, stateDir
}

func newCodexManagerActionRouterWithoutUpstream(t *testing.T) (*gin.Engine, string) {
	return newCodexManagerActionRouterWithStateDir(t, nil)
}

func seedCodexManagerCredentials(t *testing.T, stateDir string, records []codexmanager.CredentialRecord) {
	t.Helper()

	store, err := codexmanager.NewCredentialStoreForStateDir(stateDir)
	if err != nil {
		t.Fatalf("create credential store: %v", err)
	}
	if _, err := store.UpsertBatch(records, time.Date(2026, time.March, 9, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seed credential store: %v", err)
	}
}

func performCodexActionJSONRequest(t *testing.T, router http.Handler, method, path, body string) (int, map[string]any, string) {
	t.Helper()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if strings.TrimSpace(body) != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	router.ServeHTTP(recorder, request)

	raw := recorder.Body.String()
	payload := map[string]any{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		return recorder.Code, nil, raw
	}
	return recorder.Code, payload, raw
}

func readAccountFromListByID(t *testing.T, router http.Handler, accountID string) map[string]any {
	t.Helper()

	statusCode, payload, raw := performCodexActionJSONRequest(
		t,
		router,
		http.MethodGet,
		codexmanager.ManagementNamespace+"/accounts?page=1&pageSize=20",
		"",
	)
	if statusCode != http.StatusOK {
		t.Fatalf("expected list status %d, got %d: %s", http.StatusOK, statusCode, raw)
	}

	data := requireObjectField(t, payload, "data")
	items := requireArrayField(t, data, "items")
	for i := range items {
		candidate := requireObjectValue(t, items[i], "list account candidate")
		if id, _ := candidate["accountId"].(string); id == accountID {
			return candidate
		}
	}
	t.Fatalf("account %q not found in list payload", accountID)
	return nil
}

func findCallByMethod(t *testing.T, calls []RPCRequestCapture, method string, occurrence int) RPCRequestCapture {
	t.Helper()
	if occurrence < 1 {
		occurrence = 1
	}
	count := 0
	for i := range calls {
		if calls[i].Method == method {
			count++
			if count == occurrence {
				return calls[i]
			}
		}
	}
	t.Fatalf("expected method %q occurrence %d in %d call(s)", method, occurrence, len(calls))
	return RPCRequestCapture{}
}

func countCallsByMethod(calls []RPCRequestCapture, method string) int {
	count := 0
	for i := range calls {
		if calls[i].Method == method {
			count++
		}
	}
	return count
}
