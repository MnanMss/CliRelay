package codexmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

const (
	rpcMethodAccountList          = "account/list"
	rpcMethodAccountImport        = "account/import"
	rpcMethodAccountDelete        = "account/delete"
	rpcMethodAccountLoginStart    = "account/login/start"
	rpcMethodAccountLoginStatus   = "account/login/status"
	rpcMethodAccountLoginComplete = "account/login/complete"
	rpcMethodUsageRead            = "account/usage/read"
	rpcMethodUsageList            = "account/usage/list"
	rpcMethodUsageRefresh         = "account/usage/refresh"

	defaultRPCEndpointPath   = "/rpc"
	defaultRPCRequestTimeout = 8 * time.Second
	defaultRPCMaxRetries     = 1
	defaultRPCRetryDelay     = 180 * time.Millisecond
	maxRPCRetryDelay         = 1200 * time.Millisecond

	rpcResponseBodyLimitBytes = 1 << 20
)

type RPCClientConfig struct {
	Endpoint       string
	RPCToken       string
	RequestTimeout time.Duration
	MaxRetries     int
	RetryDelay     time.Duration
	HTTPClient     *http.Client
}

type RPCClientAPI interface {
	ListAccounts(ctx context.Context, params RPCAccountListParams) (RPCAccountListResult, error)
	ImportAccounts(ctx context.Context, contents []string) (RPCAccountImportResult, error)
	DeleteAccount(ctx context.Context, accountID string) error
	StartLogin(ctx context.Context, req RPCLoginStartRequest) (RPCLoginStartResult, error)
	GetLoginStatus(ctx context.Context, loginID string) (RPCLoginStatusResult, error)
	CompleteLogin(ctx context.Context, req RPCLoginCompleteRequest) error
	ReadUsage(ctx context.Context, accountID string) (RPCUsageReadResult, error)
	ListUsage(ctx context.Context) (RPCUsageListResult, error)
	RefreshUsage(ctx context.Context, accountID string) error
}

type RPCClient struct {
	endpoint       string
	rpcToken       string
	httpClient     *http.Client
	requestTimeout time.Duration
	maxRetries     int
	retryDelay     time.Duration
	requestID      atomic.Uint64
}

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc,omitempty"`
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcActionResult struct {
	OK bool `json:"ok"`
}

type RPCAccountListParams struct {
	Page        int
	PageSize    int
	Query       string
	Filter      string
	GroupFilter string
}

type RPCAccountSummary struct {
	ID        string  `json:"id"`
	Label     string  `json:"label"`
	GroupName *string `json:"groupName"`
	Sort      int64   `json:"sort"`
	Status    string  `json:"status"`
}

type RPCAccountListResult struct {
	Items    []RPCAccountSummary `json:"items"`
	Total    int64               `json:"total"`
	Page     int64               `json:"page"`
	PageSize int64               `json:"pageSize"`
}

type RPCAccountImportError struct {
	Index   int64  `json:"index"`
	Message string `json:"message"`
}

type RPCAccountImportResult struct {
	Total   int64                   `json:"total"`
	Created int64                   `json:"created"`
	Updated int64                   `json:"updated"`
	Failed  int64                   `json:"failed"`
	Errors  []RPCAccountImportError `json:"errors"`
}

type RPCLoginStartRequest struct {
	Type        string
	OpenBrowser *bool
	Note        string
	Tags        string
	GroupName   string
	WorkspaceID string
}

type RPCDeviceAuthInfo struct {
	UserCodeURL     string `json:"userCodeUrl"`
	TokenURL        string `json:"tokenUrl"`
	VerificationURL string `json:"verificationUrl"`
	RedirectURI     string `json:"redirectUri"`
}

type RPCLoginStartResult struct {
	AuthURL     string             `json:"authUrl"`
	LoginID     string             `json:"loginId"`
	LoginType   string             `json:"loginType"`
	Issuer      string             `json:"issuer"`
	ClientID    string             `json:"clientId"`
	RedirectURI string             `json:"redirectUri"`
	Warning     *string            `json:"warning"`
	Device      *RPCDeviceAuthInfo `json:"device"`
}

