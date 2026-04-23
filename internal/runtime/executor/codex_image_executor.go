package executor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"golang.org/x/crypto/sha3"
)

const (
	codexImageModel              = "gpt-image-2"
	codexImageBackendUserAgent   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	codexImageRequirementsDiff   = "0fffff"
	codexImageGenerationAlt      = "images/generations"
	codexImageEditsAlt           = "images/edits"
	codexImageDefaultPrompt      = "Generate an image."
	codexImageConversationTimout = 180 * time.Second
	codexImageMaxN               = 4
)

var codexImageChatGPTBaseURL = "https://chatgpt.com"

type codexImageRequest struct {
	Model          string
	Prompt         string
	N              int
	Size           string
	Quality        string
	Stream         bool
	ResponseFormat string
	Uploads        []codexImageUpload
}

type codexImageUpload struct {
	FileName    string `json:"file_name"`
	ContentType string `json:"content_type"`
	DataBase64  string `json:"data_base64,omitempty"`
	Data        []byte `json:"-"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
}

type codexUploadedImage struct {
	FileID      string
	FileName    string
	FileSize    int
	ContentType string
	Width       int
	Height      int
}

type codexChatRequirements struct {
	Token  string `json:"token"`
	Arkose struct {
		Required bool `json:"required"`
	} `json:"arkose"`
	ProofOfWork struct {
		Required   bool   `json:"required"`
		Seed       string `json:"seed"`
		Difficulty string `json:"difficulty"`
	} `json:"proofofwork"`
}

type codexImagePointer struct {
	Pointer string
	Prompt  string
}

func (e *CodexExecutor) executeImageGeneration(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (resp cliproxyexecutor.Response, err error) {
	parsed, err := parseCodexImageRequest(req.Payload)
	if err != nil {
		return cliproxyexecutor.Response{}, statusErr{code: http.StatusBadRequest, msg: err.Error()}
	}
	if opts.Alt == codexImageEditsAlt && len(parsed.Uploads) == 0 {
		return cliproxyexecutor.Response{}, statusErr{code: http.StatusBadRequest, msg: "image file is required for image edits"}
	}
	reporter := newUsageReporter(ctx, e.Identifier(), parsed.Model, auth)
	inputForLog := sanitizeCodexImageRequestForLog(req.Payload)
	reporter.setInputContent(inputForLog)
	defer reporter.trackFailure(ctx, &err)
	apiKey, _ := codexCreds(auth)
	if strings.TrimSpace(apiKey) == "" {
		return cliproxyexecutor.Response{}, statusErr{code: http.StatusUnauthorized, msg: "codex image generation requires a Codex OAuth access token"}
	}

	ctxRequest := ctx
	if ctxRequest == nil {
		ctxRequest = context.Background()
	}
	if _, ok := ctxRequest.Deadline(); !ok {
		var cancel context.CancelFunc
		ctxRequest, cancel = context.WithTimeout(ctxRequest, codexImageConversationTimout)
		defer cancel()
	}

	httpClient := newProxyAwareHTTPClient(ctxRequest, e.cfg, auth, codexImageConversationTimout)
	headers := buildCodexImageBackendHeaders(auth, apiKey)

	_ = codexImageBootstrap(ctxRequest, httpClient, headers)
	chatReqs, err := fetchCodexImageChatRequirements(ctxRequest, httpClient, headers)
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}
	if chatReqs.Arkose.Required {
		return cliproxyexecutor.Response{}, statusErr{code: http.StatusForbidden, msg: "chat-requirements requires unsupported challenge (arkose)"}
	}

	proofToken := generateCodexImageProofToken(chatReqs.ProofOfWork.Required, chatReqs.ProofOfWork.Seed, chatReqs.ProofOfWork.Difficulty, headers.Get("User-Agent"))
	payloads := make([][]byte, 0, parsed.N)
	var responseHeaders http.Header
	for i := 0; i < parsed.N; i++ {
		payload, headers, runErr := e.executeCodexImageOnce(ctxRequest, httpClient, headers, parsed, chatReqs, proofToken, i)
		if runErr != nil {
			return cliproxyexecutor.Response{}, runErr
		}
		payloads = append(payloads, payload)
		responseHeaders = headers
	}

	payload, err := mergeCodexImageOpenAIResponses(payloads)
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}
	reporter.publishWithContent(ctxRequest, parseOpenAIUsage(payload), inputForLog, string(payload))
	reporter.ensurePublished(ctxRequest)
	return cliproxyexecutor.Response{Payload: payload, Headers: responseHeaders}, nil
}

func (e *CodexExecutor) executeCodexImageOnce(
	ctx context.Context,
	httpClient *http.Client,
	headers http.Header,
	parsed *codexImageRequest,
	chatReqs *codexChatRequirements,
	proofToken string,
	index int,
) ([]byte, http.Header, error) {
	parentMessageID := uuid.NewString()
	prompt := buildCodexImagePrompt(parsed, index)
	_ = initializeCodexImageConversation(ctx, httpClient, headers)
	conduitToken, err := prepareCodexImageConversation(ctx, httpClient, headers, prompt, parentMessageID, chatReqs.Token, proofToken)
	if err != nil {
		return nil, nil, err
	}

	uploads, err := uploadCodexImageFiles(ctx, httpClient, headers, parsed.Uploads)
	if err != nil {
		return nil, nil, err
	}

	convReq := buildCodexImageConversationRequest(prompt, parentMessageID, uploads)
	convHeaders := cloneHeader(headers)
	convHeaders.Set("Accept", "text/event-stream")
	convHeaders.Set("Content-Type", "application/json")
	convHeaders.Set("openai-sentinel-chat-requirements-token", chatReqs.Token)
	if conduitToken != "" {
		convHeaders.Set("x-conduit-token", conduitToken)
	}
	if proofToken != "" {
		convHeaders.Set("openai-sentinel-proof-token", proofToken)
	}

	body, _ := json.Marshal(convReq)
	httpResp, err := doCodexImageJSON(ctx, httpClient, http.MethodPost, codexImageURL("/backend-api/f/conversation"), convHeaders, body)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if httpResp != nil && httpResp.Body != nil {
			_ = httpResp.Body.Close()
		}
	}()
	if httpResp.StatusCode < http.StatusOK || httpResp.StatusCode >= http.StatusMultipleChoices {
		return nil, nil, codexImageStatusErr(httpResp, "openai image conversation request failed")
	}

	conversationID, pointers, err := readCodexImageConversationStream(httpResp.Body)
	if err != nil {
		return nil, nil, err
	}
	if conversationID != "" && !hasCodexFileServicePointer(pointers) {
		polled, pollErr := pollCodexImageConversation(ctx, httpClient, headers, conversationID)
		if pollErr != nil {
			return nil, nil, pollErr
		}
		pointers = mergeCodexImagePointers(pointers, polled)
	}
	pointers = preferCodexFileServicePointers(pointers)
	if len(pointers) == 0 {
		return nil, nil, statusErr{code: http.StatusBadGateway, msg: "openai image conversation returned no downloadable images"}
	}

	payload, err := buildCodexImageOpenAIResponse(ctx, httpClient, headers, conversationID, pointers)
	if err != nil {
		return nil, nil, err
	}
	return payload, httpResp.Header.Clone(), nil
}

func parseCodexImageRequest(body []byte) (*codexImageRequest, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("request body is empty")
	}
	if !gjson.ValidBytes(body) {
		return nil, fmt.Errorf("failed to parse request body")
	}
	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if model == "" {
		model = codexImageModel
	}
	if model != codexImageModel {
		return nil, fmt.Errorf("model %q is not supported by this endpoint", model)
	}
	prompt := strings.TrimSpace(gjson.GetBytes(body, "prompt").String())
	if prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	parsed := &codexImageRequest{Model: model, Prompt: prompt, N: 1}
	if nResult := gjson.GetBytes(body, "n"); nResult.Exists() {
		if nResult.Type != gjson.Number {
			return nil, fmt.Errorf("n must be a number")
		}
		parsed.N = int(nResult.Int())
	}
	if parsed.N < 1 || parsed.N > codexImageMaxN {
		return nil, fmt.Errorf("n must be between 1 and %d for Codex OAuth image generation", codexImageMaxN)
	}
	if streamResult := gjson.GetBytes(body, "stream"); streamResult.Exists() {
		if streamResult.Type != gjson.True && streamResult.Type != gjson.False {
			return nil, fmt.Errorf("stream must be a boolean")
		}
		parsed.Stream = streamResult.Bool()
	}
	if parsed.Stream {
		return nil, fmt.Errorf("streaming image generation is not supported for Codex OAuth")
	}
	parsed.ResponseFormat = strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "response_format").String()))
	if parsed.ResponseFormat != "" && parsed.ResponseFormat != "b64_json" {
		return nil, fmt.Errorf("only response_format=b64_json is supported for Codex OAuth image generation")
	}
	parsed.Size = strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "size").String()))
	if parsed.Size != "" && !isSupportedCodexImageSize(parsed.Size) {
		return nil, fmt.Errorf("size must be one of 1024x1024, 1792x1024, 1024x1792")
	}
	parsed.Quality = strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "quality").String()))
	if parsed.Quality != "" && !isSupportedCodexImageQuality(parsed.Quality) {
		return nil, fmt.Errorf("quality must be one of low, medium, high")
	}
	if uploadResult := gjson.GetBytes(body, "image_files"); uploadResult.Exists() {
		if !uploadResult.IsArray() {
			return nil, fmt.Errorf("image_files must be an array")
		}
		for _, item := range uploadResult.Array() {
			upload := codexImageUpload{
				FileName:    strings.TrimSpace(item.Get("file_name").String()),
				ContentType: strings.TrimSpace(item.Get("content_type").String()),
				DataBase64:  strings.TrimSpace(item.Get("data_base64").String()),
				Width:       int(item.Get("width").Int()),
				Height:      int(item.Get("height").Int()),
			}
			if upload.FileName == "" {
				upload.FileName = "image.png"
			}
			if upload.ContentType == "" {
				upload.ContentType = "application/octet-stream"
			}
			if upload.DataBase64 == "" {
				return nil, fmt.Errorf("image_files[].data_base64 is required")
			}
			decoded, err := base64.StdEncoding.DecodeString(upload.DataBase64)
			if err != nil {
				return nil, fmt.Errorf("image_files[].data_base64 is invalid")
			}
			if len(decoded) == 0 {
				return nil, fmt.Errorf("image_files[].data_base64 is empty")
			}
			upload.Data = decoded
			parsed.Uploads = append(parsed.Uploads, upload)
		}
	}
	return parsed, nil
}

func isSupportedCodexImageSize(size string) bool {
	switch strings.ToLower(strings.TrimSpace(size)) {
	case "1024x1024", "1792x1024", "1024x1792":
		return true
	default:
		return false
	}
}

func isSupportedCodexImageQuality(quality string) bool {
	switch strings.ToLower(strings.TrimSpace(quality)) {
	case "low", "medium", "high":
		return true
	default:
		return false
	}
}

func buildCodexImageBackendHeaders(auth *cliproxyauth.Auth, token string) http.Header {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+token)
	headers.Set("Accept", "application/json")
	headers.Set("Origin", "https://chatgpt.com")
	headers.Set("Referer", "https://chatgpt.com/")
	headers.Set("Sec-Fetch-Dest", "empty")
	headers.Set("Sec-Fetch-Mode", "cors")
	headers.Set("Sec-Fetch-Site", "same-origin")
	headers.Set("User-Agent", codexImageBackendUserAgent)
	if auth != nil {
		if accountID, ok := auth.Metadata["account_id"].(string); ok && strings.TrimSpace(accountID) != "" {
			headers.Set("chatgpt-account-id", strings.TrimSpace(accountID))
		}
	}
	deviceID, sessionID := codexImageSessionIDs(auth)
	if deviceID != "" {
		headers.Set("oai-device-id", deviceID)
		headers.Set("Cookie", "oai-did="+deviceID)
	}
	if sessionID != "" {
		headers.Set("oai-session-id", sessionID)
	}
	return headers
}

func codexImageSessionIDs(auth *cliproxyauth.Auth) (string, string) {
	deviceID := ""
	sessionID := ""
	if auth != nil && auth.Metadata != nil {
		if raw, ok := auth.Metadata["openai_device_id"].(string); ok {
			deviceID = strings.TrimSpace(raw)
		}
		if raw, ok := auth.Metadata["openai_session_id"].(string); ok {
			sessionID = strings.TrimSpace(raw)
		}
	}
	if deviceID == "" {
		deviceID = uuid.NewString()
	}
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	return deviceID, sessionID
}

func codexImageURL(path string) string {
	base := strings.TrimRight(strings.TrimSpace(codexImageChatGPTBaseURL), "/")
	if base == "" {
		base = "https://chatgpt.com"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}

func codexImageBootstrap(ctx context.Context, client *http.Client, headers http.Header) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, codexImageURL("/"), nil)
	if err != nil {
		return err
	}
	req.Header = cloneHeader(headers)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func fetchCodexImageChatRequirements(ctx context.Context, client *http.Client, headers http.Header) (*codexChatRequirements, error) {
	var lastErr error
	for _, payload := range []map[string]any{
		{"p": nil},
		{"p": generateCodexImageRequirementsToken(headers.Get("User-Agent"))},
	} {
		body, _ := json.Marshal(payload)
		resp, err := doCodexImageJSON(ctx, client, http.MethodPost, codexImageURL("/backend-api/sentinel/chat-requirements"), headers, body)
		if err != nil {
			lastErr = err
			continue
		}
		respBody, readErr := readAndCloseCodexImageBody(resp)
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			var result codexChatRequirements
			if err := json.Unmarshal(respBody, &result); err != nil {
				lastErr = err
				continue
			}
			if strings.TrimSpace(result.Token) != "" {
				return &result, nil
			}
		}
		lastErr = codexImageStatusErrWithBody(resp.StatusCode, respBody, "chat-requirements failed")
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("chat-requirements failed")
	}
	return nil, lastErr
}

func initializeCodexImageConversation(ctx context.Context, client *http.Client, headers http.Header) error {
	payload := map[string]any{
		"gizmo_id":                nil,
		"requested_default_model": nil,
		"conversation_id":         nil,
		"timezone_offset_min":     codexTimezoneOffsetMinutes(),
		"system_hints":            []string{"picture_v2"},
	}
	body, _ := json.Marshal(payload)
	resp, err := doCodexImageJSON(ctx, client, http.MethodPost, codexImageURL("/backend-api/conversation/init"), headers, body)
	if err != nil {
		return err
	}
	respBody, readErr := readAndCloseCodexImageBody(resp)
	if readErr != nil {
		return readErr
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return codexImageStatusErrWithBody(resp.StatusCode, respBody, "conversation init failed")
	}
	return nil
}

func prepareCodexImageConversation(ctx context.Context, client *http.Client, headers http.Header, prompt, parentMessageID, chatToken, proofToken string) (string, error) {
	messageID := uuid.NewString()
	payload := map[string]any{
		"action":                "next",
		"client_prepare_state":  "success",
		"fork_from_shared_post": false,
		"parent_message_id":     parentMessageID,
		"model":                 "auto",
		"timezone_offset_min":   codexTimezoneOffsetMinutes(),
		"timezone":              codexTimezoneName(),
		"conversation_mode":     map[string]any{"kind": "primary_assistant"},
		"system_hints":          []string{"picture_v2"},
		"supports_buffering":    true,
		"supported_encodings":   []string{"v1"},
		"partial_query": map[string]any{
			"id":     messageID,
			"author": map[string]any{"role": "user"},
			"content": map[string]any{
				"content_type": "text",
				"parts":        []string{coalesceCodexImageText(prompt, codexImageDefaultPrompt)},
			},
		},
		"client_contextual_info": map[string]any{"app_name": "chatgpt.com"},
	}
	prepareHeaders := cloneHeader(headers)
	prepareHeaders.Set("Accept", "*/*")
	prepareHeaders.Set("Content-Type", "application/json")
	if strings.TrimSpace(chatToken) != "" {
		prepareHeaders.Set("openai-sentinel-chat-requirements-token", strings.TrimSpace(chatToken))
	}
	if strings.TrimSpace(proofToken) != "" {
		prepareHeaders.Set("openai-sentinel-proof-token", strings.TrimSpace(proofToken))
	}
	body, _ := json.Marshal(payload)
	resp, err := doCodexImageJSON(ctx, client, http.MethodPost, codexImageURL("/backend-api/f/conversation/prepare"), prepareHeaders, body)
	if err != nil {
		return "", err
	}
	respBody, readErr := readAndCloseCodexImageBody(resp)
	if readErr != nil {
		return "", readErr
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", codexImageStatusErrWithBody(resp.StatusCode, respBody, "conversation prepare failed")
	}
	var result struct {
		ConduitToken string `json:"conduit_token"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", err
	}
	return strings.TrimSpace(result.ConduitToken), nil
}

