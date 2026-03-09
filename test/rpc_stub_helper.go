package test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// RPCRequestCapture represents a captured JSON-RPC request for inspection
type RPCRequestCapture struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      uint64          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	Token   string          `json:"-"`
}

// RPCStubServer is a reusable JSON-RPC stub server for Codex-Manager testing
type RPCStubServer struct {
	mu          sync.Mutex
	Server      *httptest.Server
	Calls       []RPCRequestCapture
	MethodHooks map[string]func(req RPCRequestCapture) (any, error)
	DefaultHook func(method string, req RPCRequestCapture) (any, error)
}

// NewRPCStubServer creates a new JSON-RPC stub server with default method handlers
func NewRPCStubServer(t *testing.T) *RPCStubServer {
	t.Helper()

	stub := &RPCStubServer{
		Calls:       make([]RPCRequestCapture, 0),
		MethodHooks: make(map[string]func(req RPCRequestCapture) (any, error)),
	}

	// Set up default method handlers
	stub.MethodHooks["account/list"] = func(req RPCRequestCapture) (any, error) {
		return map[string]any{"items": []any{}, "total": 0, "page": 1, "pageSize": 5}, nil
	}
	stub.MethodHooks["account/import"] = func(req RPCRequestCapture) (any, error) {
		return map[string]any{"total": 1, "created": 1, "updated": 0, "failed": 0, "errors": []any{}}, nil
	}
	stub.MethodHooks["account/delete"] = func(req RPCRequestCapture) (any, error) {
		return map[string]any{"ok": true}, nil
	}
	stub.MethodHooks["account/login/start"] = func(req RPCRequestCapture) (any, error) {
		return map[string]any{
			"authUrl":     "https://example.com/oauth/authorize",
			"loginId":     "login-1",
			"loginType":   "chatgpt",
			"issuer":      "https://auth.openai.com",
			"clientId":    "client-1",
			"redirectUri": "http://127.0.0.1:1455/auth/callback",
			"warning":     nil,
			"device":      nil,
		}, nil
	}
	stub.MethodHooks["account/login/status"] = func(req RPCRequestCapture) (any, error) {
		return map[string]any{"status": "pending", "error": nil, "updatedAt": 1700000000}, nil
	}
	stub.MethodHooks["account/login/complete"] = func(req RPCRequestCapture) (any, error) {
		return map[string]any{"ok": true}, nil
	}
	stub.MethodHooks["account/usage/read"] = func(req RPCRequestCapture) (any, error) {
		return map[string]any{"snapshot": nil}, nil
	}
	stub.MethodHooks["account/usage/list"] = func(req RPCRequestCapture) (any, error) {
		return map[string]any{"items": []any{}}, nil
	}
	stub.MethodHooks["account/usage/refresh"] = func(req RPCRequestCapture) (any, error) {
		return map[string]any{"ok": true}, nil
	}

	stub.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

		var req RPCRequestCapture
		if err := json.Unmarshal(rawBody, &req); err != nil {
			t.Fatalf("failed to decode rpc request: %v", err)
		}
		req.Token = r.Header.Get("X-CodexManager-Rpc-Token")

		stub.mu.Lock()
		stub.Calls = append(stub.Calls, req)
		stub.mu.Unlock()

		result, err := stub.handleMethod(req)
		if err != nil {
			writeRPCErrorResponse(t, w, req.ID, err)
			return
		}

		writeRPCResponse(t, w, req.ID, result)
	}))

	return stub
}

// URL returns the stub server URL
func (s *RPCStubServer) URL() string {
	return s.Server.URL
}

// Close shuts down the stub server
func (s *RPCStubServer) Close() {
	s.Server.Close()
}

// GetCalls returns a copy of all captured calls
func (s *RPCStubServer) GetCalls() []RPCRequestCapture {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]RPCRequestCapture(nil), s.Calls...)
}

// SetMethodHook overrides the handler for a specific method
func (s *RPCStubServer) SetMethodHook(method string, hook func(req RPCRequestCapture) (any, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.MethodHooks[method] = hook
}

// SetDefaultHook sets a fallback handler for unhandled methods
func (s *RPCStubServer) SetDefaultHook(hook func(method string, req RPCRequestCapture) (any, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.DefaultHook = hook
}

// ResetCalls clears the captured calls history
func (s *RPCStubServer) ResetCalls() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Calls = make([]RPCRequestCapture, 0)
}

func (s *RPCStubServer) handleMethod(req RPCRequestCapture) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if hook, ok := s.MethodHooks[req.Method]; ok {
		return hook(req)
	}

	if s.DefaultHook != nil {
		return s.DefaultHook(req.Method, req)
	}

	return map[string]any{}, nil
}

func writeRPCResponse(t *testing.T, w http.ResponseWriter, id uint64, result any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"id":     id,
		"result": result,
	}); err != nil {
		t.Fatalf("failed to encode rpc response: %v", err)
	}
}

func writeRPCErrorResponse(t *testing.T, w http.ResponseWriter, id uint64, rpcErr error) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK) // JSON-RPC errors are returned with HTTP 200

	code := -32603 // Internal error
	message := rpcErr.Error()

	if err := json.NewEncoder(w).Encode(map[string]any{
		"id": id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	}); err != nil {
		t.Fatalf("failed to encode rpc error response: %v", err)
	}
}

// MustDecodeParamsAsMap decodes JSON-RPC params as a map
func MustDecodeParamsAsMap(t *testing.T, raw json.RawMessage) map[string]any {
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

// AssertParamString asserts a string parameter value
func AssertParamString(t *testing.T, params map[string]any, key, expected string) {
	t.Helper()
	value, ok := params[key].(string)
	if !ok {
		t.Fatalf("expected %q as string, got %#v", key, params[key])
	}
	if value != expected {
		t.Fatalf("expected %q=%q, got %q", key, expected, value)
	}
}

// AssertParamBool asserts a bool parameter value
func AssertParamBool(t *testing.T, params map[string]any, key string, expected bool) {
	t.Helper()
	value, ok := params[key].(bool)
	if !ok {
		t.Fatalf("expected %q as bool, got %#v", key, params[key])
	}
	if value != expected {
		t.Fatalf("expected %q=%v, got %v", key, expected, value)
	}
}

// AssertParamNumber asserts a number parameter value
func AssertParamNumber(t *testing.T, params map[string]any, key string, expected int64) {
	t.Helper()
	value, ok := params[key].(float64)
	if !ok {
		t.Fatalf("expected %q as number, got %#v", key, params[key])
	}
	if int64(value) != expected {
		t.Fatalf("expected %q=%d, got %v", key, expected, value)
	}
}

// AssertParamNotEmpty asserts that a parameter exists and is not empty
func AssertParamNotEmpty(t *testing.T, params map[string]any, key string) {
	t.Helper()
	value, ok := params[key]
	if !ok {
		t.Fatalf("expected param %q to exist", key)
	}
	if value == nil || value == "" {
		t.Fatalf("expected param %q to be non-empty, got %#v", key, value)
	}
}
