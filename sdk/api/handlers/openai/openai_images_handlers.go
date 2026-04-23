package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
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

const (
	openAIImageGenerationAlt = "images/generations"
	openAIImageEditsAlt      = "images/edits"
	openAIImageMaxUploadSize = 20 << 20
)

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
	h.executeImages(c, rawJSON, openAIImageGenerationAlt)
}

func (h *OpenAIImagesAPIHandler) Edits(c *gin.Context) {
	rawJSON, ok := readOpenAIImageEditRequest(c)
	if !ok {
		return
	}
	h.executeImages(c, rawJSON, openAIImageEditsAlt)
}

func (h *OpenAIImagesAPIHandler) executeImages(c *gin.Context, rawJSON []byte, alt string) {
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
		Alt:             alt,
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

func readOpenAIImageEditRequest(c *gin.Context) ([]byte, bool) {
	contentType := strings.TrimSpace(c.GetHeader("Content-Type"))
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err == nil && strings.EqualFold(mediaType, "multipart/form-data") {
		payload, parseErr := buildOpenAIImageEditPayloadFromMultipart(c)
		if parseErr != nil {
			writeOpenAIImagesError(c, http.StatusBadRequest, "invalid_request_error", parseErr.Error())
			return nil, false
		}
		return payload, true
	}
	rawJSON, ok := handlers.ReadJSONRequestBody(c)
	if !ok {
		return nil, false
	}
	return rawJSON, true
}

func buildOpenAIImageEditPayloadFromMultipart(c *gin.Context) ([]byte, error) {
	if err := c.Request.ParseMultipartForm(openAIImageMaxUploadSize); err != nil {
		return nil, fmt.Errorf("invalid multipart body")
	}
	form := c.Request.MultipartForm
	if form == nil {
		return nil, fmt.Errorf("invalid multipart body")
	}
	payload := map[string]any{
		"model":  firstOpenAIImagesFormValue(form.Value, "model", "gpt-image-2"),
		"prompt": firstOpenAIImagesFormValue(form.Value, "prompt", ""),
	}
	for _, field := range []string{"size", "quality", "response_format"} {
		if value := firstOpenAIImagesFormValue(form.Value, field, ""); value != "" {
			payload[field] = value
		}
	}
	if value := firstOpenAIImagesFormValue(form.Value, "n", ""); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("n must be a positive integer")
		}
		payload["n"] = n
	}
	files := form.File["image"]
	for key, value := range form.File {
		if strings.HasPrefix(key, "image[") {
			files = append(files, value...)
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("image file is required")
	}
	uploads := make([]map[string]any, 0, len(files))
	for _, fileHeader := range files {
		if fileHeader == nil {
			continue
		}
		if fileHeader.Size > openAIImageMaxUploadSize {
			return nil, fmt.Errorf("image file is too large")
		}
		file, err := fileHeader.Open()
		if err != nil {
			return nil, fmt.Errorf("read image file: %w", err)
		}
		data, err := io.ReadAll(io.LimitReader(file, openAIImageMaxUploadSize+1))
		_ = file.Close()
		if err != nil {
			return nil, fmt.Errorf("read image file: %w", err)
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("image file is empty")
		}
		if len(data) > openAIImageMaxUploadSize {
			return nil, fmt.Errorf("image file is too large")
		}
		contentType := strings.TrimSpace(fileHeader.Header.Get("Content-Type"))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		uploads = append(uploads, map[string]any{
			"file_name":    fileHeader.Filename,
			"content_type": contentType,
			"data_base64":  base64.StdEncoding.EncodeToString(data),
		})
	}
	payload["image_files"] = uploads
	return json.Marshal(payload)
}

func firstOpenAIImagesFormValue(values map[string][]string, key, fallback string) string {
	for _, value := range values[key] {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return fallback
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
