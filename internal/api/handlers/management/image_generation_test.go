package management

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

type managementImageExecutor struct {
	alt     string
	model   string
	payload string
	calls   int
}

func (e *managementImageExecutor) Identifier() string { return "codex" }

func (e *managementImageExecutor) Execute(ctx context.Context, auth *coreauth.Auth, req coreexecutor.Request, opts coreexecutor.Options) (coreexecutor.Response, error) {
	e.calls++
	e.alt = opts.Alt
	e.model = req.Model
	e.payload = string(req.Payload)
	return coreexecutor.Response{Payload: []byte(`{"created":1,"data":[{"b64_json":"dGVzdA=="}]}`)}, nil
}

func (e *managementImageExecutor) ExecuteStream(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	return nil, errors.New("not implemented")
}

func (e *managementImageExecutor) Refresh(ctx context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (e *managementImageExecutor) CountTokens(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, errors.New("not implemented")
}

func (e *managementImageExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, errors.New("not implemented")
}

func TestPostImageGenerationTestExecutesCodexImageAlt(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor := &managementImageExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "codex-auth",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{"access_token": "token"},
	}); err != nil {
		t.Fatalf("Register auth: %v", err)
	}

	h := &Handler{authManager: manager}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/image-generation/test", strings.NewReader(`{"prompt":"test prompt"}`))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req

	h.PostImageGenerationTest(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if executor.calls != 1 {
		t.Fatalf("executor calls = %d, want 1", executor.calls)
	}
	if executor.alt != "images/generations" {
		t.Fatalf("alt = %q, want images/generations", executor.alt)
	}
	if executor.model != "" {
		t.Fatalf("model = %q, want empty route model for direct codex selection", executor.model)
	}
	if !strings.Contains(executor.payload, "test prompt") || !strings.Contains(executor.payload, "gpt-image-2") {
		t.Fatalf("payload = %s, want prompt and model", executor.payload)
	}
	if !strings.Contains(executor.payload, `"size":"1024x1024"`) && strings.Contains(executor.payload, "size") {
		t.Fatalf("payload = %s, should only include explicit size", executor.payload)
	}
}

func TestPostImageGenerationTestForwardsGenerationOptions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor := &managementImageExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "codex-auth",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{"access_token": "token"},
	}); err != nil {
		t.Fatalf("Register auth: %v", err)
	}

	h := &Handler{authManager: manager}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/image-generation/test", strings.NewReader(`{
		"prompt":"test prompt",
		"size":"1024x1792",
		"quality":"high",
		"n":2
	}`))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req

	h.PostImageGenerationTest(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if executor.alt != "images/generations" {
		t.Fatalf("alt = %q, want images/generations", executor.alt)
	}
	for _, want := range []string{`"size":"1024x1792"`, `"quality":"high"`, `"n":2`} {
		if !strings.Contains(executor.payload, want) {
			t.Fatalf("payload = %s, want %s", executor.payload, want)
		}
	}
}

func TestPostImageGenerationTestAcceptsMultipartImageEdits(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor := &managementImageExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "codex-auth",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{"access_token": "token"},
	}); err != nil {
		t.Fatalf("Register auth: %v", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("model", "gpt-image-2")
	_ = writer.WriteField("prompt", "make it blue")
	_ = writer.WriteField("size", "1792x1024")
	_ = writer.WriteField("quality", "low")
	_ = writer.WriteField("n", "2")
	part, err := writer.CreateFormFile("image", "icon.png")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	_, _ = part.Write([]byte("hello"))
	_ = writer.Close()

	h := &Handler{authManager: manager}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/image-generation/test", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	c.Request = req

	h.PostImageGenerationTest(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if executor.alt != "images/edits" {
		t.Fatalf("alt = %q, want images/edits", executor.alt)
	}
	for _, want := range []string{`"prompt":"make it blue"`, `"file_name":"icon.png"`, `"data_base64":"aGVsbG8="`, `"n":2`} {
		if !strings.Contains(executor.payload, want) {
			t.Fatalf("payload = %s, want %s", executor.payload, want)
		}
	}
}

func TestListImageGenerationChannelsUsesCurrentChannelLabels(t *testing.T) {
	gin.SetMode(gin.TestMode)

	manager := coreauth.NewManager(nil, nil, nil)
	_, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "codex-oauth-1",
		Provider: "codex",
		Label:    "设计号 A",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{"type": "codex", "email": "a@example.com"},
	})
	if err != nil {
		t.Fatalf("Register first auth: %v", err)
	}
	_, err = manager.Register(context.Background(), &coreauth.Auth{
		ID:       "codex-oauth-2",
		Provider: "codex",
		Metadata: map[string]any{"type": "codex", "label": "设计号 B", "email": "b@example.com"},
		Status:   coreauth.StatusActive,
	})
	if err != nil {
		t.Fatalf("Register second auth: %v", err)
	}
	_, err = manager.Register(context.Background(), &coreauth.Auth{
		ID:       "gemini-oauth-1",
		Provider: "gemini-cli",
		Label:    "Gemini",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{"type": "gemini-cli"},
	})
	if err != nil {
		t.Fatalf("Register third auth: %v", err)
	}

	h := &Handler{authManager: manager}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/image-generation/channels", nil)

	h.ListImageGenerationChannels(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "设计号 A") || !strings.Contains(body, "设计号 B") {
		t.Fatalf("body = %s, want codex channel labels", body)
	}
	if strings.Contains(body, "Gemini") {
		t.Fatalf("body = %s, should not include non-codex channel", body)
	}
}
