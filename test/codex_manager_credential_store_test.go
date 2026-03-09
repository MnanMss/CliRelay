package test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/codexmanager"
)

func TestCodexManagerCredentialStorePersistence(t *testing.T) {
	stateDir := t.TempDir()
	store, err := codexmanager.NewCredentialStoreForStateDir(stateDir)
	if err != nil {
		t.Fatalf("create credential store: %v", err)
	}

	updatedAtA := time.Date(2026, time.March, 8, 12, 0, 0, 0, time.UTC)
	recordA, err := store.Upsert(codexmanager.CredentialRecord{
		AccountID:    "acc-b",
		AccountRef:   "chatgpt-b",
		AccessToken:  "access-token-b",
		RefreshToken: "refresh-token-b",
		Email:        "acc-b@example.com",
	}, updatedAtA)
	if err != nil {
		t.Fatalf("upsert recordA: %v", err)
	}
	if recordA.AccountID != "acc-b" {
		t.Fatalf("expected accountId acc-b, got %q", recordA.AccountID)
	}
	if recordA.AccountRef != "chatgpt-b" {
		t.Fatalf("expected accountID chatgpt-b, got %q", recordA.AccountRef)
	}
	if recordA.AccessToken != "access-token-b" {
		t.Fatalf("expected access token persisted, got %q", recordA.AccessToken)
	}
	if recordA.RefreshToken != "refresh-token-b" {
		t.Fatalf("expected refresh token persisted, got %q", recordA.RefreshToken)
	}
	if recordA.Email != "acc-b@example.com" {
		t.Fatalf("expected email persisted, got %q", recordA.Email)
	}
	if !recordA.UpdatedAt.Equal(updatedAtA) {
		t.Fatalf("expected updatedAt %s, got %s", updatedAtA, recordA.UpdatedAt)
	}

	updatedAtB := time.Date(2026, time.March, 8, 12, 1, 0, 0, time.UTC)
	if _, err := store.Upsert(codexmanager.CredentialRecord{
		AccountID:    "acc-a",
		AccessToken:  "access-token-a",
		RefreshToken: "refresh-token-a",
		Email:        "acc-a@example.com",
	}, updatedAtB); err != nil {
		t.Fatalf("upsert recordB: %v", err)
	}

	list := store.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 records, got %d", len(list))
	}
	if list[0].AccountID != "acc-a" || list[1].AccountID != "acc-b" {
		t.Fatalf("expected sorted accountIds [acc-a acc-b], got [%s %s]", list[0].AccountID, list[1].AccountID)
	}

	storePath := codexmanager.CredentialStatePath(stateDir)
	if storePath == "" {
		t.Fatal("expected non-empty credential store path")
	}
	if _, statErr := os.Stat(storePath); statErr != nil {
		t.Fatalf("expected store file at %s: %v", storePath, statErr)
	}
	if filepath.Base(storePath) != "credential_state.json" {
		t.Fatalf("expected store file name credential_state.json, got %s", filepath.Base(storePath))
	}

	reloaded, err := codexmanager.NewCredentialStoreForStateDir(stateDir)
	if err != nil {
		t.Fatalf("reload credential store: %v", err)
	}
	gotA, ok := reloaded.Get("acc-a")
	if !ok {
		t.Fatal("expected acc-a after reload")
	}
	if gotA.AccessToken != "access-token-a" {
		t.Fatalf("expected reloaded access token access-token-a, got %q", gotA.AccessToken)
	}

	if err := reloaded.Delete("acc-b", time.Date(2026, time.March, 8, 12, 2, 0, 0, time.UTC)); err != nil {
		t.Fatalf("delete acc-b: %v", err)
	}

	reloadedAfterDelete, err := codexmanager.NewCredentialStoreForStateDir(stateDir)
	if err != nil {
		t.Fatalf("reload after delete: %v", err)
	}
	if _, exists := reloadedAfterDelete.Get("acc-b"); exists {
		t.Fatal("expected acc-b deleted from persisted store")
	}
	if _, exists := reloadedAfterDelete.Get("acc-a"); !exists {
		t.Fatal("expected acc-a to remain after deleting acc-b")
	}
}

func TestCodexManagerCredentialStoreLoadAndValidation(t *testing.T) {
	stateDir := t.TempDir()
	store, err := codexmanager.NewCredentialStoreForStateDir(stateDir)
	if err != nil {
		t.Fatalf("create credential store: %v", err)
	}

	if _, err := store.Upsert(codexmanager.CredentialRecord{
		AccountID:    "acc-load-a",
		AccessToken:  "access-a",
		RefreshToken: "refresh-a",
		Email:        "acc-load-a@example.com",
	}, time.Date(2026, time.March, 8, 13, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seed first record: %v", err)
	}

	secondaryStore, err := codexmanager.NewCredentialStoreForStateDir(stateDir)
	if err != nil {
		t.Fatalf("create secondary credential store: %v", err)
	}
	if _, err := secondaryStore.Upsert(codexmanager.CredentialRecord{
		AccountID:    "acc-load-b",
		AccessToken:  "access-b",
		RefreshToken: "refresh-b",
		Email:        "acc-load-b@example.com",
	}, time.Date(2026, time.March, 8, 13, 1, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seed second record: %v", err)
	}

	if _, exists := store.Get("acc-load-b"); exists {
		t.Fatal("expected primary store cache to miss acc-load-b before Load")
	}
	if err := store.Load(); err != nil {
		t.Fatalf("reload primary store via Load: %v", err)
	}
	if _, exists := store.Get("acc-load-b"); !exists {
		t.Fatal("expected Load() to refresh in-memory state with acc-load-b")
	}

	if _, err := store.Upsert(codexmanager.CredentialRecord{AccountID: "   ", AccessToken: "x"}, time.Now().UTC()); err != codexmanager.ErrInvalidAccountID {
		t.Fatalf("expected ErrInvalidAccountID for empty accountId, got %v", err)
	}
}
