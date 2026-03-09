package test

import (
	"strconv"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/codexmanager"
)

func TestCodexManagerBuildProjectedRuntimeCodexAuthsOAuthMapping(t *testing.T) {
	syncedAt := time.Date(2026, time.March, 8, 15, 0, 0, 0, time.UTC)
	projected := []codexmanager.ProjectionAccount{
		{
			ProjectionID: "cm:acc-oauth",
			AccountID:    "acc-oauth",
			ExternalRef:  "acc-oauth",
			Label:        "oauth-account",
			RelayEnabled: true,
			Source:       codexmanager.RuntimeSourceCodexManager,
			LastSyncedAt: syncedAt,
			UpstreamSort: 7,
		},
	}
	credentials := []codexmanager.CredentialRecord{
		{
			AccountID:    "acc-oauth",
			AccountRef:   "chatgpt-oauth-account",
			AccessToken:  "oauth-access-token",
			RefreshToken: "oauth-refresh-token",
			Email:        "oauth@example.com",
			UpdatedAt:    syncedAt,
		},
	}

	auths := codexmanager.BuildProjectedRuntimeCodexAuths(projected, credentials)
	if len(auths) != 1 {
		t.Fatalf("expected 1 runtime auth, got %d", len(auths))
	}
	auth := auths[0]
	if auth.Provider != "codex" {
		t.Fatalf("expected provider codex, got %q", auth.Provider)
	}
	if auth.ID != "cm:acc-oauth" {
		t.Fatalf("expected stable id cm:acc-oauth, got %q", auth.ID)
	}
	if auth.Attributes["runtime_only"] != "true" {
		t.Fatalf("expected runtime_only=true, got %q", auth.Attributes["runtime_only"])
	}
	priority, err := strconv.Atoi(auth.Attributes["priority"])
	if err != nil {
		t.Fatalf("expected numeric priority, got %q", auth.Attributes["priority"])
	}
	if priority >= 0 {
		t.Fatalf("expected priority lower than local codex (0), got %d", priority)
	}
	if auth.Attributes["source"] != codexmanager.RuntimeSourceCodexManager {
		t.Fatalf("expected attributes source %q, got %q", codexmanager.RuntimeSourceCodexManager, auth.Attributes["source"])
	}
	if auth.Attributes["auth_kind"] != "oauth" {
		t.Fatalf("expected auth_kind oauth, got %q", auth.Attributes["auth_kind"])
	}
	if apiKey := auth.Attributes["api_key"]; apiKey != "" {
		t.Fatalf("expected oauth auth without api_key attribute, got %q", apiKey)
	}
	if source, _ := auth.Metadata["source"].(string); source != codexmanager.RuntimeSourceCodexManager {
		t.Fatalf("expected metadata source %q, got %q", codexmanager.RuntimeSourceCodexManager, source)
	}
	if typ, _ := auth.Metadata["type"].(string); typ != "codex" {
		t.Fatalf("expected metadata type codex, got %q", typ)
	}
	if accountID, _ := auth.Metadata["account_id"].(string); accountID != "chatgpt-oauth-account" {
		t.Fatalf("expected metadata account_id chatgpt-oauth-account, got %q", accountID)
	}
	if accessToken, _ := auth.Metadata["access_token"].(string); accessToken != "oauth-access-token" {
		t.Fatalf("expected metadata access_token oauth-access-token, got %q", accessToken)
	}
	if refreshToken, _ := auth.Metadata["refresh_token"].(string); refreshToken != "oauth-refresh-token" {
		t.Fatalf("expected metadata refresh_token oauth-refresh-token, got %q", refreshToken)
	}
	if email, _ := auth.Metadata["email"].(string); email != "oauth@example.com" {
		t.Fatalf("expected metadata email oauth@example.com, got %q", email)
	}
}