type RPCLoginStatusResult struct {
	Status    string  `json:"status"`
	Error     *string `json:"error"`
	UpdatedAt *int64  `json:"updatedAt"`
}

type RPCLoginCompleteRequest struct {
	State       string
	Code        string
	RedirectURI string
}

type RPCUsageSnapshot struct {
	AccountID              *string  `json:"accountId"`
	AvailabilityStatus     *string  `json:"availabilityStatus"`
	UsedPercent            *float64 `json:"usedPercent"`
	WindowMinutes          *int64   `json:"windowMinutes"`
	ResetsAt               *int64   `json:"resetsAt"`
	SecondaryUsedPercent   *float64 `json:"secondaryUsedPercent"`
	SecondaryWindowMinutes *int64   `json:"secondaryWindowMinutes"`
	SecondaryResetsAt      *int64   `json:"secondaryResetsAt"`
	CreditsJSON            *string  `json:"-"`
	CapturedAt             *int64   `json:"capturedAt"`
}

type RPCUsageReadResult struct {
	Snapshot *RPCUsageSnapshot `json:"snapshot"`
}

type RPCUsageListResult struct {
	Items []RPCUsageSnapshot `json:"items"`
}

func NewRPCClient(cfg RPCClientConfig) (*RPCClient, error) {
	endpoint, err := normalizeRPCEndpoint(cfg.Endpoint)
	if err != nil {
		return nil, err
	}

	timeout := cfg.RequestTimeout
	if timeout <= 0 {
		timeout = defaultRPCRequestTimeout
	}

	maxRetries := cfg.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}
	if maxRetries == 0 {
		maxRetries = defaultRPCMaxRetries
	}

	retryDelay := cfg.RetryDelay
	if retryDelay <= 0 {
		retryDelay = defaultRPCRetryDelay
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}

	return &RPCClient{
		endpoint:       endpoint,
		rpcToken:       strings.TrimSpace(cfg.RPCToken),
		httpClient:     httpClient,
		requestTimeout: timeout,
		maxRetries:     maxRetries,
		retryDelay:     retryDelay,
	}, nil
}

func normalizeRPCEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("codex-manager rpc endpoint is required")
	}

	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid codex-manager rpc endpoint: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("invalid codex-manager rpc endpoint")
	}

	if strings.TrimSpace(parsed.Path) == "" || parsed.Path == "/" {
		parsed.Path = defaultRPCEndpointPath
	}

	return parsed.String(), nil
}

func (c *RPCClient) ListAccounts(ctx context.Context, params RPCAccountListParams) (RPCAccountListResult, error) {
	var out RPCAccountListResult
	err := c.call(ctx, rpcMethodAccountList, buildAccountListParams(params), true, &out)
	if err != nil {
		return RPCAccountListResult{}, err
	}
	return out, nil
}

func buildAccountListParams(params RPCAccountListParams) map[string]any {
	payload := map[string]any{}
	if params.Page > 0 {
		payload["page"] = params.Page
	}
	if params.PageSize > 0 {
		payload["pageSize"] = params.PageSize
	}
	if query := strings.TrimSpace(params.Query); query != "" {
		payload["query"] = query
	}
	if filter := strings.TrimSpace(params.Filter); filter != "" {
		payload["filter"] = filter
	}
	if groupFilter := strings.TrimSpace(params.GroupFilter); groupFilter != "" {
		payload["groupFilter"] = groupFilter
	}
	if len(payload) == 0 {
		return nil
	}
	return payload
}

func (c *RPCClient) ImportAccounts(ctx context.Context, contents []string) (RPCAccountImportResult, error) {
	normalizedContents := make([]string, 0, len(contents))
	for _, content := range contents {
		trimmed := strings.TrimSpace(content)
		if trimmed != "" {
			normalizedContents = append(normalizedContents, trimmed)
		}
	}

	var out RPCAccountImportResult
	err := c.call(
		ctx,
		rpcMethodAccountImport,
		map[string]any{"contents": normalizedContents},
		false,
		&out,
	)
	if err != nil {
		return RPCAccountImportResult{}, err
	}
	return out, nil
}