func buildCodexImagePrompt(parsed *codexImageRequest, index int) string {
	if parsed == nil {
		return codexImageDefaultPrompt
	}
	base := strings.TrimSpace(parsed.Prompt)
	if base == "" {
		base = codexImageDefaultPrompt
	}
	extras := make([]string, 0, 4)
	if parsed.Size != "" {
		extras = append(extras, "Preferred image size: "+parsed.Size+".")
	}
	if parsed.Quality != "" {
		extras = append(extras, "Preferred render quality: "+parsed.Quality+".")
	}
	if parsed.N > 1 {
		extras = append(extras, fmt.Sprintf("This is variation %d of %d. Keep it distinct while following the same prompt.", index+1, parsed.N))
	}
	if len(parsed.Uploads) > 0 {
		extras = append(extras, "Use the attached image as the source reference and preserve the original composition unless the prompt says otherwise.")
	}
	if len(extras) == 0 {
		return base
	}
	return base + "\n\n" + strings.Join(extras, "\n")
}

func buildCodexImageConversationRequest(prompt, parentMessageID string, uploads []codexUploadedImage) map[string]any {
	parts := []any{coalesceCodexImageText(prompt, codexImageDefaultPrompt)}
	contentType := "text"
	attachments := make([]map[string]any, 0, len(uploads))
	if len(uploads) > 0 {
		contentType = "multimodal_text"
		parts = make([]any, 0, len(uploads)+1)
		for _, upload := range uploads {
			parts = append(parts, map[string]any{
				"content_type":  "image_asset_pointer",
				"asset_pointer": "file-service://" + upload.FileID,
				"size_bytes":    upload.FileSize,
				"width":         upload.Width,
				"height":        upload.Height,
			})
			attachment := map[string]any{
				"id":       upload.FileID,
				"mimeType": upload.ContentType,
				"name":     upload.FileName,
				"size":     upload.FileSize,
			}
			if upload.Width > 0 {
				attachment["width"] = upload.Width
			}
			if upload.Height > 0 {
				attachment["height"] = upload.Height
			}
			attachments = append(attachments, attachment)
		}
		parts = append(parts, coalesceCodexImageText(prompt, "Edit this image."))
	}
	metadata := map[string]any{
		"developer_mode_connector_ids": []any{},
		"selected_github_repos":        []any{},
		"selected_all_github_repos":    false,
		"system_hints":                 []string{"picture_v2"},
		"serialization_metadata": map[string]any{
			"custom_symbol_offsets": []any{},
		},
	}
	message := map[string]any{
		"id":     uuid.NewString(),
		"author": map[string]any{"role": "user"},
		"content": map[string]any{
			"content_type": contentType,
			"parts":        parts,
		},
		"metadata":    metadata,
		"create_time": float64(time.Now().UnixMilli()) / 1000,
	}
	if len(attachments) > 0 {
		metadata["attachments"] = attachments
	}
	return map[string]any{
		"action":                               "next",
		"client_prepare_state":                 "sent",
		"parent_message_id":                    parentMessageID,
		"model":                                "auto",
		"timezone_offset_min":                  codexTimezoneOffsetMinutes(),
		"timezone":                             codexTimezoneName(),
		"conversation_mode":                    map[string]any{"kind": "primary_assistant"},
		"enable_message_followups":             true,
		"system_hints":                         []string{"picture_v2"},
		"supports_buffering":                   true,
		"supported_encodings":                  []string{"v1"},
		"paragen_cot_summary_display_override": "allow",
		"force_parallel_switch":                "auto",
		"client_contextual_info": map[string]any{
			"is_dark_mode":      false,
			"time_since_loaded": 200,
			"page_height":       900,
			"page_width":        1440,
			"pixel_ratio":       1,
			"screen_height":     1080,
			"screen_width":      1920,
			"app_name":          "chatgpt.com",
		},
		"messages": []any{message},
	}
}

