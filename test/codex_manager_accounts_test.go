package test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/codexmanager"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

const (
	seedAccountAlpha   = "acc-alpha"
	seedAccountBravo   = "acc-bravo"
	seedAccountCharlie = "acc-charlie"
	seedLabelAlpha     = "Alpha Runner"
	seedLabelBravo     = "Bravo Backup"
	seedLabelCharlie   = "Charlie Tombstone"
	seedGroupAlpha     = "team-red"
	seedGroupBravo     = "team-blue"
	seedGroupCharlie   = "team-green"
)

func TestCodexManagerAccountsListAndDetail(t *testing.T) {
	gin.SetMode(gin.TestMode)

	stub := NewRPCStubServer(t)
	defer stub.Close()
	stub.SetMethodHook("account/usage/read", func(req RPCRequestCapture) (any, error) {
		params := MustDecodeParamsAsMap(t, req.Params)
		accountID, _ := params["accountId"].(string)
		switch accountID {
		case seedAccountAlpha:
			return map[string]any{
				"snapshot": map[string]any{
					"accountId":          seedAccountAlpha,
					"availabilityStatus": "available",
					"usedPercent":        42,
					"windowMinutes":      60,
					"capturedAt":         time.Date(2026, time.March, 1, 10, 10, 0, 0, time.UTC).Unix(),
				},
			}, nil
		case seedAccountBravo:
			return nil, errors.New("usage read unavailable")
		default:
			return map[string]any{"snapshot": nil}, nil
		}
	})

	cfg := &config.Config{
		AuthDir: t.TempDir(),
		CodexManager: config.CodexManagerConfig{
			Enabled:               true,
			Endpoint:              stub.URL(),
			RequestTimeoutSeconds: 1,
		},
	}
	seedCodexManagerProjectionState(t, cfg)

	handler := codexmanager.NewHandler(cfg)
	router := gin.New()
	routes := router.Group(codexmanager.ManagementNamespace)
	routes.GET("/accounts", handler.ListAccounts)
	routes.GET("/accounts/:accountId", handler.GetAccount)

	t.Run("list_supports_local_pagination", func(t *testing.T) {
		payload := performJSONRequest(t, router, http.MethodGet, codexmanager.ManagementNamespace+"/accounts?page=1&pageSize=2")
		assertBoolField(t, payload, "ok", true)

		data := requireObjectField(t, payload, "data")
		assertNumberField(t, data, "total", 3)
		assertNumberField(t, data, "page", 1)
		assertNumberField(t, data, "pageSize", 2)

		items := requireArrayField(t, data, "items")
		if len(items) != 2 {
			t.Fatalf("expected 2 paged items, got %d", len(items))
		}

		first := requireObjectValue(t, items[0], "first list item")
		assertAccountShape(t, first)
		assertStringField(t, first, "accountId", seedAccountAlpha)
		assertStringField(t, first, "label", seedLabelAlpha)
		assertStringField(t, first, "groupName", seedGroupAlpha)
		assertStringField(t, first, "status", "active")
		assertNumberField(t, first, "sort", 10)
		assertBoolField(t, first, "relayEnabled", true)
		assertBoolField(t, first, "runtimeIncluded", true)
		assertBoolField(t, first, "stale", false)
		assertUsageSummary(t, first["usageSummary"], "available", 42, 60)

		second := requireObjectValue(t, items[1], "second list item")
		assertAccountShape(t, second)
		assertStringField(t, second, "accountId", seedAccountBravo)
		assertStringField(t, second, "label", seedLabelBravo)
		assertStringField(t, second, "groupName", seedGroupBravo)
		assertStringField(t, second, "status", "active")
		assertNumberField(t, second, "sort", 20)
		assertBoolField(t, second, "relayEnabled", false)
		assertBoolField(t, second, "runtimeIncluded", false)
		assertBoolField(t, second, "stale", false)
		assertUsageSummary(t, second["usageSummary"], "limited", 87, 60)
	})

	t.Run("list_supports_query_filtering", func(t *testing.T) {
		payload := performJSONRequest(t, router, http.MethodGet, codexmanager.ManagementNamespace+"/accounts?page=1&pageSize=10&query=team-green")
		data := requireObjectField(t, payload, "data")
		assertNumberField(t, data, "total", 1)

		items := requireArrayField(t, data, "items")
		if len(items) != 1 {
			t.Fatalf("expected 1 filtered item, got %d", len(items))
		}

		item := requireObjectValue(t, items[0], "filtered list item")
		assertAccountShape(t, item)
		assertStringField(t, item, "accountId", seedAccountCharlie)
		assertStringField(t, item, "label", seedLabelCharlie)
		assertStringField(t, item, "groupName", seedGroupCharlie)
		assertStringField(t, item, "status", "inactive")
		assertNumberField(t, item, "sort", 30)
		assertBoolField(t, item, "runtimeIncluded", false)
		assertBoolField(t, item, "stale", true)
		assertUsageSummary(t, item["usageSummary"], "exhausted", 100, 60)
	})

	t.Run("list_returns_empty_page_for_no_matches", func(t *testing.T) {
		payload := performJSONRequest(t, router, http.MethodGet, codexmanager.ManagementNamespace+"/accounts?page=1&pageSize=10&query=missing")
		data := requireObjectField(t, payload, "data")
		assertNumberField(t, data, "total", 0)

		items := requireArrayField(t, data, "items")
		if len(items) != 0 {
			t.Fatalf("expected empty result set, got %d item(s)", len(items))
		}
	})

	t.Run("detail_returns_projection_and_usage_snapshot", func(t *testing.T) {
		payload := performJSONRequest(t, router, http.MethodGet, codexmanager.ManagementNamespace+"/accounts/"+seedAccountAlpha)
		assertBoolField(t, payload, "ok", true)

		data := requireObjectField(t, payload, "data")
		assertAccountShape(t, data)
		assertStringField(t, data, "accountId", seedAccountAlpha)
		assertStringField(t, data, "label", seedLabelAlpha)
		assertStringField(t, data, "groupName", seedGroupAlpha)
		assertStringField(t, data, "status", "active")
		assertNumberField(t, data, "sort", 10)
		assertUsageSummary(t, data["usageSummary"], "available", 42, 60)

		usageSnapshot := requireObjectField(t, data, "usageSnapshot")
		assertStringField(t, usageSnapshot, "accountId", seedAccountAlpha)
		assertStringField(t, usageSnapshot, "availabilityStatus", "available")
		assertNumberField(t, usageSnapshot, "usedPercent", 42)
	})

	t.Run("detail_uses_null_snapshot_when_usage_read_fails", func(t *testing.T) {
		payload := performJSONRequest(t, router, http.MethodGet, codexmanager.ManagementNamespace+"/accounts/"+seedAccountBravo)
		data := requireObjectField(t, payload, "data")
		assertAccountShape(t, data)
		assertStringField(t, data, "label", seedLabelBravo)
		assertUsageSummary(t, data["usageSummary"], "limited", 87, 60)
		if value, exists := data["usageSnapshot"]; !exists || value != nil {
			t.Fatalf("expected usageSnapshot=null when usage read fails, got %#v", data["usageSnapshot"])
		}
	})

	t.Run("fresh_startup_live_handler_sees_async_synced_projection", func(t *testing.T) {
		startupStub := NewRPCStubServer(t)
		defer startupStub.Close()

		startupStub.SetMethodHook("account/list", func(req RPCRequestCapture) (any, error) {
			params := MustDecodeParamsAsMap(t, req.Params)
			page := int(params["page"].(float64))
			pageSize := int(params["pageSize"].(float64))
			if page > 1 {
				return map[string]any{"items": []any{}, "total": 1, "page": page, "pageSize": pageSize}, nil
			}
			return map[string]any{
				"items": []map[string]any{{
					"id":        "acc-startup",
					"label":     "Startup Synced",
					"groupName": "team-startup",
					"sort":      7,
					"status":    "active",
				}},
				"total":    1,
				"page":     1,
				"pageSize": pageSize,
			}, nil
		})
		startupStub.SetMethodHook("account/usage/list", func(req RPCRequestCapture) (any, error) {
			_ = req
			return map[string]any{
				"items": []map[string]any{{
					"accountId":          "acc-startup",
					"availabilityStatus": "available",
					"usedPercent":        61,
					"windowMinutes":      60,
					"capturedAt":         time.Date(2026, time.March, 1, 10, 30, 0, 0, time.UTC).Unix(),
				}},
			}, nil
		})
		startupStub.SetMethodHook("account/usage/read", func(req RPCRequestCapture) (any, error) {
			params := MustDecodeParamsAsMap(t, req.Params)
			if accountID, _ := params["accountId"].(string); accountID != "acc-startup" {
				return map[string]any{"snapshot": nil}, nil
			}
			return map[string]any{
				"snapshot": map[string]any{
					"accountId":          "acc-startup",
					"availabilityStatus": "available",
					"usedPercent":        61,
					"windowMinutes":      60,
					"capturedAt":         time.Date(2026, time.March, 1, 10, 31, 0, 0, time.UTC).Unix(),
				},
			}, nil
		})

		baseURL, stopServer := startServerWithCodexManagerEndpoint(t, startupStub.URL())
		defer stopServer()

		client := &http.Client{Timeout: 3 * time.Second}
		var listPayload map[string]any
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			resp := doManagementGET(t, client, baseURL+codexmanager.ManagementNamespace+"/accounts?page=1&pageSize=20", true)
			if resp.StatusCode == http.StatusOK {
				payload := decodeJSONMap(t, readAndCloseResponseBodyBytes(t, resp))
				data := requireObjectField(t, payload, "data")
				items := requireArrayField(t, data, "items")
				if len(items) == 1 {
					item := requireObjectValue(t, items[0], "startup synced list item")
					if accountID, _ := item["accountId"].(string); accountID == "acc-startup" {
						listPayload = payload
						break
					}
				}
			} else {
				_ = resp.Body.Close()
			}
			time.Sleep(100 * time.Millisecond)
		}
		if listPayload == nil {
			t.Fatal("fresh startup list never reflected async synced projection")
		}

		listData := requireObjectField(t, listPayload, "data")
		items := requireArrayField(t, listData, "items")
		item := requireObjectValue(t, items[0], "startup list item")
		assertAccountShape(t, item)
		assertStringField(t, item, "accountId", "acc-startup")
		assertStringField(t, item, "label", "Startup Synced")
		assertStringField(t, item, "groupName", "team-startup")
		assertStringField(t, item, "status", "active")
		assertNumberField(t, item, "sort", 7)
		assertUsageSummary(t, item["usageSummary"], "available", 61, 60)

		detailResp := doManagementGET(t, client, baseURL+codexmanager.ManagementNamespace+"/accounts/acc-startup", true)
		if detailResp.StatusCode != http.StatusOK {
			body := readAndCloseResponseBody(t, detailResp)
			t.Fatalf("expected startup detail status %d, got %d: %s", http.StatusOK, detailResp.StatusCode, body)
		}
		detailPayload := decodeJSONMap(t, readAndCloseResponseBodyBytes(t, detailResp))
		detailData := requireObjectField(t, detailPayload, "data")
		assertAccountShape(t, detailData)
		assertStringField(t, detailData, "accountId", "acc-startup")
		assertUsageSummary(t, detailData["usageSummary"], "available", 61, 60)
		startupUsage := requireObjectField(t, detailData, "usageSnapshot")
		assertStringField(t, startupUsage, "accountId", "acc-startup")
		assertNumberField(t, startupUsage, "usedPercent", 61)
	})

	t.Run("detail_rejects_invalid_account_id", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, codexmanager.ManagementNamespace+"/accounts/%20", nil)
		router.ServeHTTP(recorder, req)

		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("expected invalid accountId status %d, got %d", http.StatusBadRequest, recorder.Code)
		}
		payload := decodeJSONMap(t, recorder.Body.Bytes())
		assertBoolField(t, payload, "ok", false)
		assertStringField(t, payload, "code", codexmanager.CodeBadRequest)
		assertStringField(t, payload, "message", codexmanager.ErrInvalidAccountID.Error())
	})

	t.Run("detail_returns_not_found_for_unknown_projection", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, codexmanager.ManagementNamespace+"/accounts/acc-missing", nil)
		router.ServeHTTP(recorder, req)

		if recorder.Code != http.StatusNotFound {
			t.Fatalf("expected not found status %d, got %d", http.StatusNotFound, recorder.Code)
		}
		payload := decodeJSONMap(t, recorder.Body.Bytes())
		assertBoolField(t, payload, "ok", false)
		assertStringField(t, payload, "code", codexmanager.CodeAccountNotFound)
		assertStringField(t, payload, "message", codexmanager.ErrAccountNotFound.Error())
	})
}

