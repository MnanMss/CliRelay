package openai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

type imageCaptureExecutor struct {
	alt      string
	model    string
	payload  string
	metadata map[string]any
	calls    int
}

func (e *imageCaptureExecutor) Identifier() string { return "codex" }

func (e *imageCaptureExecutor) Execute(ctx context.Context, auth *coreauth.Auth, req coreexecutor.Request, opts coreexecutor.Options) (coreexecutor.Response, error) {
	e.calls++
	e.alt = opts.Alt
	e.model = req.Model
	e.payload = string(req.Payload)
	e.metadata = opts.Metadata
	return coreexecutor.Response{Payload: []byte(`{"created":1,"data":[{"b64_json":"aGVsbG8="}]}`)}, nil
}

func (e *imageCaptureExecutor) ExecuteStream(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	return nil, errors.New("not implemented")
}

func (e *imageCaptureExecutor) Refresh(ctx context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (e *imageCaptureExecutor) CountTokens(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, errors.New("not implemented")
}

func (e *imageCaptureExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, errors.New("not implemented")
}

func TestOpenAIImagesGenerationsExecutesCodexImageAlt(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor := &imageCaptureExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "codex-auth",
		Provider: "codex",
		Label:    "Team Codex",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{"access_token": "token", "email": "team@example.com"},
	}); err != nil {
		t.Fatalf("Register auth: %v", err)
	}

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIImagesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/images/generations", func(c *gin.Context) {
		c.Set("accessMetadata", map[string]string{"allowed-channels": "Team Codex"})
		h.Generations(c)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"gpt-image-2","prompt":"draw a fox"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.calls != 1 {
		t.Fatalf("executor calls = %d, want 1", executor.calls)
	}
	if executor.alt != "images/generations" {
		t.Fatalf("alt = %q, want %q", executor.alt, "images/generations")
	}
	if executor.model != "" {
		t.Fatalf("model = %q, want empty route model for direct codex selection", executor.model)
	}
	if !strings.Contains(executor.payload, "draw a fox") || !strings.Contains(executor.payload, "gpt-image-2") {
		t.Fatalf("payload = %s, want prompt", executor.payload)
	}
	if executor.metadata["allowed-channels"] != "Team Codex" {
		t.Fatalf("allowed channel metadata = %#v", executor.metadata["allowed-channels"])
	}
	if strings.TrimSpace(resp.Body.String()) != `{"created":1,"data":[{"b64_json":"aGVsbG8="}]}` {
		t.Fatalf("body = %s", resp.Body.String())
	}
}

func TestOpenAIImagesGenerationsDefaultsModel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor := &imageCaptureExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "codex-auth",
		Provider: "codex",
		Label:    "Team Codex",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{"access_token": "token", "email": "team@example.com"},
	}); err != nil {
		t.Fatalf("Register auth: %v", err)
	}

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIImagesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/images/generations", h.Generations)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"prompt":"draw a fox"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.model != "" {
		t.Fatalf("model = %q, want empty route model for direct codex selection", executor.model)
	}
	if !strings.Contains(executor.payload, "gpt-image-2") {
		t.Fatalf("payload = %s, want default model in payload", executor.payload)
	}
}