func uploadCodexImageFiles(ctx context.Context, client *http.Client, headers http.Header, uploads []codexImageUpload) ([]codexUploadedImage, error) {
	if len(uploads) == 0 {
		return nil, nil
	}
	results := make([]codexUploadedImage, 0, len(uploads))
	for _, item := range uploads {
		payload, _ := json.Marshal(map[string]any{
			"file_name": codexImageCoalesce(item.FileName, "image.png"),
			"file_size": len(item.Data),
			"use_case":  "multimodal",
		})
		resp, err := doCodexImageJSON(ctx, client, http.MethodPost, codexImageURL("/backend-api/files"), headers, payload)
		if err != nil {
			return nil, err
		}
		respBody, readErr := readAndCloseCodexImageBody(resp)
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return nil, codexImageStatusErrWithBody(resp.StatusCode, respBody, "create upload slot failed")
		}
		var created struct {
			FileID    string `json:"file_id"`
			UploadURL string `json:"upload_url"`
		}
		if err := json.Unmarshal(respBody, &created); err != nil {
			return nil, err
		}
		if strings.TrimSpace(created.FileID) == "" || strings.TrimSpace(created.UploadURL) == "" {
			return nil, statusErr{code: http.StatusBadGateway, msg: "create upload slot failed"}
		}

		putReq, err := http.NewRequestWithContext(ctx, http.MethodPut, created.UploadURL, bytes.NewReader(item.Data))
		if err != nil {
			return nil, err
		}
		putReq.Header.Set("Content-Type", codexImageCoalesce(item.ContentType, "application/octet-stream"))
		putReq.Header.Set("Origin", "https://chatgpt.com")
		putReq.Header.Set("x-ms-blob-type", "BlockBlob")
		putReq.Header.Set("x-ms-version", "2020-04-08")
		putReq.Header.Set("User-Agent", headers.Get("User-Agent"))
		putResp, err := client.Do(putReq)
		if err != nil {
			return nil, err
		}
		putBody, readErr := readAndCloseCodexImageBody(putResp)
		if readErr != nil {
			return nil, readErr
		}
		if putResp.StatusCode < http.StatusOK || putResp.StatusCode >= http.StatusMultipleChoices {
			return nil, codexImageStatusErrWithBody(putResp.StatusCode, putBody, "upload image bytes failed")
		}

		donePayload, _ := json.Marshal(map[string]any{})
		doneResp, err := doCodexImageJSON(ctx, client, http.MethodPost, codexImageURL("/backend-api/files/"+created.FileID+"/uploaded"), headers, donePayload)
		if err != nil {
			return nil, err
		}
		doneBody, readErr := readAndCloseCodexImageBody(doneResp)
		if readErr != nil {
			return nil, readErr
		}
		if doneResp.StatusCode < http.StatusOK || doneResp.StatusCode >= http.StatusMultipleChoices {
			return nil, codexImageStatusErrWithBody(doneResp.StatusCode, doneBody, "mark upload complete failed")
		}

		results = append(results, codexUploadedImage{
			FileID:      created.FileID,
			FileName:    codexImageCoalesce(item.FileName, "image.png"),
			FileSize:    len(item.Data),
			ContentType: codexImageCoalesce(item.ContentType, "application/octet-stream"),
			Width:       item.Width,
			Height:      item.Height,
		})
	}
	return results, nil
}