func (c *RPCClient) DeleteAccount(ctx context.Context, accountID string) error {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return ErrInvalidAccountID
	}
	var out rpcActionResult
	return c.call(
		ctx,
		rpcMethodAccountDelete,
		map[string]any{"accountId": accountID},
		false,
		&out,
	)
}

func (c *RPCClient) StartLogin(ctx context.Context, req RPCLoginStartRequest) (RPCLoginStartResult, error) {
	loginType := strings.TrimSpace(req.Type)
	if loginType == "" {
		loginType = "chatgpt"
	}

	openBrowser := true
	if req.OpenBrowser != nil {
		openBrowser = *req.OpenBrowser
	}

	payload := map[string]any{
		"type":        loginType,
		"openBrowser": openBrowser,
	}
	if note := strings.TrimSpace(req.Note); note != "" {
		payload["note"] = note
	}
	if tags := strings.TrimSpace(req.Tags); tags != "" {
		payload["tags"] = tags
	}
	if groupName := strings.TrimSpace(req.GroupName); groupName != "" {
		payload["groupName"] = groupName
	}
	if workspaceID := strings.TrimSpace(req.WorkspaceID); workspaceID != "" {
		payload["workspaceId"] = workspaceID
	}

	var out RPCLoginStartResult
	err := c.call(ctx, rpcMethodAccountLoginStart, payload, false, &out)
	if err != nil {
		return RPCLoginStartResult{}, err
	}
	return out, nil
}

func (c *RPCClient) GetLoginStatus(ctx context.Context, loginID string) (RPCLoginStatusResult, error) {
	payload := map[string]any{"loginId": strings.TrimSpace(loginID)}
	var out RPCLoginStatusResult
	err := c.callWithOptions(ctx, rpcMethodAccountLoginStatus, payload, true, false, &out)
	if err != nil {
		return RPCLoginStatusResult{}, err
	}
	return out, nil
}

func (c *RPCClient) CompleteLogin(ctx context.Context, req RPCLoginCompleteRequest) error {
	payload := map[string]any{
		"state": strings.TrimSpace(req.State),
		"code":  strings.TrimSpace(req.Code),
	}
	if redirectURI := strings.TrimSpace(req.RedirectURI); redirectURI != "" {
		payload["redirectUri"] = redirectURI
	}

	var out rpcActionResult
	return c.call(ctx, rpcMethodAccountLoginComplete, payload, false, &out)
}

func (c *RPCClient) ReadUsage(ctx context.Context, accountID string) (RPCUsageReadResult, error) {
	accountID = strings.TrimSpace(accountID)
	var params any
	if accountID != "" {
		params = map[string]any{"accountId": accountID}
	}

	var out RPCUsageReadResult
	err := c.call(ctx, rpcMethodUsageRead, params, true, &out)
	if err != nil {
		return RPCUsageReadResult{}, err
	}
	return out, nil
}

func (c *RPCClient) ListUsage(ctx context.Context) (RPCUsageListResult, error) {
	var out RPCUsageListResult
	err := c.call(ctx, rpcMethodUsageList, nil, true, &out)
	if err != nil {
		return RPCUsageListResult{}, err
	}
	return out, nil
}

func (c *RPCClient) RefreshUsage(ctx context.Context, accountID string) error {
	accountID = strings.TrimSpace(accountID)
	var params any
	if accountID != "" {
		params = map[string]any{"accountId": accountID}
	}
	var out rpcActionResult
	return c.call(ctx, rpcMethodUsageRefresh, params, false, &out)
}

func (c *RPCClient) AccountList(ctx context.Context, params RPCAccountListParams) (RPCAccountListResult, error) {
	return c.ListAccounts(ctx, params)
}

func (c *RPCClient) AccountImport(ctx context.Context, contents []string) (RPCAccountImportResult, error) {
	return c.ImportAccounts(ctx, contents)
}

func (c *RPCClient) AccountDelete(ctx context.Context, accountID string) error {
	return c.DeleteAccount(ctx, accountID)
}

