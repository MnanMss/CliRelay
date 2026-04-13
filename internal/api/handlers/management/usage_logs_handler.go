package management

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
)

const authFileGroupTrendCacheTTL = 30 * time.Second

type authFileGroupTrendResponse struct {
	Days   int                     `json:"days"`
	Group  string                  `json:"group"`
	Points []usage.DailyCountPoint `json:"points"`
}

// GetUsageLogs returns paginated, filterable request log entries from SQLite.
// It enriches each log item with resolved api_key_name and channel_name
// from the in-memory config, eliminating the need for multiple frontend API calls.
func (h *Handler) GetUsageLogs(c *gin.Context) {
	// Build name maps from config and auth store first so channel filtering can resolve
	// to stable auth_index values (and reflect renamed OAuth channels).
	keyNameMap, channelNameMap, authIndexChannelMap := h.buildNameMaps()

	channelFilterRaw := strings.TrimSpace(c.Query("channel"))
	if channelFilterRaw == "" {
		channelFilterRaw = strings.TrimSpace(c.Query("channel_name"))
	}
	if channelFilterRaw == "" {
		channelFilterRaw = strings.TrimSpace(c.Query("channel-name"))
	}
	selectedChannelKeys := make(map[string]struct{})
	if channelFilterRaw != "" {
		for _, part := range strings.Split(channelFilterRaw, ",") {
			key := strings.ToLower(strings.TrimSpace(part))
			if key == "" {
				continue
			}
			selectedChannelKeys[key] = struct{}{}
		}
	}
	var authIndexes []string
	if len(selectedChannelKeys) > 0 {
		for idx, name := range authIndexChannelMap {
			key := strings.ToLower(strings.TrimSpace(name))
			if key == "" {
				continue
			}
			if _, ok := selectedChannelKeys[key]; ok {
				authIndexes = append(authIndexes, idx)
			}
		}
		// No matches should yield an empty result set rather than "no filter".
		if len(authIndexes) == 0 {
			authIndexes = []string{""}
		}
	}

	params := usage.LogQueryParams{
		Page:        intQueryDefault(c, "page", 1),
		Size:        intQueryDefault(c, "size", 50),
		Days:        intQueryDefault(c, "days", 7),
		APIKey:      strings.TrimSpace(c.Query("api_key")),
		Model:       strings.TrimSpace(c.Query("model")),
		Status:      strings.TrimSpace(c.Query("status")),
		AuthIndexes: authIndexes,
	}

	result, err := usage.QueryLogs(params)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	filters, err := usage.QueryFilters(params.Days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	stats, err := usage.QueryStats(params)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Enrich log items with resolved names
	for i := range result.Items {
		item := &result.Items[i]
		if item.APIKeyName == "" {
			if name, ok := keyNameMap[item.APIKey]; ok {
				item.APIKeyName = name
			}
		}
		// Prefer the current auth-index derived channel name so renamed OAuth
		// channels are reflected in logs immediately without rewriting history.
		if name, ok := authIndexChannelMap[item.AuthIndex]; ok && strings.TrimSpace(name) != "" {
			item.ChannelName = name
			continue
		}
		// Fall back to source-based mapping when the stored log channel is empty.
		if item.ChannelName == "" {
			if name, ok := channelNameMap[item.Source]; ok {
				item.ChannelName = name
			}
		}
	}

	// Enrich filter options with key names
	filters.APIKeyNames = make(map[string]string, len(filters.APIKeys))
	for _, key := range filters.APIKeys {
		if name, ok := keyNameMap[key]; ok {
			filters.APIKeyNames[key] = name
		}
	}
	// Add channel filter options from current auth snapshot.
	if len(authIndexChannelMap) > 0 {
		seen := make(map[string]struct{})
		channels := make([]string, 0, len(authIndexChannelMap))
		for _, name := range authIndexChannelMap {
			trimmed := strings.TrimSpace(name)
			key := strings.ToLower(trimmed)
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			channels = append(channels, trimmed)
		}
		sort.Slice(channels, func(i, j int) bool { return strings.ToLower(channels[i]) < strings.ToLower(channels[j]) })
		filters.Channels = channels
	}

	// Defensive: ensure JSON arrays are never encoded as null.
	if result.Items == nil {
		result.Items = make([]usage.LogRow, 0)
	}
	if filters.APIKeys == nil {
		filters.APIKeys = make([]string, 0)
	}
	if filters.Models == nil {
		filters.Models = make([]string, 0)
	}
	if filters.Channels == nil {
		filters.Channels = make([]string, 0)
	}
	if filters.APIKeyNames == nil {
		filters.APIKeyNames = make(map[string]string)
	}

	c.JSON(http.StatusOK, gin.H{
		"items":   result.Items,
		"total":   result.Total,
		"page":    result.Page,
		"size":    result.Size,
		"filters": filters,
		"stats":   stats,
	})
}

// buildNameMaps builds three maps from the current config/auth store:
//  1. keyNameMap:          user-facing api_key → display name
//  2. channelNameMap:      source/api_key/email → channel name
//  3. authIndexChannelMap: auth_index → current channel name
func (h *Handler) buildNameMaps() (keyNameMap, channelNameMap, authIndexChannelMap map[string]string) {
	keyNameMap = make(map[string]string)
	channelNameMap = make(map[string]string)
	authIndexChannelMap = make(map[string]string)

	// User-facing API key names from SQLite
	for _, row := range usage.ListAPIKeys() {
		if row.Key != "" && row.Name != "" {
			keyNameMap[row.Key] = row.Name
		}
	}

	cfg := h.cfg
	if cfg != nil {
		for _, k := range cfg.GeminiKey {
			if k.APIKey != "" && k.Name != "" {
				channelNameMap[k.APIKey] = k.Name
			}
		}
		for _, k := range cfg.ClaudeKey {
			if k.APIKey != "" && k.Name != "" {
				channelNameMap[k.APIKey] = k.Name
			}
		}
		for _, k := range cfg.CodexKey {
			if k.APIKey != "" && k.Name != "" {
				channelNameMap[k.APIKey] = k.Name
			}
		}
		// Vertex keys: no Name field, skip

		// OpenAI compatibility: provider name applies to all its API keys
		for _, provider := range cfg.OpenAICompatibility {
			if provider.Name == "" {
				continue
			}
			for _, entry := range provider.APIKeyEntries {
				if entry.APIKey != "" {
					channelNameMap[entry.APIKey] = provider.Name
				}
			}
		}
	}

	if h.authManager != nil {
		for _, auth := range h.authManager.List() {
			if auth == nil {
				continue
			}
			channel := strings.TrimSpace(auth.ChannelName())
			if channel == "" {
				continue
			}
			auth.EnsureIndex()
			if idx := strings.TrimSpace(auth.Index); idx != "" {
				authIndexChannelMap[idx] = channel
			}
			if accountType, account := auth.AccountInfo(); strings.EqualFold(accountType, "oauth") {
				if source := strings.TrimSpace(account); source != "" {
					channelNameMap[source] = channel
				}
			}
			if email := strings.TrimSpace(authEmail(auth)); email != "" {
				channelNameMap[email] = channel
			}
		}
	}

	return
}

func intQueryDefault(c *gin.Context, key string, def int) int {
	v := strings.TrimSpace(c.Query(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return def
	}
	return n
}

func normalizeLogContentFormat(c *gin.Context) string {
	format := strings.ToLower(strings.TrimSpace(c.Query("format")))
	if format == "" {
		return "json"
	}
	switch format {
	case "json", "text":
		return format
	default:
		return "json"
	}
}

func normalizeLogContentPartQuery(c *gin.Context) string {
	part := strings.ToLower(strings.TrimSpace(c.Query("part")))
	if part == "" {
		return "both"
	}
	switch part {
	case "both", "input", "output":
		return part
	default:
		return "both"
	}
}

// GetLogContent returns the stored request/response content for a single log entry.
func (h *Handler) GetLogContent(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil || id < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid log id"})
		return
	}

	part := normalizeLogContentPartQuery(c)
	format := normalizeLogContentFormat(c)

	if format == "text" && part == "both" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "format=text requires part=input or part=output"})
		return
	}

	if part == "both" {
		result, err := usage.QueryLogContent(id)
		if err != nil {
			if strings.Contains(err.Error(), "no rows") {
				c.JSON(http.StatusNotFound, gin.H{"error": "log entry not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, result)
		return
	}

	result, err := usage.QueryLogContentPart(id, part)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			c.JSON(http.StatusNotFound, gin.H{"error": "log entry not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if format == "text" {
		c.Header("Content-Type", "text/plain; charset=utf-8")
		c.Header("X-Log-Id", strconv.FormatInt(result.ID, 10))
		c.Header("X-Log-Part", result.Part)
		if strings.TrimSpace(result.Model) != "" {
			c.Header("X-Model", result.Model)
		}
		c.String(http.StatusOK, result.Content)
		return
	}

	c.JSON(http.StatusOK, result)
}

// GetPublicUsageLogs returns paginated request log entries for a specific API key.
// This is a public endpoint (no management key required) that strips sensitive
// fields (source/auth_index/channel_name) before returning.
func (h *Handler) GetPublicUsageLogs(c *gin.Context) {
	apiKey := strings.TrimSpace(c.Query("api_key"))
	if apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "api_key parameter is required"})
		return
	}

	params := usage.LogQueryParams{
		Page:   intQueryDefault(c, "page", 1),
		Size:   intQueryDefault(c, "size", 50),
		Days:   intQueryDefault(c, "days", 7),
		APIKey: apiKey,
		Model:  strings.TrimSpace(c.Query("model")),
		Status: strings.TrimSpace(c.Query("status")),
	}

	result, err := usage.QueryLogs(params)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	stats, err := usage.QueryStats(params)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// SECURITY: Strip sensitive fields from public response
	for i := range result.Items {
		result.Items[i].Source = ""
		result.Items[i].AuthIndex = ""
		result.Items[i].ChannelName = ""
		result.Items[i].APIKey = ""
		result.Items[i].APIKeyName = ""
	}

	// Model filter options (scoped to this api_key via QueryFilters with key filter)
	models, _ := usage.QueryModelsForKey(apiKey, params.Days)
	if models == nil {
		models = make([]string, 0)
	}

	c.JSON(http.StatusOK, gin.H{
		"items": result.Items,
		"total": result.Total,
		"page":  result.Page,
		"size":  result.Size,
		"stats": stats,
		"filters": gin.H{
			"models": models,
		},
	})
}

// GetPublicUsageChartData returns pre-aggregated chart data for a specific API key.
// This is a public endpoint (no management key required) that provides lightweight
// daily series and model distribution data for rendering charts.
func (h *Handler) GetPublicUsageChartData(c *gin.Context) {
	apiKey := strings.TrimSpace(c.Query("api_key"))
	if apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "api_key parameter is required"})
		return
	}

	days := intQueryDefault(c, "days", 7)

	daily, err := usage.QueryDailySeries(apiKey, days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if daily == nil {
		daily = []usage.DailySeriesPoint{}
	}

	models, err := usage.QueryModelDistribution(apiKey, days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if models == nil {
		models = []usage.ModelDistributionPoint{}
	}

	// Also fetch stats for KPI cards
	stats, err := usage.QueryStats(usage.LogQueryParams{APIKey: apiKey, Days: days})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"daily_series":       daily,
		"model_distribution": models,
		"stats":              stats,
	})
}

