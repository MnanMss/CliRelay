package test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/codexmanager"
)

type rpcRequestCapture struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      uint64          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	Token   string          `json:"-"`
}

func TestCodexManagerRPCClientMapsMethods(t *testing.T) {
	var (
		mu    sync.Mutex
		calls []rpcRequestCapture
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/rpc" {
			t.Fatalf("expected request path /rpc, got %s", r.URL.Path)
		}

		rawBody, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read request body: %v", err)
		}

		var req rpcRequestCapture
		if err := json.Unmarshal(rawBody, &req); err != nil {
			t.Fatalf("failed to decode rpc request: %v", err)
		}
		req.Token = r.Header.Get("X-CodexManager-Rpc-Token")

		mu.Lock()
		calls = append(calls, req)
		mu.Unlock()

		writeRPCResponse(t, w, req.ID, rpcResultForMethod(req.Method))
	}))
	defer server.Close()

	client, err := codexmanager.NewRPCClient(codexmanager.RPCClientConfig{
		Endpoint:       server.URL,
		RPCToken:       "rpc-token-for-test",
		RequestTimeout: 1500 * time.Millisecond,
		MaxRetries:     1,
		RetryDelay:     1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("failed to create rpc client: %v", err)
	}

	ctx := context.Background()

	_, err = client.ListAccounts(ctx, codexmanager.RPCAccountListParams{
		Page:        2,
		PageSize:    15,
		Query:       "acc",
		Filter:      "active",
		GroupFilter: "group-a",
	})
	if err != nil {
		t.Fatalf("ListAccounts failed: %v", err)
	}

	_, err = client.ImportAccounts(ctx, []string{"{\"tokens\":{}}"})
	if err != nil {
		t.Fatalf("ImportAccounts failed: %v", err)
	}

	err = client.DeleteAccount(ctx, "acc-1")
	if err != nil {
		t.Fatalf("DeleteAccount failed: %v", err)
	}

	openBrowser := false
	_, err = client.StartLogin(ctx, codexmanager.RPCLoginStartRequest{
		Type:        "chatgpt",
		OpenBrowser: &openBrowser,
		Note:        "note-1",
		Tags:        "tag-a",
		GroupName:   "group-a",
		WorkspaceID: "ws-1",
	})
	if err != nil {
		t.Fatalf("StartLogin failed: %v", err)
	}

	_, err = client.GetLoginStatus(ctx, "login-1")
	if err != nil {
		t.Fatalf("GetLoginStatus failed: %v", err)
	}

	err = client.CompleteLogin(ctx, codexmanager.RPCLoginCompleteRequest{
		State:       "state-1",
		Code:        "code-1",
		RedirectURI: "http://127.0.0.1:1455/auth/callback",
	})
	if err != nil {
		t.Fatalf("CompleteLogin failed: %v", err)
	}

	_, err = client.ReadUsage(ctx, "acc-1")
	if err != nil {
		t.Fatalf("ReadUsage failed: %v", err)
	}

	_, err = client.ListUsage(ctx)
	if err != nil {
		t.Fatalf("ListUsage failed: %v", err)
	}

	err = client.RefreshUsage(ctx, "acc-1")
	if err != nil {
		t.Fatalf("RefreshUsage failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(calls) != 9 {
		t.Fatalf("expected 9 rpc calls, got %d", len(calls))
	}

	expectedMethods := []string{
		"account/list",
		"account/import",
		"account/delete",
		"account/login/start",
		"account/login/status",
		"account/login/complete",
		"account/usage/read",
		"account/usage/list",
		"account/usage/refresh",
	}

	for i, call := range calls {
		if call.Method != expectedMethods[i] {
			t.Fatalf("call %d expected method %q, got %q", i+1, expectedMethods[i], call.Method)
		}
		if call.JSONRPC != "2.0" {
			t.Fatalf("call %d expected jsonrpc=2.0, got %q", i+1, call.JSONRPC)
		}
		if call.ID == 0 {
			t.Fatalf("call %d expected non-zero request id", i+1)
		}
		if call.ID != uint64(i+1) {
			t.Fatalf("call %d expected request id %d, got %d", i+1, i+1, call.ID)
		}
		if call.Token != "rpc-token-for-test" {
			t.Fatalf("call %d expected rpc token header to be set", i+1)
		}
	}

	listParams := mustDecodeParamsAsMap(t, calls[0].Params)
	assertParamNumber(t, listParams, "page", 2)
	assertParamNumber(t, listParams, "pageSize", 15)
	assertParamString(t, listParams, "query", "acc")
	assertParamString(t, listParams, "filter", "active")
	assertParamString(t, listParams, "groupFilter", "group-a")

	importParams := mustDecodeParamsAsMap(t, calls[1].Params)
	contentsRaw, ok := importParams["contents"].([]any)
	if !ok || len(contentsRaw) != 1 {
		t.Fatalf("expected import params.contents with one element, got %#v", importParams["contents"])
	}

	deleteParams := mustDecodeParamsAsMap(t, calls[2].Params)
	assertParamString(t, deleteParams, "accountId", "acc-1")

	loginStartParams := mustDecodeParamsAsMap(t, calls[3].Params)
	assertParamString(t, loginStartParams, "type", "chatgpt")
	assertParamBool(t, loginStartParams, "openBrowser", false)
	assertParamString(t, loginStartParams, "note", "note-1")
	assertParamString(t, loginStartParams, "tags", "tag-a")
	assertParamString(t, loginStartParams, "groupName", "group-a")
	assertParamString(t, loginStartParams, "workspaceId", "ws-1")

	loginStatusParams := mustDecodeParamsAsMap(t, calls[4].Params)
	assertParamString(t, loginStatusParams, "loginId", "login-1")

	loginCompleteParams := mustDecodeParamsAsMap(t, calls[5].Params)
	assertParamString(t, loginCompleteParams, "state", "state-1")
	assertParamString(t, loginCompleteParams, "code", "code-1")
	assertParamString(t, loginCompleteParams, "redirectUri", "http://127.0.0.1:1455/auth/callback")

	usageReadParams := mustDecodeParamsAsMap(t, calls[6].Params)
	assertParamString(t, usageReadParams, "accountId", "acc-1")

	if len(calls[7].Params) != 0 {
		t.Fatalf("expected account/usage/list to have empty params, got %s", string(calls[7].Params))
	}

	usageRefreshParams := mustDecodeParamsAsMap(t, calls[8].Params)
	assertParamString(t, usageRefreshParams, "accountId", "acc-1")
}

func TestCodexManagerRPCClientTimeoutAndErrorMapping(t *testing.T) {
	t.Run("timeout_is_mapped_to_upstream_timeout", func(t *testing.T) {
		var count atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count.Add(1)
			time.Sleep(80 * time.Millisecond)
			writeRPCResponse(t, w, 1, map[string]any{"items": []any{}, "total": 0, "page": 1, "pageSize": 5})
		}))
		defer server.Close()

		client, err := codexmanager.NewRPCClient(codexmanager.RPCClientConfig{
			Endpoint:       server.URL,
			RPCToken:       "rpc-token",
			RequestTimeout: 25 * time.Millisecond,
			MaxRetries:     1,
			RetryDelay:     1 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("failed to create rpc client: %v", err)
		}

		_, err = client.ListAccounts(context.Background(), codexmanager.RPCAccountListParams{})
		if err == nil {
			t.Fatal("expected timeout error, got nil")
		}

		status, payload := codexmanager.MapError(err)
		if status != http.StatusServiceUnavailable {
			t.Fatalf("expected status 503, got %d", status)
		}
		if payload.Code != codexmanager.CodeUpstreamTimeout {
			t.Fatalf("expected code %q, got %q", codexmanager.CodeUpstreamTimeout, payload.Code)
		}
		if !payload.Retryable {
			t.Fatalf("expected timeout mapping retryable=true")
		}
		if got := count.Load(); got != 2 {
			t.Fatalf("expected idempotent timeout call to retry once (2 attempts), got %d", got)
		}
	})

	t.Run("network_error_is_mapped_to_upstream_unavailable", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		endpoint := server.URL
		server.Close()

		client, err := codexmanager.NewRPCClient(codexmanager.RPCClientConfig{
			Endpoint:       endpoint,
			RPCToken:       "rpc-token",
			RequestTimeout: 50 * time.Millisecond,
			MaxRetries:     1,
			RetryDelay:     1 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("failed to create rpc client: %v", err)
		}

		_, err = client.ListAccounts(context.Background(), codexmanager.RPCAccountListParams{})
		if err == nil {
			t.Fatal("expected network error, got nil")
		}

		status, payload := codexmanager.MapError(err)
		if status != http.StatusServiceUnavailable {
			t.Fatalf("expected status 503, got %d", status)
		}
		if payload.Code != codexmanager.CodeUpstreamUnavailable {
			t.Fatalf("expected code %q, got %q", codexmanager.CodeUpstreamUnavailable, payload.Code)
		}
		if !payload.Retryable {
			t.Fatalf("expected upstream unavailable mapping retryable=true")
		}
	})

	t.Run("json_rpc_business_error_is_stably_mapped", func(t *testing.T) {
		var count atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count.Add(1)
			var req rpcRequestCapture
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("failed to decode request: %v", err)
			}
			writeRPCResponse(t, w, req.ID, map[string]any{"error": "account not found"})
		}))
		defer server.Close()

		client, err := codexmanager.NewRPCClient(codexmanager.RPCClientConfig{
			Endpoint:       server.URL,
			RPCToken:       "rpc-token",
			RequestTimeout: 100 * time.Millisecond,
			MaxRetries:     1,
			RetryDelay:     1 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("failed to create rpc client: %v", err)
		}

		_, err = client.ListAccounts(context.Background(), codexmanager.RPCAccountListParams{})
		if err == nil {
			t.Fatal("expected business error, got nil")
		}
		if !errors.Is(err, codexmanager.ErrAccountNotFound) {
			t.Fatalf("expected ErrAccountNotFound, got %v", err)
		}

		status, payload := codexmanager.MapError(err)
		if status != http.StatusNotFound {
			t.Fatalf("expected status 404, got %d", status)
		}
		if payload.Code != codexmanager.CodeAccountNotFound {
			t.Fatalf("expected code %q, got %q", codexmanager.CodeAccountNotFound, payload.Code)
		}
		if got := count.Load(); got != 1 {
			t.Fatalf("expected business error to avoid retries, got %d attempts", got)
		}
	})

	t.Run("retry_boundary_only_for_idempotent_calls", func(t *testing.T) {
		var listCalls atomic.Int32
		var deleteCalls atomic.Int32

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req rpcRequestCapture
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("failed to decode request: %v", err)
			}

			switch req.Method {
			case "account/list":
				if listCalls.Add(1) == 1 {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				writeRPCResponse(t, w, req.ID, map[string]any{"items": []any{}, "total": 0, "page": 1, "pageSize": 5})
			case "account/delete":
				deleteCalls.Add(1)
				w.WriteHeader(http.StatusServiceUnavailable)
			default:
				t.Fatalf("unexpected method %q", req.Method)
			}
		}))
		defer server.Close()

		client, err := codexmanager.NewRPCClient(codexmanager.RPCClientConfig{
			Endpoint:       server.URL,
			RPCToken:       "rpc-token",
			RequestTimeout: 100 * time.Millisecond,
			MaxRetries:     1,
			RetryDelay:     1 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("failed to create rpc client: %v", err)
		}

		if _, err := client.ListAccounts(context.Background(), codexmanager.RPCAccountListParams{}); err != nil {
			t.Fatalf("expected ListAccounts to succeed after retry, got %v", err)
		}

		err = client.DeleteAccount(context.Background(), "acc-delete-1")
		if err == nil {
			t.Fatal("expected DeleteAccount to fail on 5xx")
		}
		status, payload := codexmanager.MapError(err)
		if status != http.StatusServiceUnavailable {
			t.Fatalf("expected status 503 for delete failure, got %d", status)
		}
		if payload.Code != codexmanager.CodeUpstreamUnavailable {
			t.Fatalf("expected code %q, got %q", codexmanager.CodeUpstreamUnavailable, payload.Code)
		}

		if got := listCalls.Load(); got != 2 {
			t.Fatalf("expected account/list to retry once, got %d attempts", got)
		}
		if got := deleteCalls.Load(); got != 1 {
			t.Fatalf("expected account/delete to avoid retries, got %d attempts", got)
		}
	})
}


