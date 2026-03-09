package test

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/codexmanager"
)

func TestCodexManagerUsageReadAndRefresh(t *testing.T) {
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
					"accountId":              seedAccountAlpha,
					"availabilityStatus":     "available",
					"usedPercent":            64,
					"windowMinutes":          60,
					"secondaryUsedPercent":   12,
					"secondaryWindowMinutes": 1440,
					"creditsJson":            `{"secret":true}`,
					"capturedAt":             time.Date(2026, time.March, 2, 10, 0, 0, 0, time.UTC).Unix(),
				},
			}, nil
		case seedAccountBravo:
			return nil, errors.New("usage read unavailable")
		default:
			return map[string]any{"snapshot": nil}, nil
		}
	})

	router := newCodexManagerActionRouter(t, stub)

	t.Run("usage_list_prefers_projection_summary_and_local_filters", func(t *testing.T) {
		stub.ResetCalls()

		statusCode, payload, raw := performCodexActionJSONRequest(
			t,
			router,
			http.MethodGet,
			codexmanager.ManagementNamespace+"/usage?page=1&pageSize=1&query=team-blue",
			"",
		)
		if statusCode != http.StatusOK {
			t.Fatalf("expected usage list status %d, got %d: %s", http.StatusOK, statusCode, raw)
		}

		data := requireObjectField(t, payload, "data")
		assertNumberField(t, data, "total", 1)
		assertNumberField(t, data, "page", 1)
		assertNumberField(t, data, "pageSize", 1)
		items := requireArrayField(t, data, "items")
		if len(items) != 1 {
			t.Fatalf("expected 1 usage list item, got %d", len(items))
		}

		item := requireObjectValue(t, items[0], "usage list item")
		assertAccountShape(t, item)
		assertStringField(t, item, "accountId", seedAccountBravo)
		assertUsageSummary(t, item["usageSummary"], "limited", 87, 60)

		calls := stub.GetCalls()
		if got := countCallsByMethod(calls, "account/usage/list"); got != 0 {
			t.Fatalf("expected projection-backed usage list to avoid account/usage/list rpc, got %d call(s)", got)
		}
		if got := countCallsByMethod(calls, "account/usage/read"); got != 0 {
			t.Fatalf("expected usage list to avoid account/usage/read rpc, got %d call(s)", got)
		}
	})

	t.Run("account_usage_read_returns_snapshot_and_hides_credits_json", func(t *testing.T) {
		stub.ResetCalls()

		statusCode, payload, raw := performCodexActionJSONRequest(
			t,
			router,
			http.MethodGet,
			codexmanager.ManagementNamespace+"/accounts/"+seedAccountAlpha+"/usage",
			"",
		)
		if statusCode != http.StatusOK {
			t.Fatalf("expected usage read status %d, got %d: %s", http.StatusOK, statusCode, raw)
		}

		data := requireObjectField(t, payload, "data")
		assertStringField(t, data, "accountId", seedAccountAlpha)
		assertUsageSummary(t, data["usageSummary"], "available", 64, 60)

		snapshot := requireObjectField(t, data, "snapshot")
		assertStringField(t, snapshot, "accountId", seedAccountAlpha)
		assertStringField(t, snapshot, "availabilityStatus", "available")
		assertNumberField(t, snapshot, "usedPercent", 64)
		if _, exists := snapshot["creditsJson"]; exists {
			t.Fatalf("expected usage snapshot to hide creditsJson, got %#v", snapshot["creditsJson"])
		}

		calls := stub.GetCalls()
		if got := countCallsByMethod(calls, "account/usage/read"); got != 1 {
			t.Fatalf("expected 1 account/usage/read call, got %d", got)
		}
		params := MustDecodeParamsAsMap(t, findCallByMethod(t, calls, "account/usage/read", 1).Params)
		AssertParamString(t, params, "accountId", seedAccountAlpha)
	})

	t.Run("account_usage_read_returns_null_snapshot_when_upstream_read_fails", func(t *testing.T) {
		statusCode, payload, raw := performCodexActionJSONRequest(
			t,
			router,
			http.MethodGet,
			codexmanager.ManagementNamespace+"/accounts/"+seedAccountBravo+"/usage",
			"",
		)
		if statusCode != http.StatusOK {
			t.Fatalf("expected usage read status %d, got %d: %s", http.StatusOK, statusCode, raw)
		}

		data := requireObjectField(t, payload, "data")
		assertStringField(t, data, "accountId", seedAccountBravo)
		assertUsageSummary(t, data["usageSummary"], "limited", 87, 60)
		if value, exists := data["snapshot"]; !exists || value != nil {
			t.Fatalf("expected snapshot=null when usage read fails, got %#v", data["snapshot"])
		}
	})

	t.Run("single_refresh_calls_account_scoped_refresh_and_returns_snapshot", func(t *testing.T) {
		stub.ResetCalls()

		statusCode, payload, raw := performCodexActionJSONRequest(
			t,
			router,
			http.MethodPost,
			codexmanager.ManagementNamespace+"/accounts/"+seedAccountAlpha+"/usage/refresh",
			"",
		)
		if statusCode != http.StatusOK {
			t.Fatalf("expected usage refresh status %d, got %d: %s", http.StatusOK, statusCode, raw)
		}

		data := requireObjectField(t, payload, "data")
		assertStringField(t, data, "accountId", seedAccountAlpha)
		assertUsageSummary(t, data["usageSummary"], "available", 64, 60)
		snapshot := requireObjectField(t, data, "snapshot")
		assertStringField(t, snapshot, "accountId", seedAccountAlpha)
		assertNumberField(t, snapshot, "usedPercent", 64)

		calls := stub.GetCalls()
		if got := countCallsByMethod(calls, "account/usage/refresh"); got != 1 {
			t.Fatalf("expected 1 account/usage/refresh call, got %d", got)
		}
		refreshParams := MustDecodeParamsAsMap(t, findCallByMethod(t, calls, "account/usage/refresh", 1).Params)
		AssertParamString(t, refreshParams, "accountId", seedAccountAlpha)
		if got := countCallsByMethod(calls, "account/usage/list"); got != 0 {
			t.Fatalf("expected single refresh flow to avoid account/usage/list rpc, got %d call(s)", got)
		}
	})

	t.Run("account_usage_read_returns_not_found_for_unknown_projection", func(t *testing.T) {
		statusCode, payload, raw := performCodexActionJSONRequest(
			t,
			router,
			http.MethodGet,
			codexmanager.ManagementNamespace+"/accounts/acc-missing/usage",
			"",
		)
		if statusCode != http.StatusNotFound {
			t.Fatalf("expected missing usage read status %d, got %d: %s", http.StatusNotFound, statusCode, raw)
		}
		assertBoolField(t, payload, "ok", false)
		assertStringField(t, payload, "code", codexmanager.CodeAccountNotFound)
	})
}

func TestCodexManagerBatchRefreshPartialFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)

	stub := NewRPCStubServer(t)
	defer stub.Close()

	stub.SetMethodHook("account/usage/refresh", func(req RPCRequestCapture) (any, error) {
		params := MustDecodeParamsAsMap(t, req.Params)
		accountID, _ := params["accountId"].(string)
		switch accountID {
		case seedAccountAlpha:
			return map[string]any{"ok": true}, nil
		case seedAccountBravo:
			return nil, errors.New("usage refresh unavailable")
		default:
			return map[string]any{"ok": true}, nil
		}
	})
	stub.SetMethodHook("account/usage/read", func(req RPCRequestCapture) (any, error) {
		params := MustDecodeParamsAsMap(t, req.Params)
		accountID, _ := params["accountId"].(string)
		if accountID != seedAccountAlpha {
			return map[string]any{"snapshot": nil}, nil
		}
		return map[string]any{
			"snapshot": map[string]any{
				"accountId":          seedAccountAlpha,
				"availabilityStatus": "available",
				"usedPercent":        18,
				"windowMinutes":      60,
				"creditsJson":        `{"secret":true}`,
				"capturedAt":         time.Date(2026, time.March, 2, 11, 0, 0, 0, time.UTC).Unix(),
			},
		}, nil
	})

	router := newCodexManagerActionRouter(t, stub)

	t.Run("subset_refresh_returns_per_item_success_and_failure", func(t *testing.T) {
		stub.ResetCalls()

		statusCode, payload, raw := performCodexActionJSONRequest(
			t,
			router,
			http.MethodPost,
			codexmanager.ManagementNamespace+"/usage/refresh-batch",
			`{"accountIds":["acc-alpha","acc-missing","acc-bravo"]}`,
		)
		if statusCode != http.StatusOK {
			t.Fatalf("expected batch refresh status %d, got %d: %s", http.StatusOK, statusCode, raw)
		}

		data := requireObjectField(t, payload, "data")
		assertNumberField(t, data, "total", 3)
		assertNumberField(t, data, "successCount", 1)
		assertNumberField(t, data, "failedCount", 2)

		items := requireArrayField(t, data, "items")
		if len(items) != 3 {
			t.Fatalf("expected 3 batch refresh items, got %d", len(items))
		}

		first := requireObjectValue(t, items[0], "first batch item")
		assertStringField(t, first, "accountId", seedAccountAlpha)
		assertBoolField(t, first, "success", true)
		if value, exists := first["reason"]; !exists || value != nil {
			t.Fatalf("expected success item reason=null, got %#v", first["reason"])
		}
		assertUsageSummary(t, first["usageSummary"], "available", 18, 60)
		firstSnapshot := requireObjectField(t, first, "snapshot")
		assertStringField(t, firstSnapshot, "accountId", seedAccountAlpha)
		if _, exists := firstSnapshot["creditsJson"]; exists {
			t.Fatalf("expected batch success snapshot to hide creditsJson, got %#v", firstSnapshot["creditsJson"])
		}

		second := requireObjectValue(t, items[1], "second batch item")
		assertStringField(t, second, "accountId", "acc-missing")
		assertBoolField(t, second, "success", false)
		assertStringField(t, second, "reason", codexmanager.ErrAccountNotFound.Error())
		if value, exists := second["snapshot"]; !exists || value != nil {
			t.Fatalf("expected missing-account snapshot=null, got %#v", second["snapshot"])
		}

		third := requireObjectValue(t, items[2], "third batch item")
		assertStringField(t, third, "accountId", seedAccountBravo)
		assertBoolField(t, third, "success", false)
		assertStringField(t, third, "reason", "usage refresh unavailable")
		assertUsageSummary(t, third["usageSummary"], "limited", 87, 60)
		if value, exists := third["snapshot"]; !exists || value != nil {
			t.Fatalf("expected failed refresh snapshot=null, got %#v", third["snapshot"])
		}

		calls := stub.GetCalls()
		refreshCalls := filterCallsByMethod(calls, "account/usage/refresh")
		if len(refreshCalls) != 2 {
			t.Fatalf("expected 2 account/usage/refresh calls for selected local subset, got %d", len(refreshCalls))
		}
		seen := map[string]bool{}
		for i := range refreshCalls {
			params := MustDecodeParamsAsMap(t, refreshCalls[i].Params)
			accountID, _ := params["accountId"].(string)
			if accountID == "" {
				t.Fatalf("expected subset refresh rpc call to include accountId, got params %#v", params)
			}
			seen[accountID] = true
		}
		if !seen[seedAccountAlpha] || !seen[seedAccountBravo] {
			t.Fatalf("expected subset refresh rpc calls for alpha and bravo only, got %#v", seen)
		}
		if got := countCallsByMethod(calls, "account/usage/list"); got != 0 {
			t.Fatalf("expected batch refresh to avoid account/usage/list rpc, got %d call(s)", got)
		}
		if got := countCallsByMethod(calls, "account/usage/read"); got != 1 {
			t.Fatalf("expected only successful refreshes to perform read-back, got %d account/usage/read call(s)", got)
		}
	})

	t.Run("rejects_empty_selection_payload", func(t *testing.T) {
		statusCode, payload, raw := performCodexActionJSONRequest(
			t,
			router,
			http.MethodPost,
			codexmanager.ManagementNamespace+"/usage/refresh-batch",
			`{"accountIds":[]}`,
		)
		if statusCode != http.StatusBadRequest {
			t.Fatalf("expected empty selection status %d, got %d: %s", http.StatusBadRequest, statusCode, raw)
		}
		assertBoolField(t, payload, "ok", false)
		assertStringField(t, payload, "code", codexmanager.CodeBadRequest)
	})
}

func filterCallsByMethod(calls []RPCRequestCapture, method string) []RPCRequestCapture {
	filtered := make([]RPCRequestCapture, 0)
	for i := range calls {
		if calls[i].Method == method {
			filtered = append(filtered, calls[i])
		}
	}
	return filtered
}