func codexImageCoalesce(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func sanitizeCodexImageRequestForLog(body []byte) string {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return string(body)
	}
	sanitized := append([]byte(nil), body...)
	if gjson.GetBytes(sanitized, "image_files").Exists() {
		imageFiles := gjson.GetBytes(sanitized, "image_files").Array()
		for i, item := range imageFiles {
			size := 0
			if data := item.Get("data_base64").String(); data != "" {
				size = len(data)
			}
			replacement := map[string]any{
				"file_name":    strings.TrimSpace(item.Get("file_name").String()),
				"content_type": strings.TrimSpace(item.Get("content_type").String()),
				"data_base64":  fmt.Sprintf("[omitted:%d chars]", size),
			}
			if width := item.Get("width").Int(); width > 0 {
				replacement["width"] = width
			}
			if height := item.Get("height").Int(); height > 0 {
				replacement["height"] = height
			}
			if updated, err := sjson.SetBytes(sanitized, fmt.Sprintf("image_files.%d", i), replacement); err == nil {
				sanitized = updated
			}
		}
	}
	return string(sanitized)
}

func mergeCodexImageOpenAIResponses(payloads [][]byte) ([]byte, error) {
	if len(payloads) == 0 {
		return nil, fmt.Errorf("no image payloads to merge")
	}
	if len(payloads) == 1 {
		return payloads[0], nil
	}
	type imageItem struct {
		B64JSON       string `json:"b64_json,omitempty"`
		RevisedPrompt string `json:"revised_prompt,omitempty"`
	}
	merged := struct {
		Created int64       `json:"created"`
		Data    []imageItem `json:"data"`
	}{}
	for _, payload := range payloads {
		var item struct {
			Created int64       `json:"created"`
			Data    []imageItem `json:"data"`
		}
		if err := json.Unmarshal(payload, &item); err != nil {
			return nil, err
		}
		if item.Created > merged.Created {
			merged.Created = item.Created
		}
		merged.Data = append(merged.Data, item.Data...)
	}
	return json.Marshal(merged)
}