func seedCodexManagerProjectionState(t *testing.T, cfg *config.Config) {
	t.Helper()

	repo, err := codexmanager.NewProjectionRepository(codexmanager.ProjectionStateDir(cfg))
	if err != nil {
		t.Fatalf("failed to create projection repository: %v", err)
	}

	initialSyncAt := time.Date(2026, time.March, 1, 10, 0, 0, 0, time.UTC)
	initial := []codexmanager.ProjectionAccount{
		{
			ProjectionID:   "cm:" + seedAccountAlpha,
			AccountID:      seedAccountAlpha,
			ExternalRef:    seedAccountAlpha,
			Label:          seedLabelAlpha,
			GroupName:      seedGroupAlpha,
			Source:         codexmanager.RuntimeSourceCodexManager,
			LastSyncedAt:   initialSyncAt,
			UpstreamSort:   10,
			UpstreamStatus: "active",
			UsageSummary: codexmanager.ProjectionUsageSummary{
				AvailabilityStatus: "available",
				UsedPercent:        floatPtr(42),
				WindowMinutes:      int64Ptr(60),
				CapturedAt:         timePtr(initialSyncAt.Add(10 * time.Minute)),
			},
		},
		{
			ProjectionID:   "cm:" + seedAccountBravo,
			AccountID:      seedAccountBravo,
			ExternalRef:    seedAccountBravo,
			Label:          seedLabelBravo,
			GroupName:      seedGroupBravo,
			Source:         codexmanager.RuntimeSourceCodexManager,
			LastSyncedAt:   initialSyncAt,
			UpstreamSort:   20,
			UpstreamStatus: "active",
			UsageSummary: codexmanager.ProjectionUsageSummary{
				AvailabilityStatus: "limited",
				UsedPercent:        floatPtr(87),
				WindowMinutes:      int64Ptr(60),
				CapturedAt:         timePtr(initialSyncAt.Add(11 * time.Minute)),
			},
		},
		{
			ProjectionID:   "cm:" + seedAccountCharlie,
			AccountID:      seedAccountCharlie,
			ExternalRef:    seedAccountCharlie,
			Label:          seedLabelCharlie,
			GroupName:      seedGroupCharlie,
			Source:         codexmanager.RuntimeSourceCodexManager,
			LastSyncedAt:   initialSyncAt,
			UpstreamSort:   30,
			UpstreamStatus: "inactive",
			UsageSummary: codexmanager.ProjectionUsageSummary{
				AvailabilityStatus: "exhausted",
				UsedPercent:        floatPtr(100),
				WindowMinutes:      int64Ptr(60),
				CapturedAt:         timePtr(initialSyncAt.Add(12 * time.Minute)),
			},
		},
	}
	if _, err := repo.ApplySync(initial, initialSyncAt); err != nil {
		t.Fatalf("failed to seed initial projection state: %v", err)
	}

	latestSyncAt := initialSyncAt.Add(5 * time.Minute)
	latest := []codexmanager.ProjectionAccount{initial[0], initial[1]}
	latest[0].LastSyncedAt = latestSyncAt
	latest[1].LastSyncedAt = latestSyncAt
	if _, err := repo.ApplySync(latest, latestSyncAt); err != nil {
		t.Fatalf("failed to seed latest projection state: %v", err)
	}

	projectionID, err := codexmanager.ProjectionIDForAccountID(seedAccountBravo)
	if err != nil {
		t.Fatalf("failed to build bravo projection id: %v", err)
	}
	if _, err := repo.SetRelayEnabled(projectionID, false, latestSyncAt.Add(time.Minute)); err != nil {
		t.Fatalf("failed to apply relay overlay: %v", err)
	}
}