func TestCodexManagerBuildProjectedRuntimeCodexAuthsAPIKeyMapping(t *testing.T) {
	syncedAt := time.Date(2026, time.March, 8, 15, 30, 0, 0, time.UTC)
	auths := codexmanager.BuildProjectedRuntimeCodexAuths(
		[]codexmanager.ProjectionAccount{{
			ProjectionID: "cm:acc-api",
			AccountID:    "acc-api",
			ExternalRef:  "acc-api",
			RelayEnabled: true,
			Source:       codexmanager.RuntimeSourceCodexManager,
			LastSyncedAt: syncedAt,
			UpstreamSort: 3,
		}},
		[]codexmanager.CredentialRecord{{
			AccountID: "acc-api",
			APIKey:    "runtime-api-key",
			BaseURL:   "https://chatgpt.com/backend-api/codex",
			UpdatedAt: syncedAt,
		}},
	)
	if len(auths) != 1 {
		t.Fatalf("expected 1 runtime auth, got %d", len(auths))
	}
	auth := auths[0]
	if auth.Attributes["api_key"] != "runtime-api-key" {
		t.Fatalf("expected api_key runtime-api-key, got %q", auth.Attributes["api_key"])
	}
	if auth.Attributes["auth_kind"] != "api_key" {
		t.Fatalf("expected auth_kind api_key, got %q", auth.Attributes["auth_kind"])
	}
	if auth.Attributes["base_url"] != "https://chatgpt.com/backend-api/codex" {
		t.Fatalf("expected base_url mapped, got %q", auth.Attributes["base_url"])
	}
	if auth.Provider != "codex" {
		t.Fatalf("expected provider codex, got %q", auth.Provider)
	}
	if auth.ID != "cm:acc-api" {
		t.Fatalf("expected id cm:acc-api, got %q", auth.ID)
	}
}

func TestCodexManagerLoadProjectedRuntimeCodexAuthsSkipsOverlayDisabledAndTombstone(t *testing.T) {
	stateDir := t.TempDir()
	repo, err := codexmanager.NewProjectionRepository(stateDir)
	if err != nil {
		t.Fatalf("create projection repository: %v", err)
	}

	syncedAt := time.Date(2026, time.March, 8, 16, 0, 0, 0, time.UTC)
	base := []codexmanager.ProjectionAccount{
		{
			ProjectionID: "cm:acc-enabled",
			AccountID:    "acc-enabled",
			ExternalRef:  "acc-enabled",
			RelayEnabled: true,
			Source:       codexmanager.RuntimeSourceCodexManager,
			LastSyncedAt: syncedAt,
			UpstreamSort: 1,
		},
		{
			ProjectionID: "cm:acc-disabled",
			AccountID:    "acc-disabled",
			ExternalRef:  "acc-disabled",
			RelayEnabled: true,
			Source:       codexmanager.RuntimeSourceCodexManager,
			LastSyncedAt: syncedAt,
			UpstreamSort: 2,
		},
		{
			ProjectionID: "cm:acc-tomb",
			AccountID:    "acc-tomb",
			ExternalRef:  "acc-tomb",
			RelayEnabled: true,
			Source:       codexmanager.RuntimeSourceCodexManager,
			LastSyncedAt: syncedAt,
			UpstreamSort: 3,
		},
	}
	if _, err := repo.ApplySync(base, syncedAt); err != nil {
		t.Fatalf("seed projection sync: %v", err)
	}
	disabledProjectionID, err := codexmanager.ProjectionIDForAccountID("acc-disabled")
	if err != nil {
		t.Fatalf("build disabled projection id: %v", err)
	}
	if _, err := repo.SetRelayEnabled(disabledProjectionID, false, syncedAt.Add(1*time.Minute)); err != nil {
		t.Fatalf("set relay overlay false: %v", err)
	}

	nextSync := []codexmanager.ProjectionAccount{base[0], base[1]}
	nextSyncAt := syncedAt.Add(2 * time.Minute)
	nextSync[0].LastSyncedAt = nextSyncAt
	nextSync[1].LastSyncedAt = nextSyncAt
	if _, err := repo.ApplySync(nextSync, nextSyncAt); err != nil {
		t.Fatalf("apply second sync: %v", err)
	}

	store, err := codexmanager.NewCredentialStoreForStateDir(stateDir)
	if err != nil {
		t.Fatalf("create credential store: %v", err)
	}
	if _, err := store.UpsertBatch([]codexmanager.CredentialRecord{
		{AccountID: "acc-enabled", AccessToken: "enabled-token", RefreshToken: "enabled-refresh", Email: "enabled@example.com"},
		{AccountID: "acc-disabled", AccessToken: "disabled-token", RefreshToken: "disabled-refresh", Email: "disabled@example.com"},
		{AccountID: "acc-tomb", AccessToken: "tomb-token", RefreshToken: "tomb-refresh", Email: "tomb@example.com"},
	}, nextSyncAt); err != nil {
		t.Fatalf("seed credential store: %v", err)
	}

	auths, err := codexmanager.LoadProjectedRuntimeCodexAuths(stateDir)
	if err != nil {
		t.Fatalf("load projected runtime codex auths: %v", err)
	}
	if len(auths) != 1 {
		t.Fatalf("expected only enabled runtime auth, got %d", len(auths))
	}
	if auths[0].ID != "cm:acc-enabled" {
		t.Fatalf("expected only cm:acc-enabled, got %q", auths[0].ID)
	}
}
