package executor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	cliproxyusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
)

type usageCapturePlugin struct {
	records chan cliproxyusage.Record
}

func (p *usageCapturePlugin) HandleUsage(ctx context.Context, record cliproxyusage.Record) {
	select {
	case p.records <- record:
	default:
	}
}

func TestCodexExecutorExecuteImageGeneration(t *testing.T) {
	const pngBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+XgnUAAAAASUVORK5CYII="
	pngBytes := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x04, 0x00, 0x00, 0x00, 0xB5, 0x1C, 0x0C,
		0x02, 0x00, 0x00, 0x00, 0x0B, 0x49, 0x44, 0x41,
		0x54, 0x78, 0xDA, 0x63, 0xFC, 0xFF, 0x1F, 0x00,
		0x03, 0x03, 0x02, 0x00, 0xEF, 0x97, 0x82, 0x75,
		0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44,
		0xAE, 0x42, 0x60, 0x82,
	}

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/sentinel/chat-requirements":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"chat-token","arkose":{"required":false},"proofofwork":{"required":false}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/conversation/init":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/f/conversation/prepare":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"conduit_token":"conduit-token"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/f/conversation":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"conversation_id\":\"conv-1\"}\n\n"))
			_, _ = w.Write([]byte("data: {\"message\":{\"metadata\":{\"dalle\":{\"prompt\":\"revised fox prompt\"}}},\"asset_pointer\":\"file-service://file-123\"}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/files/file-123/download":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"download_url":"` + server.URL + `/download/image.png"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/download/image.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngBytes)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	originalBaseURL := codexImageChatGPTBaseURL
	codexImageChatGPTBaseURL = server.URL
	defer func() {
		codexImageChatGPTBaseURL = originalBaseURL
	}()

	usagePlugin := &usageCapturePlugin{records: make(chan cliproxyusage.Record, 8)}
	cliproxyusage.RegisterPlugin(usagePlugin)

	executor := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		ID:       "codex-auth",
		Provider: "codex",
		Status:   cliproxyauth.StatusActive,
		Metadata: map[string]any{
			"access_token": "token",
			"account_id":   "account-1",
		},
	}

	resp, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "gpt-image-2",
		Payload: []byte(`{"model":"gpt-image-2","prompt":"draw a fox"}`),
		Format:  sdktranslator.FromString("openai"),
	}, cliproxyexecutor.Options{
		Alt:          "images/generations",
		SourceFormat: sdktranslator.FromString("openai"),
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var payload struct {
		Created int64 `json:"created"`
		Data    []struct {
			B64JSON       string `json:"b64_json"`
			RevisedPrompt string `json:"revised_prompt"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Payload, &payload); err != nil {
		t.Fatalf("Unmarshal payload: %v", err)
	}
	if len(payload.Data) != 1 {
		t.Fatalf("data length = %d, want 1", len(payload.Data))
	}
	if payload.Data[0].B64JSON != pngBase64 {
		t.Fatalf("b64_json = %q", payload.Data[0].B64JSON)
	}
	if payload.Data[0].RevisedPrompt != "revised fox prompt" {
		t.Fatalf("revised_prompt = %q", payload.Data[0].RevisedPrompt)
	}
	if !strings.Contains(resp.Headers.Get("Content-Type"), "text/event-stream") && len(resp.Headers) == 0 {
		t.Fatalf("expected upstream headers to be preserved")
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case record := <-usagePlugin.records:
			if record.Model != "gpt-image-2" {
				continue
			}
			if record.Failed {
				t.Fatalf("expected successful usage record, got failed=true")
			}
			if !strings.Contains(record.InputContent, "draw a fox") {
				t.Fatalf("input content = %q, want prompt", record.InputContent)
			}
			if !strings.Contains(record.OutputContent, pngBase64) {
				t.Fatalf("output content = %q, want response payload", record.OutputContent)
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for image generation usage record")
		}
	}
}

func TestParseCodexImageRequestAcceptsExtendedGenerationOptions(t *testing.T) {
	parsed, err := parseCodexImageRequest([]byte(`{
		"model":"gpt-image-2",
		"prompt":"draw a fox",
		"size":"1792x1024",
		"quality":"high",
		"n":3
	}`))
	if err != nil {
		t.Fatalf("parseCodexImageRequest() error = %v", err)
	}
	if parsed.Model != "gpt-image-2" {
		t.Fatalf("model = %q, want gpt-image-2", parsed.Model)
	}
	if parsed.Prompt != "draw a fox" {
		t.Fatalf("prompt = %q, want draw a fox", parsed.Prompt)
	}
	if parsed.Size != "1792x1024" {
		t.Fatalf("size = %q, want 1792x1024", parsed.Size)
	}
	if parsed.Quality != "high" {
		t.Fatalf("quality = %q, want high", parsed.Quality)
	}
	if parsed.N != 3 {
		t.Fatalf("n = %d, want 3", parsed.N)
	}
}

func TestParseCodexImageRequestAcceptsImageEditsPayload(t *testing.T) {
	parsed, err := parseCodexImageRequest([]byte(`{
		"model":"gpt-image-2",
		"prompt":"turn this into a blue icon",
		"image_files":[
			{
				"file_name":"icon.png",
				"content_type":"image/png",
				"data_base64":"aGVsbG8="
			}
		]
	}`))
	if err != nil {
		t.Fatalf("parseCodexImageRequest() error = %v", err)
	}
	if len(parsed.Uploads) != 1 {
		t.Fatalf("uploads length = %d, want 1", len(parsed.Uploads))
	}
	if parsed.Uploads[0].FileName != "icon.png" {
		t.Fatalf("file name = %q, want icon.png", parsed.Uploads[0].FileName)
	}
	if parsed.Uploads[0].ContentType != "image/png" {
		t.Fatalf("content type = %q, want image/png", parsed.Uploads[0].ContentType)
	}
	if string(parsed.Uploads[0].Data) != "hello" {
		t.Fatalf("upload data = %q, want hello", string(parsed.Uploads[0].Data))
	}
}