func doCodexImageJSON(ctx context.Context, client *http.Client, method, url string, headers http.Header, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header = cloneHeader(headers)
	if len(body) > 0 && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return client.Do(req)
}

func cloneHeader(src http.Header) http.Header {
	if src == nil {
		return nil
	}
	dst := make(http.Header, len(src))
	for key, values := range src {
		dst[key] = append([]string(nil), values...)
	}
	return dst
}

func readAndCloseCodexImageBody(resp *http.Response) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, nil
	}
	defer func() { _ = resp.Body.Close() }()
	return readUpstreamResponseBody("codex-image", resp.Body)
}

func readCodexImageConversationStream(r io.Reader) (string, []codexImagePointer, error) {
	reader := bufio.NewReader(r)
	var conversationID string
	var pointers []codexImagePointer
	for {
		line, err := reader.ReadString('\n')
		if data, ok := codexExtractSSEDataLine(strings.TrimRight(line, "\r\n")); ok && data != "" && data != "[DONE]" {
			dataBytes := []byte(data)
			if conversationID == "" {
				conversationID = strings.TrimSpace(gjson.GetBytes(dataBytes, "v.conversation_id").String())
				if conversationID == "" {
					conversationID = strings.TrimSpace(gjson.GetBytes(dataBytes, "conversation_id").String())
				}
			}
			pointers = mergeCodexImagePointers(pointers, collectCodexImagePointers(dataBytes))
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", nil, err
		}
	}
	return conversationID, pointers, nil
}

