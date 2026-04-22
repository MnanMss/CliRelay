package management

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
)

const (
	imageGenerationModel = "gpt-image-2"
)

func (h *Handler) PostImageGenerationTest(c *gin.Context) {
	var body struct {
		Model  string `json:"model"`
		Prompt string `json:"prompt"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	model := strings.TrimSpace(body.Model)
	if model == "" {
		model = imageGenerationModel
	}
	prompt := strings.TrimSpace(body.Prompt)
	if prompt == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "prompt is required"})
		return
	}
	if h == nil || h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "auth manager unavailable"})
		return
	}

	payload, _ := json.Marshal(map[string]any{"model": model, "prompt": prompt})
	cliCtx := context.WithValue(c.Request.Context(), util.ContextKeyGin, c)
	resp, err := h.authManager.Execute(cliCtx, []string{"codex"}, coreexecutor.Request{
		Model:   "",
		Payload: payload,
		Format:  sdktranslator.FromString("openai"),
	}, coreexecutor.Options{
		Alt:          "images/generations",
		SourceFormat: sdktranslator.FromString("openai"),
	})
	if err != nil {
		status := http.StatusBadGateway
		if statusErr, ok := err.(coreexecutor.StatusError); ok && statusErr.StatusCode() > 0 {
			status = statusErr.StatusCode()
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	c.Data(http.StatusOK, "application/json; charset=utf-8", resp.Payload)
}

func (h *Handler) ListImageGenerationChannels(c *gin.Context) {
	channels := make([]string, 0)
	seen := make(map[string]struct{})
	if h != nil && h.authManager != nil {
		for _, auth := range h.authManager.List() {
			if auth == nil || auth.Disabled {
				continue
			}
			if !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") {
				continue
			}
			accountType, _ := auth.AccountInfo()
			if !strings.EqualFold(strings.TrimSpace(accountType), "oauth") {
				continue
			}
			if auth.Status == coreauth.StatusDisabled {
				continue
			}
			name := strings.TrimSpace(auth.ChannelName())
			if name == "" {
				continue
			}
			key := strings.ToLower(name)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			channels = append(channels, name)
		}
	}
	sort.Strings(channels)
	c.JSON(http.StatusOK, gin.H{
		"model":    imageGenerationModel,
		"channels": channels,
	})
}
