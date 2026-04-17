package management

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/buildinfo"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

const (
	updateHTTPTimeout     = 10 * time.Second
	updaterHealthTimeout  = 2 * time.Second
	updaterTokenEnv       = "CLIRELAY_UPDATER_TOKEN"
	autoUpdateChannelEnv  = "CLIRELAY_UPDATE_CHANNEL"
	defaultUpdaterService = "clirelay"
)

type updateCheckResponse struct {
	Enabled           bool   `json:"enabled"`
	CurrentVersion    string `json:"current_version"`
	CurrentCommit     string `json:"current_commit"`
	CurrentUIVersion  string `json:"current_ui_version,omitempty"`
	CurrentUICommit   string `json:"current_ui_commit,omitempty"`
	BuildDate         string `json:"build_date"`
	TargetChannel     string `json:"target_channel"`
	LatestVersion     string `json:"latest_version"`
	LatestCommit      string `json:"latest_commit"`
	LatestCommitURL   string `json:"latest_commit_url,omitempty"`
	LatestUIVersion   string `json:"latest_ui_version,omitempty"`
	LatestUICommit    string `json:"latest_ui_commit,omitempty"`
	LatestUICommitURL string `json:"latest_ui_commit_url,omitempty"`
	DockerImage       string `json:"docker_image"`
	DockerTag         string `json:"docker_tag"`
	ReleaseNotes      string `json:"release_notes,omitempty"`
	ReleaseURL        string `json:"release_url,omitempty"`
	UpdateAvailable   bool   `json:"update_available"`
	UpdaterAvailable  bool   `json:"updater_available"`
	Message           string `json:"message,omitempty"`
}

type branchCommitInfo struct {
	SHA     string `json:"sha"`
	HTMLURL string `json:"html_url"`
	Commit  struct {
		Message string `json:"message"`
	} `json:"commit"`
}

func (h *Handler) GetAutoUpdateEnabled(c *gin.Context) {
	enabled := true
	if h != nil && h.cfg != nil {
		enabled = h.cfg.AutoUpdate.Enabled
	}
	c.JSON(http.StatusOK, gin.H{"enabled": enabled})
}

func (h *Handler) PutAutoUpdateEnabled(c *gin.Context) {
	h.updateBoolField(c, func(v bool) { h.cfg.AutoUpdate.Enabled = v })
}

func (h *Handler) GetAutoUpdateChannel(c *gin.Context) {
	channel := config.DefaultAutoUpdateChannel
	if h != nil && h.cfg != nil {
		h.cfg.SanitizeAutoUpdate()
		channel = h.cfg.AutoUpdate.Channel
	}
	c.JSON(http.StatusOK, gin.H{"channel": channel})
}

