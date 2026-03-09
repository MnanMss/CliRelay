package test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/api"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/codexmanager"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	sdkaccess "github.com/router-for-me/CLIProxyAPI/v6/sdk/access"
	sdkauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
	"golang.org/x/crypto/bcrypt"
)

type fakeProjectionRPCClient struct {
	mu              sync.Mutex
	accounts        []codexmanager.RPCAccountSummary
	usage           []codexmanager.RPCUsageSnapshot
	listAccountsErr error
	listUsageErr    error
	writeCalls      int
}

func (f *fakeProjectionRPCClient) setAccounts(accounts []codexmanager.RPCAccountSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accounts = append([]codexmanager.RPCAccountSummary(nil), accounts...)
}

func (f *fakeProjectionRPCClient) setUsage(usage []codexmanager.RPCUsageSnapshot) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.usage = append([]codexmanager.RPCUsageSnapshot(nil), usage...)
}

func (f *fakeProjectionRPCClient) writeCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writeCalls
}

func (f *fakeProjectionRPCClient) ListAccounts(_ context.Context, params codexmanager.RPCAccountListParams) (codexmanager.RPCAccountListResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listAccountsErr != nil {
		return codexmanager.RPCAccountListResult{}, f.listAccountsErr
	}
	page := params.Page
	if page <= 0 {
		page = 1
	}
	pageSize := params.PageSize
	if pageSize <= 0 {
		pageSize = codexmanager.DefaultPageSize
	}
	start := (page - 1) * pageSize
	if start >= len(f.accounts) {
		return codexmanager.RPCAccountListResult{
			Items:    []codexmanager.RPCAccountSummary{},
			Total:    int64(len(f.accounts)),
			Page:     int64(page),
			PageSize: int64(pageSize),
		}, nil
	}
	end := start + pageSize
	if end > len(f.accounts) {
		end = len(f.accounts)
	}
	items := append([]codexmanager.RPCAccountSummary(nil), f.accounts[start:end]...)
	return codexmanager.RPCAccountListResult{
		Items:    items,
		Total:    int64(len(f.accounts)),
		Page:     int64(page),
		PageSize: int64(pageSize),
	}, nil
}

func (f *fakeProjectionRPCClient) ImportAccounts(_ context.Context, _ []string) (codexmanager.RPCAccountImportResult, error) {
	f.mu.Lock()
	f.writeCalls++
	f.mu.Unlock()
	return codexmanager.RPCAccountImportResult{}, nil
}

func (f *fakeProjectionRPCClient) DeleteAccount(_ context.Context, _ string) error {
	f.mu.Lock()
	f.writeCalls++
	f.mu.Unlock()
	return nil
}

func (f *fakeProjectionRPCClient) StartLogin(_ context.Context, _ codexmanager.RPCLoginStartRequest) (codexmanager.RPCLoginStartResult, error) {
	f.mu.Lock()
	f.writeCalls++
	f.mu.Unlock()
	return codexmanager.RPCLoginStartResult{}, nil
}

func (f *fakeProjectionRPCClient) GetLoginStatus(_ context.Context, _ string) (codexmanager.RPCLoginStatusResult, error) {
	return codexmanager.RPCLoginStatusResult{}, nil
}

func (f *fakeProjectionRPCClient) CompleteLogin(_ context.Context, _ codexmanager.RPCLoginCompleteRequest) error {
	f.mu.Lock()
	f.writeCalls++
	f.mu.Unlock()
	return nil
}

func (f *fakeProjectionRPCClient) ReadUsage(_ context.Context, _ string) (codexmanager.RPCUsageReadResult, error) {
	return codexmanager.RPCUsageReadResult{}, nil
}

func (f *fakeProjectionRPCClient) ListUsage(_ context.Context) (codexmanager.RPCUsageListResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listUsageErr != nil {
		return codexmanager.RPCUsageListResult{}, f.listUsageErr
	}
	items := append([]codexmanager.RPCUsageSnapshot(nil), f.usage...)
	return codexmanager.RPCUsageListResult{Items: items}, nil
}

func (f *fakeProjectionRPCClient) RefreshUsage(_ context.Context, _ string) error {
	f.mu.Lock()
	f.writeCalls++
	f.mu.Unlock()
	return nil
}