func (c *RPCClient) AccountLoginStart(ctx context.Context, req RPCLoginStartRequest) (RPCLoginStartResult, error) {
	return c.StartLogin(ctx, req)
}

func (c *RPCClient) AccountLoginStatus(ctx context.Context, loginID string) (RPCLoginStatusResult, error) {
	return c.GetLoginStatus(ctx, loginID)
}

func (c *RPCClient) AccountLoginComplete(ctx context.Context, req RPCLoginCompleteRequest) error {
	return c.CompleteLogin(ctx, req)
}

func (c *RPCClient) UsageRead(ctx context.Context, accountID string) (RPCUsageReadResult, error) {
	return c.ReadUsage(ctx, accountID)
}

func (c *RPCClient) UsageList(ctx context.Context) (RPCUsageListResult, error) {
	return c.ListUsage(ctx)
}

func (c *RPCClient) UsageRefresh(ctx context.Context, accountID string) error {
	return c.RefreshUsage(ctx, accountID)
}

func (c *RPCClient) call(ctx context.Context, method string, params any, allowRetry bool, out any) error {
	return c.callWithOptions(ctx, method, params, allowRetry, true, out)
}

func (c *RPCClient) callWithOptions(ctx context.Context, method string, params any, allowRetry bool, inspectResultErrorField bool, out any) error {
	if c == nil {
		return NewCodedError(http.StatusInternalServerError, CodeInternalError, "codex-manager rpc client is nil", false)
	}

	requestID := c.requestID.Add(1)
	payload, err := json.Marshal(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      requestID,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return NewCodedError(http.StatusInternalServerError, CodeInternalError, "failed to encode codex-manager rpc request", false)
	}

	attempts := 1
	if allowRetry && c.maxRetries > 0 {
		attempts += c.maxRetries
	}

	for attempt := 1; attempt <= attempts; attempt++ {
		retryable, callErr := c.callOnce(ctx, payload, requestID, inspectResultErrorField, out)
		if callErr == nil {
			return nil
		}
		if !retryable || attempt == attempts {
			return callErr
		}

		delay := c.retryDelayForAttempt(attempt)
		if err := sleepWithContext(ctx, delay); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			return NewCodedError(http.StatusServiceUnavailable, CodeUpstreamUnavailable, ErrUpstreamUnavailable.Error(), true)
		}
	}

	return NewCodedError(http.StatusInternalServerError, CodeInternalError, "codex-manager rpc call failed", false)
}

func (c *RPCClient) retryDelayForAttempt(attempt int) time.Duration {
	delay := c.retryDelay
	if delay <= 0 {
		delay = defaultRPCRetryDelay
	}
	if attempt <= 1 {
		if delay > maxRPCRetryDelay {
			return maxRPCRetryDelay
		}
		return delay
	}

	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= maxRPCRetryDelay {
			return maxRPCRetryDelay
		}
	}
	return delay
}

func (c *RPCClient) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.requestTimeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, c.requestTimeout)
}

