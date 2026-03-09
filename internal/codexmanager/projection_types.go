package codexmanager

import "time"

const (
	ProjectionIDPrefix          = "cm:"
	ProjectionSourceCodexMGR    = RuntimeSourceCodexManager
	projectionStoreSchemaV1     = 1
	overlayStoreSchemaV1        = 1
	credentialStoreSchemaV1     = 1
	projectionRecordSchemaV1    = 1
	projectionStateDirName      = "codexmanager"
	projectionIndexFileName     = "projection_index.json"
	overlayStateFileName        = "overlay_state.json"
	credentialStateFileName     = "credential_state.json"
	defaultProjectionSyncPages  = MaxPageSize
	codexManagerRuntimePriority = -1000000
)

type ProjectionHealth struct {
	AvailabilityStatus string     `json:"availabilityStatus,omitempty"`
	BackoffLevel       int        `json:"backoffLevel,omitempty"`
	BackoffUntil       *time.Time `json:"backoffUntil,omitempty"`
	Reason             string     `json:"reason,omitempty"`
}

type ProjectionUsageSummary struct {
	AvailabilityStatus string     `json:"availabilityStatus,omitempty"`
	UsedPercent        *float64   `json:"usedPercent,omitempty"`
	WindowMinutes      *int64     `json:"windowMinutes,omitempty"`
	CapturedAt         *time.Time `json:"capturedAt,omitempty"`
}

type ProjectionAccount struct {
	ProjectionID   string                 `json:"projectionId"`
	AccountID      string                 `json:"accountId"`
	ExternalRef    string                 `json:"externalRef"`
	Label          string                 `json:"label,omitempty"`
	GroupName      string                 `json:"groupName,omitempty"`
	RelayEnabled   bool                   `json:"relayEnabled"`
	Source         string                 `json:"source"`
	LastSyncedAt   time.Time              `json:"lastSyncedAt"`
	VersionHash    string                 `json:"versionHash"`
	UpstreamSort   int64                  `json:"upstreamSort"`
	UpstreamStatus string                 `json:"upstreamStatus,omitempty"`
	UsageSummary   ProjectionUsageSummary `json:"usageSummary,omitempty"`
	Health         ProjectionHealth       `json:"health"`
	Tombstone      bool                   `json:"tombstone"`
	SchemaVersion  int                    `json:"schemaVersion"`
}

type ProjectionSyncSummary struct {
	SyncedAt           time.Time `json:"syncedAt"`
	UpstreamAccountCnt int       `json:"upstreamAccountCount"`
	UsageSnapshotCnt   int       `json:"usageSnapshotCount"`
	Added              int       `json:"added"`
	Updated            int       `json:"updated"`
	Tombstoned         int       `json:"tombstoned"`
	TotalAfter         int       `json:"totalAfter"`
	UsageStale         bool      `json:"usageStale"`
	UsageError         string    `json:"usageError,omitempty"`
}
