package management

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

type identityFingerprintResponse struct {
	IdentityFingerprint config.IdentityFingerprintConfig `json:"identity-fingerprint"`
	Defaults            config.IdentityFingerprintConfig `json:"defaults"`
}

func (h *Handler) GetIdentityFingerprint(c *gin.Context) {
	h.mu.Lock()
	current := config.IdentityFingerprintConfig{}
	if h.cfg != nil {
		current = h.cfg.IdentityFingerprint
	}
	h.mu.Unlock()

	current.Codex = config.NormalizeCodexIdentityFingerprint(current.Codex)
	c.JSON(http.StatusOK, identityFingerprintResponse{
		IdentityFingerprint: current,
		Defaults: config.IdentityFingerprintConfig{
			Codex: config.DefaultCodexIdentityFingerprint(),
		},
	})
}

func (h *Handler) PutIdentityFingerprint(c *gin.Context) {
	var body config.IdentityFingerprintConfig
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}

	body.Codex = config.NormalizeCodexIdentityFingerprint(body.Codex)
	if err := validateCodexIdentityFingerprint(body.Codex); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.Codex.Enabled && body.Codex.SessionMode == "fixed" && strings.TrimSpace(body.Codex.SessionID) == "" {
		body.Codex.SessionID = uuid.NewString()
	}

	h.mu.Lock()
	if h.cfg == nil {
		h.cfg = &config.Config{}
	}
	h.cfg.IdentityFingerprint = body
	h.mu.Unlock()

	h.persist(c)
}

func validateCodexIdentityFingerprint(fp config.CodexIdentityFingerprintConfig) error {
	if containsHeaderLineBreak(fp.UserAgent) || containsHeaderLineBreak(fp.Version) ||
		containsHeaderLineBreak(fp.Originator) || containsHeaderLineBreak(fp.WebsocketBeta) ||
		containsHeaderLineBreak(fp.SessionID) {
		return fmt.Errorf("identity fingerprint fields must not contain line breaks")
	}
	for key, value := range fp.CustomHeaders {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			return fmt.Errorf("custom header name cannot be empty")
		}
		if !isHTTPHeaderToken(key) {
			return fmt.Errorf("invalid custom header name: %s", key)
		}
		if isIdentityFingerprintBlockedHeader(key) {
			return fmt.Errorf("custom header %s is managed by the system", key)
		}
		if containsHeaderLineBreak(value) {
			return fmt.Errorf("custom header %s must not contain line breaks", key)
		}
	}
	return nil
}

func containsHeaderLineBreak(value string) bool {
	return strings.ContainsAny(value, "\r\n")
}

func isIdentityFingerprintBlockedHeader(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "authorization", "content-type", "accept", "connection", "chatgpt-account-id",
		"user-agent", "version", "session_id", "session-id", "originator", "openai-beta":
		return true
	default:
		return false
	}
}

func isHTTPHeaderToken(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			continue
		}
		switch r {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
			continue
		default:
			return false
		}
	}
	return true
}