// GetPublicLogContent returns the stored request/response content for a single log entry,
// but only if it belongs to the specified API key. This is a public endpoint.
func (h *Handler) GetPublicLogContent(c *gin.Context) {
	apiKey := strings.TrimSpace(c.Query("api_key"))
	if apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "api_key parameter is required"})
		return
	}

	idStr := c.Param("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil || id < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid log id"})
		return
	}

	part := normalizeLogContentPartQuery(c)
	format := normalizeLogContentFormat(c)

	if format == "text" && part == "both" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "format=text requires part=input or part=output"})
		return
	}

	if part == "both" {
		result, err := usage.QueryLogContentForKey(id, apiKey)
		if err != nil {
			if strings.Contains(err.Error(), "no rows") {
				c.JSON(http.StatusNotFound, gin.H{"error": "log entry not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, result)
		return
	}

	result, err := usage.QueryLogContentPartForKey(id, apiKey, part)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			c.JSON(http.StatusNotFound, gin.H{"error": "log entry not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if format == "text" {
		c.Header("Content-Type", "text/plain; charset=utf-8")
		c.Header("X-Log-Id", strconv.FormatInt(result.ID, 10))
		c.Header("X-Log-Part", result.Part)
		if strings.TrimSpace(result.Model) != "" {
			c.Header("X-Model", result.Model)
		}
		c.String(http.StatusOK, result.Content)
		return
	}

	c.JSON(http.StatusOK, result)
}

// GetUsageChartData returns pre-aggregated chart data for the management portal.
// It applies an optional apiKey filter.
func (h *Handler) GetUsageChartData(c *gin.Context) {
	apiKey := strings.TrimSpace(c.Query("api_key"))
	days := intQueryDefault(c, "days", 7)

	daily, err := usage.QueryDailySeries(apiKey, days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if daily == nil {
		daily = []usage.DailySeriesPoint{}
	}

	models, err := usage.QueryModelDistribution(apiKey, days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if models == nil {
		models = []usage.ModelDistributionPoint{}
	}

	hourlyTokens, hourlyModels, err := usage.QueryHourlySeries(apiKey, 24)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if hourlyTokens == nil {
		hourlyTokens = []usage.HourlyTokenPoint{}
	}
	if hourlyModels == nil {
		hourlyModels = []usage.HourlyModelPoint{}
	}

	// API Key distribution (only when not filtered by a single key)
	var apikeyDist []usage.APIKeyDistributionPoint
	if apiKey == "" {
		apikeyDist, err = usage.QueryAPIKeyDistribution(days)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// Fallback: for older logs where api_key_name was not yet stored,
		// enrich with display names from the current config.
		keyNameMap, _, _ := h.buildNameMaps()
		for i := range apikeyDist {
			if apikeyDist[i].Name == "" {
				if name, ok := keyNameMap[apikeyDist[i].APIKey]; ok {
					apikeyDist[i].Name = name
				}
			}
		}
	}
	if apikeyDist == nil {
		apikeyDist = []usage.APIKeyDistributionPoint{}
	}

	c.JSON(http.StatusOK, gin.H{
		"daily_series":        daily,
		"model_distribution":  models,
		"hourly_tokens":       hourlyTokens,
		"hourly_models":       hourlyModels,
		"apikey_distribution": apikeyDist,
	})
}

// GetEntityUsageStats returns aggregated statistics grouped by source or auth_index
func (h *Handler) GetEntityUsageStats(c *gin.Context) {
	apiKey := strings.TrimSpace(c.Query("api_key"))
	days := intQueryDefault(c, "days", 7)

	sourceStats, err := usage.QueryEntityStats(apiKey, days, "source")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if sourceStats == nil {
		sourceStats = []usage.EntityStatPoint{}
	}

	authIndexStats, err := usage.QueryEntityStats(apiKey, days, "auth_index")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if authIndexStats == nil {
		authIndexStats = []usage.EntityStatPoint{}
	}

	c.JSON(http.StatusOK, gin.H{
		"source":     sourceStats,
		"auth_index": authIndexStats,
	})
}

func (h *Handler) GetAuthFileGroupTrend(c *gin.Context) {
	group := strings.ToLower(strings.TrimSpace(c.Query("group")))
	if group == "" {
		group = "all"
	}
	days := intQueryDefault(c, "days", 7)
	if days > 7 {
		days = 7
	}

	cacheKey := group + ":" + strconv.Itoa(days)
	if cached, ok := h.getTrendCache(cacheKey); ok {
		c.JSON(http.StatusOK, cached)
		return
	}

	authIndexes := h.authIndexesForProviderGroup(group)
	points, err := usage.QueryDailyCallsByAuthIndexes(authIndexes, days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if points == nil {
		points = []usage.DailyCountPoint{}
	}
	payload := authFileGroupTrendResponse{Days: days, Group: group, Points: points}
	h.setTrendCache(cacheKey, payload)
	c.JSON(http.StatusOK, payload)
}

func (h *Handler) authIndexesForProviderGroup(group string) []string {
	if h == nil || h.authManager == nil {
		return []string{}
	}
	auths := h.authManager.List()
	indexes := make([]string, 0, len(auths))
	for _, auth := range auths {
		if auth == nil {
			continue
		}
		provider := strings.ToLower(strings.TrimSpace(auth.Provider))
		if group != "all" && provider != group {
			continue
		}
		auth.EnsureIndex()
		if idx := strings.TrimSpace(auth.Index); idx != "" {
			indexes = append(indexes, idx)
		}
	}
	return indexes
}

func (h *Handler) getTrendCache(key string) (authFileGroupTrendResponse, bool) {
	if h == nil {
		return authFileGroupTrendResponse{}, false
	}
	h.trendCacheMu.Lock()
	defer h.trendCacheMu.Unlock()
	entry, ok := h.trendCache[key]
	if !ok || time.Now().After(entry.expiresAt) {
		if ok {
			delete(h.trendCache, key)
		}
		return authFileGroupTrendResponse{}, false
	}
	payload, ok := entry.payload.(authFileGroupTrendResponse)
	return payload, ok
}

func (h *Handler) setTrendCache(key string, payload authFileGroupTrendResponse) {
	if h == nil {
		return
	}
	h.trendCacheMu.Lock()
	defer h.trendCacheMu.Unlock()
	if h.trendCache == nil {
		h.trendCache = make(map[string]trendCacheEntry)
	}
	now := time.Now()
	for k, entry := range h.trendCache {
		if now.After(entry.expiresAt) {
			delete(h.trendCache, k)
		}
	}
	h.trendCache[key] = trendCacheEntry{expiresAt: now.Add(authFileGroupTrendCacheTTL), payload: payload}
}
