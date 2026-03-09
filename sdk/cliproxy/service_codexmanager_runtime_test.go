package cliproxy

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/codexmanager"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

func TestCodexManagerProjectedAccountsEnterRuntimeScope(t *testing.T) {
	service, runtimeAuthID := newCodexRuntimeTestService(t)

	service.syncCodexManagerRuntimeAuths(context.Background())

	runtimeAuth, ok := service.coreManager.GetByID(runtimeAuthID)
	if !ok || runtimeAuth == nil {
		t.Fatalf("expected runtime auth %q to be registered", runtimeAuthID)
	}
	if runtimeAuth.Provider != "codex" {
		t.Fatalf("expected provider codex, got %q", runtimeAuth.Provider)
	}
	if runtimeAuth.Attributes == nil {
		t.Fatal("expected runtime auth attributes")
	}
	if runtimeAuth.Attributes["runtime_only"] != "true" {
		t.Fatalf("expected runtime_only=true, got %q", runtimeAuth.Attributes["runtime_only"])
	}
	if runtimeAuth.Attributes["priority"] != strconv.Itoa(-1000000) {
		t.Fatalf("expected explicit low priority, got %q", runtimeAuth.Attributes["priority"])
	}
	if runtimeAuth.Attributes["base_url"] == "" {
		t.Fatal("expected runtime auth base_url")
	}
	if runtimeAuth.Metadata == nil {
		t.Fatal("expected runtime auth metadata")
	}
	if accessToken, _ := runtimeAuth.Metadata["access_token"].(string); accessToken == "" {
		t.Fatal("expected runtime auth metadata access_token")
	}
}

