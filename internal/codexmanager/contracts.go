package codexmanager

import (
	"strconv"
	"strings"
	"time"
)

const (
	ConfigSectionName         = "codex-manager"
	ManagementNamespace       = "/v0/management/codex-manager"
	RuntimeSourceCodexManager = "codex_manager"

	DefaultPage     = 1
	DefaultPageSize = 20
	MaxPageSize     = 100
)

type Envelope[T any] struct {
	OK        bool   `json:"ok"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Data      T      `json:"data"`
}

type ExportAccountPayload struct {
	Tokens ExportTokensPayload `json:"tokens"`
	Meta   ExportMetaPayload   `json:"meta"`
}

type ExportTokensPayload struct {
	AccessToken       string `json:"access_token,omitempty"`
	IDToken           string `json:"id_token,omitempty"`
	RefreshToken      string `json:"refresh_token,omitempty"`
	AccountID         string `json:"account_id"`
	APIKeyAccessToken string `json:"api_key_access_token,omitempty"`
	LastRefresh       string `json:"last_refresh,omitempty"`
}

type ExportMetaPayload struct {
	Label            string            `json:"label"`
	Issuer           string            `json:"issuer"`
	GroupName        *string           `json:"groupName,omitempty"`
	Status           string            `json:"status"`
	WorkspaceID      *string           `json:"workspaceId,omitempty"`
	ChatGPTAccountID *string           `json:"chatgptAccountId,omitempty"`
	Email            *string           `json:"email,omitempty"`
	BaseURL          *string           `json:"baseUrl,omitempty"`
	ProxyURL         *string           `json:"proxyUrl,omitempty"`
	Prefix           *string           `json:"prefix,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
	ExportedAt       int64             `json:"exportedAt"`
}

func Success[T any](data T) Envelope[T] {
	return Envelope[T]{
		OK:        true,
		Code:      "",
		Message:   "",
		Retryable: false,
		Data:      data,
	}
}

func Failure(code, message string, retryable bool) Envelope[any] {
	return Envelope[any]{
		OK:        false,
		Code:      code,
		Message:   message,
		Retryable: retryable,
		Data:      nil,
	}
}

type AccountDTO struct {
	AccountID       string           `json:"accountId"`
	Label           string           `json:"label"`
	GroupName       string           `json:"groupName"`
	Status          string           `json:"status"`
	Sort            int64            `json:"sort"`
	RelayEnabled    bool             `json:"relayEnabled"`
	RuntimeSource   string           `json:"runtimeSource"`
	RuntimeIncluded bool             `json:"runtimeIncluded"`
	UsageSummary    *UsageSummaryDTO `json:"usageSummary"`
	LastSyncedAt    *time.Time       `json:"lastSyncedAt"`
	Stale           bool             `json:"stale"`
}

type UsageSummaryDTO struct {
	AvailabilityStatus string     `json:"availabilityStatus"`
	UsedPercent        *float64   `json:"usedPercent"`
	WindowMinutes      *int64     `json:"windowMinutes"`
	CapturedAt         *time.Time `json:"capturedAt"`
}

type AccountDetailDTO struct {
	AccountDTO
	UsageSnapshot *RPCUsageSnapshot `json:"usageSnapshot"`
}

type AccountUsageData struct {
	AccountID    string            `json:"accountId"`
	UsageSummary *UsageSummaryDTO  `json:"usageSummary"`
	Snapshot     *RPCUsageSnapshot `json:"snapshot"`
}

type AccountListData struct {
	Items       []AccountDTO `json:"items"`
	Total       int          `json:"total"`
	Page        int          `json:"page"`
	PageSize    int          `json:"pageSize"`
	MaxPageSize int          `json:"maxPageSize"`
}

type UsageRefreshBatchItemData struct {
	AccountID    string            `json:"accountId"`
	Success      bool              `json:"success"`
	Reason       *string           `json:"reason"`
	UsageSummary *UsageSummaryDTO  `json:"usageSummary"`
	Snapshot     *RPCUsageSnapshot `json:"snapshot"`
}

type UsageRefreshBatchData struct {
	Items        []UsageRefreshBatchItemData `json:"items"`
	Total        int                         `json:"total"`
	SuccessCount int                         `json:"successCount"`
	FailedCount  int                         `json:"failedCount"`
}

type Pagination struct {
	Page     int
	PageSize int
}

func ParsePagination(pageRaw, pageSizeRaw string) Pagination {
	page := 0
	pageSize := 0

	if parsed, err := strconv.Atoi(strings.TrimSpace(pageRaw)); err == nil {
		page = parsed
	}
	if parsed, err := strconv.Atoi(strings.TrimSpace(pageSizeRaw)); err == nil {
		pageSize = parsed
	}

	return NormalizePagination(page, pageSize)
}

func NormalizePagination(page, pageSize int) Pagination {
	if page < DefaultPage {
		page = DefaultPage
	}

	if pageSize < 1 {
		pageSize = DefaultPageSize
	} else if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}

	return Pagination{Page: page, PageSize: pageSize}
}