func performJSONRequest(t *testing.T, router http.Handler, method, path string) map[string]any {
	t.Helper()

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d for %s, got %d with body %s", http.StatusOK, path, recorder.Code, recorder.Body.String())
	}
	return decodeJSONMap(t, recorder.Body.Bytes())
}

func decodeJSONMap(t *testing.T, raw []byte) map[string]any {
	t.Helper()

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("failed to decode json payload: %v", err)
	}
	return payload
}

func readAndCloseResponseBodyBytes(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}
	return body
}

func requireObjectField(t *testing.T, payload map[string]any, key string) map[string]any {
	t.Helper()

	value, ok := payload[key]
	if !ok {
		t.Fatalf("expected key %q to exist", key)
	}
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected key %q to be an object, got %#v", key, value)
	}
	return object
}

func requireArrayField(t *testing.T, payload map[string]any, key string) []any {
	t.Helper()

	value, ok := payload[key]
	if !ok {
		t.Fatalf("expected key %q to exist", key)
	}
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("expected key %q to be an array, got %#v", key, value)
	}
	return items
}

func requireObjectValue(t *testing.T, value any, label string) map[string]any {
	t.Helper()

	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected %s to be an object, got %#v", label, value)
	}
	return object
}

func assertAccountShape(t *testing.T, payload map[string]any) {
	t.Helper()

	for _, key := range []string{"accountId", "label", "groupName", "status", "sort", "relayEnabled", "runtimeSource", "runtimeIncluded", "usageSummary", "lastSyncedAt", "stale"} {
		if _, exists := payload[key]; !exists {
			t.Fatalf("expected account payload to include %q", key)
		}
	}
	assertStringField(t, payload, "runtimeSource", codexmanager.RuntimeSourceCodexManager)
	for _, forbidden := range []string{"auth_index", "authIndex", "token", "accessToken", "refreshToken", "workspaceHeader"} {
		if _, exists := payload[forbidden]; exists {
			t.Fatalf("account payload leaked forbidden key %q", forbidden)
		}
	}
}