func TestCodexManagerProjectionSyncAndTombstone(t *testing.T) {
	repo, err := codexmanager.NewProjectionRepository(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create projection repository: %v", err)
	}

	futureReset := time.Now().UTC().Add(30 * time.Minute).Unix()
	accountA := codexmanager.RPCAccountSummary{ID: "acc-a", Label: "A", GroupName: strPtr("group-a"), Sort: 1, Status: "active"}
	accountB := codexmanager.RPCAccountSummary{ID: "acc-b", Label: "B", GroupName: strPtr("group-b"), Sort: 2, Status: "active"}
	client := &fakeProjectionRPCClient{}
	client.setAccounts([]codexmanager.RPCAccountSummary{accountA, accountB})
	client.setUsage([]codexmanager.RPCUsageSnapshot{
		{AccountID: strPtr("acc-a"), AvailabilityStatus: strPtr("available"), UsedPercent: floatPtr(42)},
		{AccountID: strPtr("acc-b"), AvailabilityStatus: strPtr("limited"), UsedPercent: floatPtr(100), ResetsAt: int64Ptr(futureReset)},
	})

	worker, err := codexmanager.NewProjectionSyncWorker(codexmanager.NewRPCService(client), repo)
	if err != nil {
		t.Fatalf("failed to create projection sync worker: %v", err)
	}

	firstSummary, err := worker.Sync(context.Background())
	if err != nil {
		t.Fatalf("first sync failed: %v", err)
	}
	if firstSummary.Added != 2 {
		t.Fatalf("expected 2 added projections, got %d", firstSummary.Added)
	}
	if firstSummary.Tombstoned != 0 {
		t.Fatalf("expected 0 tombstoned projections, got %d", firstSummary.Tombstoned)
	}

	projectionAID, err := codexmanager.ProjectionIDForAccountID("acc-a")
	if err != nil {
		t.Fatalf("failed to build projection id: %v", err)
	}
	projectionBID, err := codexmanager.ProjectionIDForAccountID("acc-b")
	if err != nil {
		t.Fatalf("failed to build projection id: %v", err)
	}

	projectionA, ok := repo.Get(projectionAID)
	if !ok {
		t.Fatalf("projection %s not found", projectionAID)
	}
	if projectionA.Source != codexmanager.RuntimeSourceCodexManager {
		t.Fatalf("expected source %q, got %q", codexmanager.RuntimeSourceCodexManager, projectionA.Source)
	}
	if projectionA.Label != "A" {
		t.Fatalf("expected label A, got %q", projectionA.Label)
	}
	if projectionA.GroupName != "group-a" {
		t.Fatalf("expected groupName group-a, got %q", projectionA.GroupName)
	}
	if projectionA.ExternalRef != projectionA.AccountID {
		t.Fatalf("expected externalRef to mirror accountId, got externalRef=%q accountId=%q", projectionA.ExternalRef, projectionA.AccountID)
	}
	if projectionA.VersionHash == "" {
		t.Fatal("expected non-empty version hash")
	}
	if projectionA.SchemaVersion == 0 {
		t.Fatal("expected non-zero schema version")
	}
	if projectionA.LastSyncedAt.IsZero() {
		t.Fatal("expected non-zero lastSyncedAt")
	}
	if projectionA.UsageSummary.AvailabilityStatus != "available" {
		t.Fatalf("expected usage summary availability available, got %q", projectionA.UsageSummary.AvailabilityStatus)
	}
	if projectionA.UsageSummary.UsedPercent == nil || *projectionA.UsageSummary.UsedPercent != 42 {
		t.Fatalf("expected usage summary usedPercent 42, got %#v", projectionA.UsageSummary.UsedPercent)
	}

	projectionB, ok := repo.Get(projectionBID)
	if !ok {
		t.Fatalf("projection %s not found", projectionBID)
	}
	if projectionB.Label != "B" {
		t.Fatalf("expected label B, got %q", projectionB.Label)
	}
	if projectionB.GroupName != "group-b" {
		t.Fatalf("expected groupName group-b, got %q", projectionB.GroupName)
	}
	if projectionB.Health.BackoffLevel < 1 {
		t.Fatalf("expected projectionB backoff level >= 1, got %d", projectionB.Health.BackoffLevel)
	}
	if projectionB.Health.BackoffUntil == nil {
		t.Fatal("expected projectionB backoff until to be populated")
	}

	client.setAccounts([]codexmanager.RPCAccountSummary{accountB})
	secondSummary, err := worker.Sync(context.Background())
	if err != nil {
		t.Fatalf("second sync failed: %v", err)
	}
	if secondSummary.Tombstoned != 1 {
		t.Fatalf("expected 1 tombstoned projection, got %d", secondSummary.Tombstoned)
	}

	tombstonedA, ok := repo.Get(projectionAID)
	if !ok {
		t.Fatalf("projection %s missing after second sync", projectionAID)
	}
	if !tombstonedA.Tombstone {
		t.Fatalf("expected %s to be tombstoned", projectionAID)
	}
	if tombstonedA.Health.Reason == "" {
		t.Fatal("expected tombstoned projection to include reason")
	}

	liveB, ok := repo.Get(projectionBID)
	if !ok {
		t.Fatalf("projection %s missing after second sync", projectionBID)
	}
	if liveB.Tombstone {
		t.Fatalf("expected %s to stay active", projectionBID)
	}
}