func (h *Handler) PutAutoUpdateChannel(c *gin.Context) {
	var body struct {
		Value *string `json:"value"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Value == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	channel := normalizeAutoUpdateChannel(*body.Value)
	if channel == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid auto update channel"})
		return
	}
	h.cfg.AutoUpdate.Channel = channel
	h.persist(c)
}

func (h *Handler) CheckUpdate(c *gin.Context) {
	resp, err := h.buildUpdateCheck(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "update_check_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handler) ApplyUpdate(c *gin.Context) {
	if h == nil || h.cfg == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "config_unavailable"})
		return
	}
	if !h.cfg.AutoUpdate.Enabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "auto_update_disabled"})
		return
	}

	check, err := h.buildUpdateCheck(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "update_check_failed", "message": err.Error()})
		return
	}
	if !check.UpdateAvailable {
		c.JSON(http.StatusOK, gin.H{"status": "noop", "message": "already up to date"})
		return
	}

	payload := map[string]string{
		"image":   check.DockerImage,
		"tag":     check.DockerTag,
		"channel": check.TargetChannel,
		"version": check.LatestVersion,
		"commit":  check.LatestCommit,
		"service": updaterTargetService(),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "marshal_failed", "message": err.Error()})
		return
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, joinURLPath(resolveUpdaterURL(h.cfg), "/v1/update"), bytes.NewReader(body))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "request_create_failed", "message": err.Error()})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if token := updaterToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: updateHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "updater_unreachable", "message": err.Error()})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		c.JSON(http.StatusBadGateway, gin.H{"error": "updater_failed", "message": strings.TrimSpace(string(data))})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"status": "accepted", "target": check})
}

func (h *Handler) buildUpdateCheck(ctx context.Context) (*updateCheckResponse, error) {
	cfg := &config.Config{}
	if h != nil && h.cfg != nil {
		cfg = h.cfg
	}
	cfg.SanitizeAutoUpdate()

	channel := cfg.AutoUpdate.Channel
	if channel == "auto" {
		channel = inferAutoUpdateChannel(buildinfo.Version, os.Getenv(autoUpdateChannelEnv))
	}
	repo := normalizeGitHubRepository(cfg.AutoUpdate.Repository)
	frontendRepo := normalizeGitHubRepository(config.DefaultPanelGitHubRepository)
	client := h.githubClient()

	branch, err := fetchBranchCommit(ctx, client, repo, channel)
	if err != nil {
		return nil, err
	}
	frontendBranch, err := fetchBranchCommit(ctx, client, frontendRepo, channel)
	if err != nil {
		return nil, err
	}

	release, releaseErr := fetchLatestReleaseInfo(ctx, client, repo)
	releaseNotes := strings.TrimSpace(release.Body)
	if releaseErr != nil {
		releaseNotes = ""
	}

	resp := &updateCheckResponse{
		Enabled:           cfg.AutoUpdate.Enabled,
		CurrentVersion:    currentUpdateDisplayVersion(buildinfo.Version),
		CurrentCommit:     buildinfo.Commit,
		CurrentUIVersion:  currentFrontendDisplayVersion(buildinfo.FrontendVersion, buildinfo.FrontendRef, buildinfo.FrontendCommit),
		CurrentUICommit:   buildinfo.FrontendCommit,
		BuildDate:         buildinfo.BuildDate,
		TargetChannel:     channel,
		LatestVersion:     latestUpdateDisplayVersion(channel, branch.SHA),
		LatestCommit:      strings.TrimSpace(branch.SHA),
		LatestCommitURL:   strings.TrimSpace(branch.HTMLURL),
		LatestUIVersion:   latestFrontendDisplayVersion(channel, frontendBranch.SHA),
		LatestUICommit:    strings.TrimSpace(frontendBranch.SHA),
		LatestUICommitURL: strings.TrimSpace(frontendBranch.HTMLURL),
		DockerImage:       cfg.AutoUpdate.DockerImage,
		DockerTag:         dockerTagForChannel(channel, branch.SHA),
		ReleaseNotes:      releaseNotes,
		ReleaseURL:        strings.TrimSpace(release.HTMLURL),
		UpdateAvailable: cfg.AutoUpdate.Enabled && autoUpdateAvailable(
			buildinfo.Commit,
			branch.SHA,
			buildinfo.FrontendCommit,
			frontendBranch.SHA,
		),
		UpdaterAvailable: checkUpdaterAvailable(ctx, cfg),
	}
	if !resp.Enabled {
		resp.Message = "auto update disabled"
	} else if !resp.UpdateAvailable {
		resp.Message = "already up to date"
	}
	return resp, nil
}

func (h *Handler) githubClient() *http.Client {
	client := &http.Client{Timeout: updateHTTPTimeout}
	if h != nil && h.cfg != nil {
		proxyURL := strings.TrimSpace(h.cfg.ProxyURL)
		if proxyURL != "" {
			util.SetProxy(&sdkconfig.SDKConfig{ProxyURL: proxyURL}, client)
		}
	}
	return client
}

func normalizeAutoUpdateChannel(channel string) string {
	switch strings.ToLower(strings.TrimSpace(channel)) {
	case "main", "dev", "auto":
		return strings.ToLower(strings.TrimSpace(channel))
	default:
		return ""
	}
}

func fetchBranchCommit(ctx context.Context, client *http.Client, repo string, channel string) (branchCommitInfo, error) {
	var info branchCommitInfo
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubAPIURL(repo, "commits/"+channel), nil)
	if err != nil {
		return info, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", latestReleaseUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return info, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return info, fmt.Errorf("github commit status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return info, err
	}
	if strings.TrimSpace(info.SHA) == "" {
		return info, fmt.Errorf("github commit response missing sha")
	}
	return info, nil
}

func fetchLatestReleaseInfo(ctx context.Context, client *http.Client, repo string) (releaseInfo, error) {
	var info releaseInfo
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubAPIURL(repo, "releases/latest"), nil)
	if err != nil {
		return info, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", latestReleaseUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return info, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return info, fmt.Errorf("github release status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return info, json.NewDecoder(resp.Body).Decode(&info)
}

func inferAutoUpdateChannel(version string, envChannel string) string {
	env := strings.ToLower(strings.TrimSpace(envChannel))
	if env == "dev" || env == "main" {
		return env
	}
	lowered := strings.ToLower(strings.TrimSpace(version))
	if strings.HasPrefix(lowered, "dev-") || strings.Contains(lowered, "-dev") || lowered == "dev" {
		return "dev"
	}
	return "main"
}

func currentUpdateDisplayVersion(version string) string {
	trimmed := strings.TrimSpace(version)
	return trimmed
}

func latestUpdateDisplayVersion(channel string, commit string) string {
	normalized := normalizeAutoUpdateChannel(channel)
	if normalized == "dev" {
		return joinChannelCommit("dev", commit)
	}
	return joinChannelCommit("main", commit)
}

func currentFrontendDisplayVersion(version string, ref string, commit string) string {
	trimmed := strings.TrimSpace(version)
	if trimmed != "" && !strings.EqualFold(trimmed, "dev") {
		return trimmed
	}
	normalizedRef := normalizeAutoUpdateChannel(ref)
	if normalizedRef == "auto" || normalizedRef == "" {
		normalizedRef = "main"
	}
	return latestFrontendDisplayVersion(normalizedRef, commit)
}

func latestFrontendDisplayVersion(channel string, commit string) string {
	normalized := normalizeAutoUpdateChannel(channel)
	if normalized == "dev" {
		return "panel-" + joinChannelCommit("dev", commit)
	}
	return "panel-" + joinChannelCommit("main", commit)
}

func joinChannelCommit(channel string, commit string) string {
	short := shortCommit(commit)
	if short == "" {
		return channel
	}
	return channel + "-" + short
}

func shortCommit(commit string) string {
	trimmed := strings.TrimSpace(commit)
	if len(trimmed) > 7 {
		return trimmed[:7]
	}
	return trimmed
}

func autoUpdateAvailableFromCommit(currentCommit string, latestCommit string) bool {
	current := strings.TrimSpace(currentCommit)
	latest := strings.TrimSpace(latestCommit)
	if latest == "" {
		return false
	}
	if current == "" || strings.EqualFold(current, "none") {
		return true
	}
	current = strings.ToLower(current)
	latest = strings.ToLower(latest)
	return !(strings.HasPrefix(latest, current) || strings.HasPrefix(current, latest))
}

func autoUpdateAvailable(currentBackendCommit string, latestBackendCommit string, currentFrontendCommit string, latestFrontendCommit string) bool {
	return autoUpdateAvailableFromCommit(currentBackendCommit, latestBackendCommit) ||
		autoUpdateAvailableFromCommit(currentFrontendCommit, latestFrontendCommit)
}

func dockerTagForChannel(channel string, _ string) string {
	if strings.EqualFold(strings.TrimSpace(channel), "dev") {
		return "dev"
	}
	return "latest"
}

func normalizeGitHubRepository(repo string) string {
	trimmed := strings.TrimSpace(repo)
	if trimmed == "" {
		return "kittors/CliRelay"
	}
	if parsed, err := url.Parse(trimmed); err == nil && parsed.Host != "" {
		trimmed = strings.Trim(parsed.Path, "/")
	}
	trimmed = strings.TrimPrefix(trimmed, "repos/")
	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	if len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}
	return "kittors/CliRelay"
}

func githubAPIURL(repo string, path string) string {
	return "https://api.github.com/repos/" + strings.Trim(repo, "/") + "/" + strings.TrimLeft(path, "/")
}

func resolveUpdaterURL(cfg *config.Config) string {
	if fromEnv := strings.TrimSpace(os.Getenv("CLIRELAY_UPDATER_URL")); fromEnv != "" {
		return fromEnv
	}
	if cfg != nil && cfg.AutoUpdate.UpdaterURL != "" {
		return cfg.AutoUpdate.UpdaterURL
	}
	return config.DefaultAutoUpdateUpdaterURL
}

func updaterToken() string {
	return strings.TrimSpace(os.Getenv(updaterTokenEnv))
}

func updaterTargetService() string {
	if service := strings.TrimSpace(os.Getenv("CLIRELAY_TARGET_SERVICE")); service != "" {
		return service
	}
	return defaultUpdaterService
}

func checkUpdaterAvailable(ctx context.Context, cfg *config.Config) bool {
	ctx, cancel := context.WithTimeout(ctx, updaterHealthTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, joinURLPath(resolveUpdaterURL(cfg), "/v1/health"), nil)
	if err != nil {
		return false
	}
	if token := updaterToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func joinURLPath(base string, path string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(base), "/")
	if trimmed == "" {
		trimmed = config.DefaultAutoUpdateUpdaterURL
	}
	return trimmed + "/" + strings.TrimLeft(path, "/")
}