func assertUsageSummary(t *testing.T, value any, expectedStatus string, expectedUsedPercent int64, expectedWindowMinutes int64) {
	t.Helper()

	summary, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected usageSummary object, got %#v", value)
	}
	assertStringField(t, summary, "availabilityStatus", expectedStatus)
	assertNumberField(t, summary, "usedPercent", expectedUsedPercent)
	assertNumberField(t, summary, "windowMinutes", expectedWindowMinutes)
	if capturedAt, ok := summary["capturedAt"].(string); !ok || capturedAt == "" {
		t.Fatalf("expected usageSummary.capturedAt to be a non-empty string, got %#v", summary["capturedAt"])
	}
}

func assertBoolField(t *testing.T, payload map[string]any, key string, expected bool) {
	t.Helper()

	value, ok := payload[key].(bool)
	if !ok {
		t.Fatalf("expected %q to be a bool, got %#v", key, payload[key])
	}
	if value != expected {
		t.Fatalf("expected %q=%v, got %v", key, expected, value)
	}
}

func assertStringField(t *testing.T, payload map[string]any, key, expected string) {
	t.Helper()

	value, ok := payload[key].(string)
	if !ok {
		t.Fatalf("expected %q to be a string, got %#v", key, payload[key])
	}
	if value != expected {
		t.Fatalf("expected %q=%q, got %q", key, expected, value)
	}
}

func assertNumberField(t *testing.T, payload map[string]any, key string, expected int64) {
	t.Helper()

	value, ok := payload[key].(float64)
	if !ok {
		t.Fatalf("expected %q to be numeric, got %#v", key, payload[key])
	}
	if int64(value) != expected {
		t.Fatalf("expected %q=%d, got %v", key, expected, value)
	}
}

func timePtr(value time.Time) *time.Time {
	return &value
}