func rpcResultForMethod(method string) any {
	switch method {
	case "account/list":
		return map[string]any{"items": []any{}, "total": 0, "page": 1, "pageSize": 5}
	case "account/import":
		return map[string]any{"total": 1, "created": 1, "updated": 0, "failed": 0, "errors": []any{}}
	case "account/delete", "account/login/complete", "account/usage/refresh":
		return map[string]any{"ok": true}
	case "account/login/start":
		return map[string]any{
			"authUrl":     "https://example.com/oauth/authorize",
			"loginId":     "login-1",
			"loginType":   "chatgpt",
			"issuer":      "https://auth.openai.com",
			"clientId":    "client-1",
			"redirectUri": "http://127.0.0.1:1455/auth/callback",
			"warning":     nil,
			"device":      nil,
		}
	case "account/login/status":
		return map[string]any{"status": "pending", "error": nil, "updatedAt": 1700000000}
	case "account/usage/read":
		return map[string]any{"snapshot": nil}
	case "account/usage/list":
		return map[string]any{"items": []any{}}
	default:
		return map[string]any{}
	}
}

func mustDecodeParamsAsMap(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	if len(raw) == 0 {
		t.Fatalf("expected params payload, got empty")
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("failed to decode params: %v", err)
	}
	return out
}

func assertParamString(t *testing.T, params map[string]any, key, expected string) {
	t.Helper()
	value, ok := params[key].(string)
	if !ok {
		t.Fatalf("expected %q as string, got %#v", key, params[key])
	}
	if value != expected {
		t.Fatalf("expected %q=%q, got %q", key, expected, value)
	}
}

func assertParamBool(t *testing.T, params map[string]any, key string, expected bool) {
	t.Helper()
	value, ok := params[key].(bool)
	if !ok {
		t.Fatalf("expected %q as bool, got %#v", key, params[key])
	}
	if value != expected {
		t.Fatalf("expected %q=%v, got %v", key, expected, value)
	}
}

func assertParamNumber(t *testing.T, params map[string]any, key string, expected int64) {
	t.Helper()
	value, ok := params[key].(float64)
	if !ok {
		t.Fatalf("expected %q as number, got %#v", key, params[key])
	}
	if int64(value) != expected {
		t.Fatalf("expected %q=%d, got %v", key, expected, value)
	}
}
