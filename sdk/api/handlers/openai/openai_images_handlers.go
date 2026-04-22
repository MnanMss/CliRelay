package openai

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	internalrouting "github.com/router-for-me/CLIProxyAPI/v6/internal/routing"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const openAIImageGenerationAlt = "images/generations"

type OpenAIImagesAPIHandler struct {
	*handlers.BaseAPIHandler
}

func NewOpenAIImagesAPIHandler(apiHandlers *handlers.BaseAPIHandler) *OpenAIImagesAPIHandler {
	return &OpenAIImagesAPIHandler{BaseAPIHandler: apiHandlers}
}

func (h *OpenAIImagesAPIHandler) Generations(c *gin.Context) {
	rawJSON, ok := handlers.ReadJSONRequestBody(c)
	if !ok {
		return
	}

	modelName := strings.TrimSpace(gjson.GetBytes(rawJSON, "model").String())
	if modelName == "" {
		modelName = "gpt-image-2"
		if updated, err := sjson.SetBytes(rawJSON, "model", modelName); err == nil {
			rawJSON = updated
		}
	}

	cliCtx := context.WithValue(c.Request.Context(), util.ContextKeyGin, c)
	meta := requestImageExecutionMetadata(c)
	if h.AuthManager == nil {
		writeOpenAIImagesError(c, http.StatusInternalServerError, "server_error", "authentication manager not initialized")
		return
	}

	stopKeepAlive := h.StartNonStreamingKeepAlive(c, cliCtx)
	defer stopKeepAlive()

	resp, err := h.AuthManager.Execute(cliCtx, []string{"codex"}, coreexecutor.Request{
		Model:   "",
		Payload: rawJSON,
		Format:  sdktranslator.FromString("openai"),
	}, coreexecutor.Options{
		Alt:             openAIImageGenerationAlt,
		OriginalRequest: rawJSON,
		SourceFormat:    sdktranslator.FromString("openai"),
		Metadata:        meta,
	})
	if err != nil {
		status := http.StatusBadGateway
		if statusErr, ok := err.(coreexecutor.StatusError); ok && statusErr.StatusCode() > 0 {
			status = statusErr.StatusCode()
		}
		writeOpenAIImagesError(c, status, errorTypeForStatus(status), err.Error())
		return
	}

	handlers.WriteUpstreamHeaders(c.Writer.Header(), resp.Headers)
	c.Data(http.StatusOK, "application/json; charset=utf-8", resp.Payload)
}

func requestImageExecutionMetadata(c *gin.Context) map[string]any {
	meta := make(map[string]any)
	if metadataVal, exists := c.Get("accessMetadata"); exists {
		if metadata, ok := metadataVal.(map[string]string); ok {
			if allowedChannels := strings.TrimSpace(metadata["allowed-channels"]); allowedChannels != "" {
				meta["allowed-channels"] = allowedChannels
			}
			if allowedGroups := strings.TrimSpace(metadata["allowed-channel-groups"]); allowedGroups != "" {
				meta["allowed-channel-groups"] = allowedGroups
			}
		}
	}
	if routeVal, exists := c.Get(internalrouting.GinPathRouteContextKey); exists {
		if route, ok := routeVal.(*internalrouting.PathRouteContext); ok && route != nil {
			if group := strings.TrimSpace(route.Group); group != "" {
				meta[coreexecutor.RouteGroupMetadataKey] = group
			}
			if fallback := strings.TrimSpace(route.Fallback); fallback != "" {
				meta[coreexecutor.RouteFallbackMetadataKey] = fallback
			}
		}
	}
	if len(meta) == 0 {
		return nil
	}
	return meta
}

func errorTypeForStatus(status int) string {
	switch {
	case status == http.StatusUnauthorized:
		return "authentication_error"
	case status >= http.StatusBadRequest && status < http.StatusInternalServerError:
		return "invalid_request_error"
	default:
		return "server_error"
	}
}

func writeOpenAIImagesError(c *gin.Context, status int, errorType string, message string) {
	c.JSON(status, handlers.ErrorResponse{
		Error: handlers.ErrorDetail{
			Message: strings.TrimSpace(message),
			Type:    errorType,
		},
	})
}