func TestCodexManagerRelayEnabledOverlay(t *testing.T) {
	stateDir := t.TempDir()
	repo, err := codexmanager.NewProjectionRepository(stateDir)
	if err != nil {
		t.Fatalf("failed to create projection repository: %v", err)
	}

	client := &fakeProjectionRPCClient{}
	client.setAccounts([]codexmanager.RPCAccountSummary{{ID: "acc-overlay", Label: "Overlay", Sort: 3, Status: "active"}})

	worker, err := codexmanager.NewProjectionSyncWorker(codexmanager.NewRPCService(client), repo)
	if err != nil {
		t.Fatalf("failed to create sync worker: %v", err)
	}
	if _, err := worker.Sync(context.Background()); err != nil {
		t.Fatalf("initial sync failed: %v", err)
	}

	projectionID, err := codexmanager.ProjectionIDForAccountID("acc-overlay")
	if err != nil {
		t.Fatalf("failed to build projection id: %v", err)
	}
	updatedProjection, err := repo.SetRelayEnabled(projectionID, false, time.Now().UTC())
	if err != nil {
		t.Fatalf("failed to set relay overlay: %v", err)
	}
	if updatedProjection.RelayEnabled {
		t.Fatal("expected relayEnabled=false after overlay update")
	}

	reloadedRepo, err := codexmanager.NewProjectionRepository(stateDir)
	if err != nil {
		t.Fatalf("failed to reload projection repository: %v", err)
	}
	reloadedProjection, ok := reloadedRepo.Get(projectionID)
	if !ok {
		t.Fatalf("projection %s not found after reload", projectionID)
	}
	if reloadedProjection.RelayEnabled {
		t.Fatal("expected relayEnabled=false after repository reload")
	}

	workerReloaded, err := codexmanager.NewProjectionSyncWorker(codexmanager.NewRPCService(client), reloadedRepo)
	if err != nil {
		t.Fatalf("failed to create reloaded worker: %v", err)
	}
	if _, err := workerReloaded.Sync(context.Background()); err != nil {
		t.Fatalf("sync after overlay update failed: %v", err)
	}

	afterSyncProjection, ok := reloadedRepo.Get(projectionID)
	if !ok {
		t.Fatalf("projection %s not found after sync", projectionID)
	}
	if afterSyncProjection.RelayEnabled {
		t.Fatal("expected overlay relayEnabled=false to persist across sync")
	}
	if client.writeCallCount() != 0 {
		t.Fatalf("expected zero codex-manager write calls, got %d", client.writeCallCount())
	}
}

func TestCodexManagerSyncFailureDoesNotBlockStartup(t *testing.T) {
	badEndpoint := unreachableHTTPAddress(t)
	baseURL, stopServer := startServerWithCodexManagerEndpoint(t, badEndpoint)
	defer stopServer()

	client := &http.Client{Timeout: 3 * time.Second}
	rootResp, err := client.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("root request failed: %v", err)
	}
	defer func() { _ = rootResp.Body.Close() }()
	if rootResp.StatusCode != http.StatusOK {
		t.Fatalf("expected root status %d, got %d", http.StatusOK, rootResp.StatusCode)
	}

	mgmtReq, err := http.NewRequest(http.MethodGet, baseURL+"/v0/management/config", nil)
	if err != nil {
		t.Fatalf("failed to create management request: %v", err)
	}
	mgmtReq.Header.Set("Authorization", "Bearer "+testManagementKey)
	mgmtResp, err := client.Do(mgmtReq)
	if err != nil {
		t.Fatalf("management request failed: %v", err)
	}
	defer func() { _ = mgmtResp.Body.Close() }()
	if mgmtResp.StatusCode != http.StatusOK {
		t.Fatalf("expected management config status %d, got %d", http.StatusOK, mgmtResp.StatusCode)
	}
}

func startServerWithCodexManagerEndpoint(t *testing.T, endpoint string) (string, func()) {
	t.Helper()

	tmpDir := t.TempDir()
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatalf("failed to create auth directory: %v", err)
	}

	hashedSecret, err := bcrypt.GenerateFromPassword([]byte(testManagementKey), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("failed to hash management secret: %v", err)
	}

	port := reservePort(t)
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("port: 0\n"), 0o644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg := &config.Config{
		SDKConfig:              sdkconfig.SDKConfig{APIKeys: []string{"test-key"}},
		Host:                   "127.0.0.1",
		Port:                   port,
		AuthDir:                authDir,
		Debug:                  true,
		LoggingToFile:          false,
		UsageStatisticsEnabled: false,
		RemoteManagement: config.RemoteManagement{
			SecretKey: string(hashedSecret),
		},
		CodexManager: config.CodexManagerConfig{
			Enabled:               true,
			Endpoint:              endpoint,
			RequestTimeoutSeconds: 1,
		},
	}

	authManager := sdkauth.NewManager(nil, nil, nil)
	accessManager := sdkaccess.NewManager()
	server := api.NewServer(cfg, authManager, accessManager, configPath)

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start()
	}()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitForServerReady(t, baseURL)

	stopFn := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Stop(ctx)
		select {
		case <-time.After(2 * time.Second):
		case <-errCh:
		}
	}
	return baseURL, stopFn
}

func waitForServerReady(t *testing.T, baseURL string) {
	t.Helper()
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for i := 0; i < 80; i++ {
		resp, err := client.Get(baseURL + "/")
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server did not become ready: %s", baseURL)
}

func unreachableHTTPAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve unreachable port: %v", err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	return "http://" + addr
}

func strPtr(value string) *string {
	return &value
}

func floatPtr(value float64) *float64 {
	return &value
}

func int64Ptr(value int64) *int64 {
	return &value
}