func codexExtractSSEDataLine(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "data:") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(trimmed, "data:")), true
}

func collectCodexImagePointers(body []byte) []codexImagePointer {
	matches := codexImagePointerMatches(body)
	if len(matches) == 0 {
		return nil
	}
	prompt := ""
	for _, path := range []string{"message.metadata.dalle.prompt", "metadata.dalle.prompt", "revised_prompt"} {
		if value := strings.TrimSpace(gjson.GetBytes(body, path).String()); value != "" {
			prompt = value
			break
		}
	}
	out := make([]codexImagePointer, 0, len(matches))
	for _, pointer := range matches {
		out = append(out, codexImagePointer{Pointer: pointer, Prompt: prompt})
	}
	return out
}

func codexImagePointerMatches(body []byte) []string {
	raw := string(body)
	matches := make([]string, 0, 4)
	for _, prefix := range []string{"file-service://", "sediment://"} {
		start := 0
		for {
			idx := strings.Index(raw[start:], prefix)
			if idx < 0 {
				break
			}
			idx += start
			end := idx + len(prefix)
			for end < len(raw) {
				ch := raw[end]
				if ch != '-' && ch != '_' && (ch < '0' || ch > '9') && (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') {
					break
				}
				end++
			}
			matches = append(matches, raw[idx:end])
			start = end
		}
	}
	return dedupeCodexImageStrings(matches)
}

