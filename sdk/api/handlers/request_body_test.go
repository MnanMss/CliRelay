package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestReadJSONRequestBodyReturnsTooLargeError(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	oversized := bytes.Repeat([]byte("a"), (16<<20)+1)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(oversized))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req

	body, ok := ReadJSONRequestBody(c)
	if ok {
		t.Fatalf("expected oversized request body to fail, got ok")
	}
	if body != nil {
		t.Fatalf("expected no body on failure")
	}
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status %d, got %d", http.StatusRequestEntityTooLarge, recorder.Code)
	}

	var payload ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response body: %v", err)
	}
	if payload.Error.Code != "request_body_too_large" {
		t.Fatalf("expected request_body_too_large code, got %q", payload.Error.Code)
	}
}

func TestReadJSONRequestBodyRestoresRequestBody(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4.1"}`))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req

	body, ok := ReadJSONRequestBody(c)
	if !ok {
		t.Fatalf("expected request body to be readable")
	}
	if string(body) != `{"model":"gpt-4.1"}` {
		t.Fatalf("unexpected body: %s", string(body))
	}

	bodyAgain, ok := ReadJSONRequestBody(c)
	if !ok {
		t.Fatalf("expected restored request body to be reusable")
	}
	if string(bodyAgain) != string(body) {
		t.Fatalf("expected restored body %q, got %q", string(body), string(bodyAgain))
	}
}
