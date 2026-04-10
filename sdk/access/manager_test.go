package access

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestManagerAuthenticate_NoProviders_DefaultRejects(t *testing.T) {
	m := NewManager()

	req := httptest.NewRequest("POST", "/v1/responses", nil)
	_, err := m.Authenticate(context.Background(), req)
	if err == nil {
		t.Fatalf("expected auth error, got nil")
	}
	if !IsAuthErrorCode(err, AuthErrorCodeNoCredentials) {
		t.Fatalf("expected no_credentials error, got %v", err.Code)
	}
}

func TestManagerAuthenticate_NoProviders_AllowUnauthenticatedAllows(t *testing.T) {
	m := NewManager()
	m.SetAllowAllWhenNoProviders(true)

	req := httptest.NewRequest("POST", "/v1/responses", nil)
	res, err := m.Authenticate(context.Background(), req)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if res != nil {
		t.Fatalf("expected nil result, got %+v", res)
	}
}
