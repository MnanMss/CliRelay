package codexmanager

import "time"

const (
	LoginFlowStatusInProgress = "in_progress"
	LoginFlowStatusSuccess    = "success"
	LoginFlowStatusFailed     = "failed"
	LoginFlowStatusCancelled  = "cancelled"
	LoginFlowStatusTimedOut   = "timed_out"
	LoginFlowStatusUnknown    = "unknown"
)

type loginStartActionRequest struct {
	Type        string `json:"type"`
	OpenBrowser *bool  `json:"openBrowser"`
	Note        string `json:"note"`
	Tags        string `json:"tags"`
	GroupName   string `json:"groupName"`
	WorkspaceID string `json:"workspaceId"`
}

type loginCompleteActionRequest struct {
	State       string `json:"state"`
	Code        string `json:"code"`
	RedirectURI string `json:"redirectUri"`
}

type importActionRequest struct {
	Contents []string `json:"contents"`
	Content  string   `json:"content"`
}

type relayStatePatchRequest struct {
	State        any   `json:"state"`
	RelayEnabled *bool `json:"relayEnabled"`
}

type usageRefreshBatchRequest struct {
	AccountIDs []string `json:"accountIds"`
}

type LoginStartActionData struct {
	LoginID     string             `json:"loginId"`
	AuthURL     string             `json:"authUrl"`
	LoginType   string             `json:"loginType"`
	Issuer      string             `json:"issuer"`
	ClientID    string             `json:"clientId"`
	RedirectURI string             `json:"redirectUri"`
	Warning     *string            `json:"warning"`
	Device      *RPCDeviceAuthInfo `json:"device"`
}

type LoginStatusActionData struct {
	LoginID        string     `json:"loginId"`
	Status         string     `json:"status"`
	UpstreamStatus string     `json:"upstreamStatus"`
	Terminal       bool       `json:"terminal"`
	Error          *string    `json:"error"`
	UpdatedAt      *time.Time `json:"updatedAt"`
}

type LoginCompleteActionData struct {
	Status    string `json:"status"`
	Completed bool   `json:"completed"`
}

type ImportActionErrorData struct {
	Index   int64  `json:"index"`
	Message string `json:"message"`
}

type ImportActionData struct {
	Total   int64                   `json:"total"`
	Created int64                   `json:"created"`
	Updated int64                   `json:"updated"`
	Failed  int64                   `json:"failed"`
	Errors  []ImportActionErrorData `json:"errors"`
}

type DeleteActionData struct {
	AccountID          string `json:"accountId"`
	Removed            bool   `json:"removed"`
	AlreadyRemoved     bool   `json:"alreadyRemoved"`
	NotFoundButHandled bool   `json:"notFoundButHandled"`
}