func mergeCodexImagePointers(existing []codexImagePointer, next []codexImagePointer) []codexImagePointer {
	if len(next) == 0 {
		return existing
	}
	seen := make(map[string]int, len(existing)+len(next))
	out := append([]codexImagePointer(nil), existing...)
	for i, item := range out {
		seen[item.Pointer] = i
	}
	for _, item := range next {
		if idx, ok := seen[item.Pointer]; ok {
			if out[idx].Prompt == "" && item.Prompt != "" {
				out[idx].Prompt = item.Prompt
			}
			continue
		}
		seen[item.Pointer] = len(out)
		out = append(out, item)
	}
	return out
}

func hasCodexFileServicePointer(items []codexImagePointer) bool {
	for _, item := range items {
		if strings.HasPrefix(item.Pointer, "file-service://") {
			return true
		}
	}
	return false
}

func preferCodexFileServicePointers(items []codexImagePointer) []codexImagePointer {
	if !hasCodexFileServicePointer(items) {
		return items
	}
	out := make([]codexImagePointer, 0, len(items))
	for _, item := range items {
		if strings.HasPrefix(item.Pointer, "file-service://") {
			out = append(out, item)
		}
	}
	return out
}

func pollCodexImageConversation(ctx context.Context, client *http.Client, headers http.Header, conversationID string) ([]codexImagePointer, error) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil, nil
	}
	deadline := time.Now().Add(90 * time.Second)
	interval := 3 * time.Second
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := doCodexImageJSON(ctx, client, http.MethodGet, codexImageURL("/backend-api/conversation/"+conversationID), headers, nil)
		if err != nil {
			lastErr = err
		} else {
			body, readErr := readAndCloseCodexImageBody(resp)
			if readErr != nil {
				lastErr = readErr
			} else if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
				pointers := collectCodexImagePointers(body)
				if len(pointers) > 0 {
					return pointers, nil
				}
			} else {
				return nil, codexImageStatusErrWithBody(resp.StatusCode, body, "conversation poll failed")
			}
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, lastErr
}

func buildCodexImageOpenAIResponse(ctx context.Context, client *http.Client, headers http.Header, conversationID string, pointers []codexImagePointer) ([]byte, error) {
	type responseItem struct {
		B64JSON       string `json:"b64_json"`
		RevisedPrompt string `json:"revised_prompt,omitempty"`
	}
	items := make([]responseItem, 0, len(pointers))
	for _, pointer := range pointers {
		downloadURL, err := fetchCodexImageDownloadURL(ctx, client, headers, conversationID, pointer.Pointer)
		if err != nil {
			return nil, err
		}
		data, err := downloadCodexImageBytes(ctx, client, headers, downloadURL)
		if err != nil {
			return nil, err
		}
		items = append(items, responseItem{
			B64JSON:       base64.StdEncoding.EncodeToString(data),
			RevisedPrompt: pointer.Prompt,
		})
	}
	return json.Marshal(map[string]any{
		"created": time.Now().Unix(),
		"data":    items,
	})
}