func (c *RPCClient) callOnce(ctx context.Context, payload []byte, requestID uint64, inspectResultErrorField bool, out any) (bool, error) {
	requestCtx, cancel := c.requestContext(ctx)
	defer cancel()

	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return false, NewCodedError(http.StatusInternalServerError, CodeInternalError, "failed to create codex-manager rpc request", false)
	}

	request.Header.Set("Content-Type", "application/json")
	if c.rpcToken != "" {
		request.Header.Set("X-CodexManager-Rpc-Token", c.rpcToken)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return false, err
		}
		return isRetryableTransportError(err), mapRPCTransportError(err)
	}
	defer func() { _ = response.Body.Close() }()

	rawBody, readErr := io.ReadAll(io.LimitReader(response.Body, rpcResponseBodyLimitBytes))
	if readErr != nil {
		return false, NewCodedError(http.StatusServiceUnavailable, CodeUpstreamUnavailable, ErrUpstreamUnavailable.Error(), true)
	}

	if response.StatusCode >= http.StatusInternalServerError {
		return true, mapRPCHTTPStatusError(response.StatusCode)
	}

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return false, mapRPCHTTPStatusError(response.StatusCode)
	}

	var rpcResp jsonRPCResponse
	if err := json.Unmarshal(rawBody, &rpcResp); err != nil {
		return false, NewCodedError(http.StatusServiceUnavailable, CodeUpstreamUnavailable, ErrUpstreamUnavailable.Error(), true)
	}

	if rpcResp.ID != 0 && rpcResp.ID != requestID {
		return false, NewCodedError(http.StatusServiceUnavailable, CodeUpstreamUnavailable, ErrUpstreamUnavailable.Error(), true)
	}

	if rpcResp.Error != nil {
		message := strings.TrimSpace(rpcResp.Error.Message)
		if message == "" {
			message = fmt.Sprintf("codex-manager upstream rpc error (code=%d)", rpcResp.Error.Code)
		}
		return false, mapRPCBusinessError(message)
	}

	if len(rpcResp.Result) == 0 || string(rpcResp.Result) == "null" {
		return false, NewCodedError(http.StatusServiceUnavailable, CodeUpstreamUnavailable, ErrUpstreamUnavailable.Error(), true)
	}

	if inspectResultErrorField {
		if businessErr := extractRPCBusinessError(rpcResp.Result); businessErr != "" {
			return false, mapRPCBusinessError(businessErr)
		}
	}

	if out == nil {
		return false, nil
	}

	if err := json.Unmarshal(rpcResp.Result, out); err != nil {
		return false, NewCodedError(http.StatusServiceUnavailable, CodeUpstreamUnavailable, ErrUpstreamUnavailable.Error(), true)
	}

	return false, nil
}

func mapRPCTransportError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) || isTimeoutError(err) {
		return NewCodedError(http.StatusServiceUnavailable, CodeUpstreamTimeout, "codex-manager upstream request timed out", true)
	}
	return NewCodedError(http.StatusServiceUnavailable, CodeUpstreamUnavailable, ErrUpstreamUnavailable.Error(), true)
}

func isRetryableTransportError(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}

	return true
}

func isTimeoutError(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		return true
	}

	return false
}

func mapRPCHTTPStatusError(status int) error {
	if status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout {
		return NewCodedError(http.StatusServiceUnavailable, CodeUpstreamTimeout, "codex-manager upstream request timed out", true)
	}
	if status >= http.StatusInternalServerError {
		return NewCodedError(http.StatusServiceUnavailable, CodeUpstreamUnavailable, ErrUpstreamUnavailable.Error(), true)
	}

	message := fmt.Sprintf("codex-manager upstream rejected request (HTTP %d)", status)
	return NewCodedError(http.StatusBadGateway, CodeUpstreamRejected, message, false)
}

func extractRPCBusinessError(raw json.RawMessage) string {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}

	if message := normalizeRPCErrorMessage(payload["error"]); message != "" {
		return message
	}

	if rawOK, exists := payload["ok"]; exists {
		if okFlag, ok := rawOK.(bool); ok && !okFlag {
			return "codex-manager upstream returned unsuccessful result"
		}
	}

	return ""
}

func normalizeRPCErrorMessage(raw any) string {
	switch typed := raw.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]any:
		if value, ok := typed["message"].(string); ok {
			if msg := strings.TrimSpace(value); msg != "" {
				return msg
			}
		}
		if value, ok := typed["error"].(string); ok {
			if msg := strings.TrimSpace(value); msg != "" {
				return msg
			}
		}
	}

	return ""
}

func mapRPCBusinessError(message string) error {
	normalized := strings.TrimSpace(message)
	if normalized == "" {
		normalized = "codex-manager upstream rejected request"
	}

	lower := strings.ToLower(normalized)
	if strings.Contains(lower, "account") && strings.Contains(lower, "not found") {
		return ErrAccountNotFound
	}

	if strings.Contains(lower, "timeout") {
		return NewCodedError(http.StatusServiceUnavailable, CodeUpstreamTimeout, "codex-manager upstream request timed out", true)
	}

	return NewCodedError(http.StatusBadGateway, CodeUpstreamRejected, normalized, false)
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