func TestCodexManagerRuntimePrefersLocalCodexFirst(t *testing.T) {
	service, runtimeAuthID := newCodexRuntimeTestService(t)
	service.syncCodexManagerRuntimeAuths(context.Background())

	localAuth := &coreauth.Auth{
		ID:       "local-codex-auth",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Label:    "local-codex",
		Attributes: map[string]string{
			"api_key": "local-test-api-key",
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	service.applyCoreAuthAddOrUpdate(context.Background(), localAuth)

	service.coreManager.RegisterExecutor(&codexRuntimeTestExecutor{})
	selectedAuthID := ""
	_, err := service.coreManager.Execute(
		context.Background(),
		[]string{"codex"},
		cliproxyexecutor.Request{},
		cliproxyexecutor.Options{
			Metadata: map[string]any{
				cliproxyexecutor.SelectedAuthCallbackMetadataKey: func(authID string) {
					selectedAuthID = authID
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("expected local execution to succeed, got %v", err)
	}
	if selectedAuthID != localAuth.ID {
		t.Fatalf("expected local codex auth to be selected first, got %q", selectedAuthID)
	}
	if selectedAuthID == runtimeAuthID {
		t.Fatalf("expected cm runtime auth to stay behind local auth, got %q", selectedAuthID)
	}
}

func TestCodexManagerFailureIsolationFromLocalCodex(t *testing.T) {
	service, runtimeAuthID := newCodexRuntimeTestService(t)
	service.syncCodexManagerRuntimeAuths(context.Background())

	localAuth := &coreauth.Auth{
		ID:       "local-codex-auth",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Label:    "local-codex",
		Attributes: map[string]string{
			"api_key": "local-test-api-key",
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	service.applyCoreAuthAddOrUpdate(context.Background(), localAuth)

	service.coreManager.RegisterExecutor(&codexRuntimeTestExecutor{
		failByAuthID: map[string]error{
			runtimeAuthID: codexRuntimeStatusError{code: http.StatusTooManyRequests, msg: "cm-runtime-failure"},
		},
	})

	_, err := service.coreManager.Execute(
		context.Background(),
		[]string{"codex"},
		cliproxyexecutor.Request{},
		cliproxyexecutor.Options{
			Metadata: map[string]any{
				cliproxyexecutor.PinnedAuthMetadataKey: runtimeAuthID,
			},
		},
	)
	if err == nil {
		t.Fatal("expected pinned cm runtime execution to fail")
	}

	cmAuthAfterFailure, ok := service.coreManager.GetByID(runtimeAuthID)
	if !ok || cmAuthAfterFailure == nil {
		t.Fatalf("expected cm runtime auth %q to remain registered", runtimeAuthID)
	}
	if cmAuthAfterFailure.Status != coreauth.StatusError {
		t.Fatalf("expected cm runtime auth status error, got %q", cmAuthAfterFailure.Status)
	}

	localAfterFailure, ok := service.coreManager.GetByID(localAuth.ID)
	if !ok || localAfterFailure == nil {
		t.Fatalf("expected local auth %q to stay registered", localAuth.ID)
	}
	if localAfterFailure.Status != coreauth.StatusActive {
		t.Fatalf("expected local auth status active after cm failure, got %q", localAfterFailure.Status)
	}
	if localAfterFailure.Unavailable {
		t.Fatal("expected local auth to stay available after cm failure")
	}

	selectedAuthID := ""
	_, err = service.coreManager.Execute(
		context.Background(),
		[]string{"codex"},
		cliproxyexecutor.Request{},
		cliproxyexecutor.Options{
			Metadata: map[string]any{
				cliproxyexecutor.SelectedAuthCallbackMetadataKey: func(authID string) {
					selectedAuthID = authID
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("expected local auth fallback execution to succeed, got %v", err)
	}
	if selectedAuthID != localAuth.ID {
		t.Fatalf("expected fallback to local auth %q, got %q", localAuth.ID, selectedAuthID)
	}
}

func TestCodexManagerCooldownRuntimeAuthIsSkippedUntilRetryWindowExpires(t *testing.T) {
	service, runtimeAuthID := newCodexRuntimeTestService(t)
	service.syncCodexManagerRuntimeAuths(context.Background())

	localAuth := &coreauth.Auth{
		ID:       "local-codex-auth",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Label:    "local-codex",
		Attributes: map[string]string{
			"api_key": "local-test-api-key",
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	service.applyCoreAuthAddOrUpdate(context.Background(), localAuth)

	service.coreManager.RegisterExecutor(&codexRuntimeTestExecutor{
		failByAuthID: map[string]error{
			runtimeAuthID: codexRuntimeStatusError{code: http.StatusTooManyRequests, msg: "cm-runtime-cooldown"},
		},
	})

	_, err := service.coreManager.Execute(
		context.Background(),
		[]string{"codex"},
		cliproxyexecutor.Request{},
		cliproxyexecutor.Options{
			Metadata: map[string]any{
				cliproxyexecutor.PinnedAuthMetadataKey: runtimeAuthID,
			},
		},
	)
	if err == nil {
		t.Fatal("expected pinned cooldown execution to fail")
	}

	cmAuth, ok := service.coreManager.GetByID(runtimeAuthID)
	if !ok || cmAuth == nil {
		t.Fatalf("expected runtime auth %q to remain registered", runtimeAuthID)
	}
	if !cmAuth.Unavailable {
		t.Fatal("expected runtime auth to be unavailable after cooldown failure")
	}
	if cmAuth.NextRetryAfter.IsZero() || !cmAuth.NextRetryAfter.After(time.Now()) {
		t.Fatalf("expected runtime auth cooldown retry window in the future, got %v", cmAuth.NextRetryAfter)
	}

	service.coreManager.RegisterExecutor(&codexRuntimeTestExecutor{})

	selectedAuthID := ""
	_, err = service.coreManager.Execute(
		context.Background(),
		[]string{"codex"},
		cliproxyexecutor.Request{},
		cliproxyexecutor.Options{
			Metadata: map[string]any{
				cliproxyexecutor.SelectedAuthCallbackMetadataKey: func(authID string) {
					selectedAuthID = authID
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("expected non-pinned execution to fall back during cooldown, got %v", err)
	}
	if selectedAuthID != localAuth.ID {
		t.Fatalf("expected cooldown runtime auth to be skipped in favor of %q, got %q", localAuth.ID, selectedAuthID)
	}

	service.applyCoreAuthRemoval(context.Background(), localAuth.ID)
	cmRecovered := cmAuth.Clone()
	cmRecovered.NextRetryAfter = time.Now().Add(-1 * time.Second)
	service.applyCoreAuthAddOrUpdate(context.Background(), cmRecovered)
	service.coreManager.RegisterExecutor(&codexRuntimeTestExecutor{})

	selectedAuthID = ""
	_, err = service.coreManager.Execute(
		context.Background(),
		[]string{"codex"},
		cliproxyexecutor.Request{},
		cliproxyexecutor.Options{
			Metadata: map[string]any{
				cliproxyexecutor.SelectedAuthCallbackMetadataKey: func(authID string) {
					selectedAuthID = authID
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("expected cooldown-expired runtime auth to become selectable again, got %v", err)
	}
	if selectedAuthID != runtimeAuthID {
		t.Fatalf("expected runtime auth %q after cooldown expiry, got %q", runtimeAuthID, selectedAuthID)
	}
}

func newCodexRuntimeTestService(t *testing.T) (*Service, string) {
	t.Helper()

	authDir := t.TempDir()
	cfg := &config.Config{AuthDir: authDir}
	cfg.CodexManager.Enabled = true
	stateDir := codexmanager.ProjectionStateDir(cfg)
	repo, err := codexmanager.NewProjectionRepository(stateDir)
	if err != nil {
		t.Fatalf("create projection repository: %v", err)
	}
	syncedAt := time.Date(2026, time.March, 8, 12, 0, 0, 0, time.UTC)
	projection := codexmanager.ProjectionAccount{
		ProjectionID:   "cm:runtime-acc-1",
		AccountID:      "runtime-acc-1",
		ExternalRef:    "runtime-acc-1",
		Label:          "runtime-account-1",
		RelayEnabled:   true,
		Source:         codexmanager.RuntimeSourceCodexManager,
		LastSyncedAt:   syncedAt,
		SchemaVersion:  1,
		UpstreamSort:   1,
		UpstreamStatus: "active",
	}
	if _, err := repo.ApplySync([]codexmanager.ProjectionAccount{projection}, syncedAt); err != nil {
		t.Fatalf("seed projection state: %v", err)
	}

	store, err := codexmanager.NewCredentialStoreForStateDir(stateDir)
	if err != nil {
		t.Fatalf("create credential store: %v", err)
	}
	if _, err := store.UpsertBatch([]codexmanager.CredentialRecord{
		{
			AccountID:    "runtime-acc-1",
			AccessToken:  "runtime-access-token-1",
			RefreshToken: "runtime-refresh-token-1",
			Email:        "runtime-acc-1@example.com",
			BaseURL:      "https://chatgpt.com/backend-api/codex",
		},
	}, syncedAt); err != nil {
		t.Fatalf("seed credential store: %v", err)
	}

	service := &Service{
		cfg:         cfg,
		coreManager: coreauth.NewManager(nil, nil, nil),
	}
	return service, "cm:runtime-acc-1"
}

type codexRuntimeTestExecutor struct {
	failByAuthID map[string]error
}

func (e *codexRuntimeTestExecutor) Identifier() string {
	return "codex"
}

func (e *codexRuntimeTestExecutor) Execute(_ context.Context, auth *coreauth.Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	if auth != nil && e != nil && e.failByAuthID != nil {
		if err, exists := e.failByAuthID[auth.ID]; exists {
			return cliproxyexecutor.Response{}, err
		}
	}
	return cliproxyexecutor.Response{Payload: []byte(`{"ok":true}`)}, nil
}

func (e *codexRuntimeTestExecutor) ExecuteStream(_ context.Context, auth *coreauth.Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	if auth != nil && e != nil && e.failByAuthID != nil {
		if err, exists := e.failByAuthID[auth.ID]; exists {
			return nil, err
		}
	}
	chunks := make(chan cliproxyexecutor.StreamChunk)
	close(chunks)
	return &cliproxyexecutor.StreamResult{Chunks: chunks, Headers: make(http.Header)}, nil
}

func (e *codexRuntimeTestExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (e *codexRuntimeTestExecutor) CountTokens(_ context.Context, auth *coreauth.Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	if auth != nil && e != nil && e.failByAuthID != nil {
		if err, exists := e.failByAuthID[auth.ID]; exists {
			return cliproxyexecutor.Response{}, err
		}
	}
	return cliproxyexecutor.Response{Payload: []byte(`{"count":1}`)}, nil
}

func (e *codexRuntimeTestExecutor) HttpRequest(_ context.Context, auth *coreauth.Auth, _ *http.Request) (*http.Response, error) {
	if auth != nil && e != nil && e.failByAuthID != nil {
		if err, exists := e.failByAuthID[auth.ID]; exists {
			return nil, err
		}
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}"))}, nil
}

type codexRuntimeStatusError struct {
	code int
	msg  string
}

func (e codexRuntimeStatusError) Error() string {
	return e.msg
}

func (e codexRuntimeStatusError) StatusCode() int {
	return e.code
}