func fetchCodexImageDownloadURL(ctx context.Context, client *http.Client, headers http.Header, conversationID string, pointer string) (string, error) {
	url := ""
	switch {
	case strings.HasPrefix(pointer, "file-service://"):
		fileID := strings.TrimPrefix(pointer, "file-service://")
		url = codexImageURL("/backend-api/files/" + fileID + "/download")
	case strings.HasPrefix(pointer, "sediment://"):
		attachmentID := strings.TrimPrefix(pointer, "sediment://")
		if strings.TrimSpace(conversationID) == "" {
			return "", fmt.Errorf("conversation id is required for sediment image pointer")
		}
		url = codexImageURL("/backend-api/conversation/" + strings.TrimSpace(conversationID) + "/attachment/" + attachmentID + "/download")
	default:
		return "", fmt.Errorf("unsupported image pointer: %s", pointer)
	}
	resp, err := doCodexImageJSON(ctx, client, http.MethodGet, url, headers, nil)
	if err != nil {
		return "", err
	}
	body, readErr := readAndCloseCodexImageBody(resp)
	if readErr != nil {
		return "", readErr
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", codexImageStatusErrWithBody(resp.StatusCode, body, "fetch image download url failed")
	}
	var result struct {
		DownloadURL string `json:"download_url"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if strings.TrimSpace(result.DownloadURL) == "" {
		return "", fmt.Errorf("fetch image download url returned empty download_url")
	}
	return strings.TrimSpace(result.DownloadURL), nil
}

func downloadCodexImageBytes(ctx context.Context, client *http.Client, headers http.Header, downloadURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", coalesceCodexImageText(headers.Get("User-Agent"), codexImageBackendUserAgent))
	if strings.HasPrefix(downloadURL, codexImageURL("/")) {
		req.Header = cloneHeader(headers)
		req.Header.Set("Accept", "image/*,*/*;q=0.8")
		req.Header.Del("Content-Type")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := readUpstreamResponseBody("codex-image", resp.Body)
		return nil, codexImageStatusErrWithBody(resp.StatusCode, body, "download image bytes failed")
	}
	return readUpstreamResponseBody("codex-image", resp.Body)
}

func codexImageStatusErr(resp *http.Response, fallback string) statusErr {
	if resp == nil {
		return statusErr{code: http.StatusBadGateway, msg: fallback}
	}
	body, _ := readUpstreamResponseBody("codex-image", resp.Body)
	return codexImageStatusErrWithBody(resp.StatusCode, body, fallback)
}

func codexImageStatusErrWithBody(statusCode int, body []byte, fallback string) statusErr {
	message := strings.TrimSpace(extractCodexImageErrorMessage(body))
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	if message == "" {
		message = fallback
	}
	return statusErr{code: statusCode, msg: message}
}

func extractCodexImageErrorMessage(body []byte) string {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ""
	}
	for _, path := range []string{"error.message", "detail", "message"} {
		if value := strings.TrimSpace(gjson.GetBytes(body, path).String()); value != "" {
			return value
		}
	}
	return ""
}

func codexTimezoneOffsetMinutes() int {
	_, offset := time.Now().Zone()
	return offset / 60
}

func codexTimezoneName() string {
	return time.Now().Location().String()
}

func generateCodexImageRequirementsToken(userAgent string) string {
	config := []any{
		"core" + strconv.Itoa(3008),
		time.Now().UTC().Format(time.RFC1123),
		nil,
		0.123456,
		coalesceCodexImageText(strings.TrimSpace(userAgent), codexImageBackendUserAgent),
		nil,
		"prod-openai-images",
		"en-US",
		"en-US,en",
		0,
		"navigator.webdriver",
		"location",
		"document.body",
		float64(time.Now().UnixMilli()) / 1000,
		uuid.NewString(),
		"",
		8,
		time.Now().Unix(),
	}
	answer, solved := generateCodexImageChallengeAnswer(strconv.FormatInt(time.Now().UnixNano(), 10), codexImageRequirementsDiff, config)
	if solved {
		return "gAAAAAC" + answer
	}
	return ""
}

func generateCodexImageChallengeAnswer(seed string, difficulty string, config []any) (string, bool) {
	diffBytes, err := hex.DecodeString(difficulty)
	if err != nil {
		return "", false
	}
	p1 := []byte(jsonCompactCodexImageSlice(config[:3], true))
	p2 := []byte(jsonCompactCodexImageSlice(config[4:9], false))
	p3 := []byte(jsonCompactCodexImageSlice(config[10:], false))
	seedBytes := []byte(seed)
	for i := 0; i < 100000; i++ {
		payload := fmt.Sprintf("%s%d,%s,%d,%s", p1, i, p2, i>>1, p3)
		encoded := base64.StdEncoding.EncodeToString([]byte(payload))
		sum := sha3.Sum512(append(seedBytes, []byte(encoded)...))
		if bytes.Compare(sum[:len(diffBytes)], diffBytes) <= 0 {
			return encoded, true
		}
	}
	return "", false
}

func jsonCompactCodexImageSlice(values []any, trimSuffixComma bool) string {
	raw, _ := json.Marshal(values)
	text := string(raw)
	if trimSuffixComma {
		return strings.TrimSuffix(text, "]")
	}
	return strings.TrimPrefix(text, "[")
}

func generateCodexImageProofToken(required bool, seed string, difficulty string, userAgent string) string {
	if !required || strings.TrimSpace(seed) == "" || strings.TrimSpace(difficulty) == "" {
		return ""
	}
	screen := 3008
	if len(seed)%2 == 0 {
		screen = 4010
	}
	proofToken := []any{
		screen,
		time.Now().UTC().Format(time.RFC1123),
		nil,
		0,
		coalesceCodexImageText(strings.TrimSpace(userAgent), codexImageBackendUserAgent),
		"https://chatgpt.com/",
		"dpl=openai-images",
		"en",
		"en-US",
		nil,
		"plugins[object PluginArray]",
		"_reactListening",
		"alert",
	}
	diffLen := len(difficulty)
	for i := 0; i < 100000; i++ {
		proofToken[3] = i
		raw, _ := json.Marshal(proofToken)
		encoded := base64.StdEncoding.EncodeToString(raw)
		sum := sha3.Sum512([]byte(seed + encoded))
		if strings.Compare(hex.EncodeToString(sum[:])[:diffLen], difficulty) <= 0 {
			return "gAAAAAB" + encoded
		}
	}
	fallbackBase := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%q", seed)))
	return "gAAAAABwQ8Lk5FbGpA2NcR9dShT6gYjU7VxZ4D" + fallbackBase
}

func coalesceCodexImageText(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func dedupeCodexImageStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
