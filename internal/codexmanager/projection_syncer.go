package codexmanager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type ProjectionSyncWorker struct {
	service    *RPCService
	repository *ProjectionRepository
	now        func() time.Time
	pageSize   int
}

func NewProjectionSyncWorker(service *RPCService, repository *ProjectionRepository) (*ProjectionSyncWorker, error) {
	if service == nil {
		return nil, fmt.Errorf("rpc service is required")
	}
	if repository == nil {
		return nil, fmt.Errorf("projection repository is required")
	}
	return &ProjectionSyncWorker{
		service:    service,
		repository: repository,
		now:        func() time.Time { return time.Now().UTC() },
		pageSize:   defaultProjectionSyncPages,
	}, nil
}

func (w *ProjectionSyncWorker) SetClock(nowFn func() time.Time) {
	if w == nil {
		return
	}
	if nowFn == nil {
		w.now = func() time.Time { return time.Now().UTC() }
		return
	}
	w.now = nowFn
}

func (w *ProjectionSyncWorker) SetPageSize(pageSize int) {
	if w == nil {
		return
	}
	if pageSize <= 0 {
		w.pageSize = defaultProjectionSyncPages
		return
	}
	w.pageSize = pageSize
}

func (w *ProjectionSyncWorker) Sync(ctx context.Context) (ProjectionSyncSummary, error) {
	if w == nil || w.service == nil || w.repository == nil {
		return ProjectionSyncSummary{}, fmt.Errorf("projection sync worker is not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := w.now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	syncedAt := now().UTC()

	summary := ProjectionSyncSummary{SyncedAt: syncedAt}

	accounts, err := w.listAllAccounts(ctx)
	if err != nil {
		return summary, err
	}
	summary.UpstreamAccountCnt = len(accounts)

	usageByAccount := map[string]RPCUsageSnapshot{}
	usageSnapshots, usageErr := w.listUsageByAccount(ctx)
	if usageErr != nil {
		summary.UsageStale = true
		summary.UsageError = usageErr.Error()
	} else {
		usageByAccount = usageSnapshots
	}
	summary.UsageSnapshotCnt = len(usageByAccount)

	projected := make([]ProjectionAccount, 0, len(accounts))
	for _, account := range accounts {
		usageSnapshot, hasUsage := usageByAccount[strings.TrimSpace(account.ID)]
		var usagePtr *RPCUsageSnapshot
		if hasUsage {
			usageCopy := usageSnapshot
			usagePtr = &usageCopy
		}
		projection := buildProjectionAccount(account, usagePtr, syncedAt)
		if projection.ProjectionID == "" {
			continue
		}
		projected = append(projected, projection)
	}

	applySummary, err := w.repository.ApplySync(projected, syncedAt)
	if err != nil {
		return summary, err
	}
	applySummary.UpstreamAccountCnt = summary.UpstreamAccountCnt
	applySummary.UsageSnapshotCnt = summary.UsageSnapshotCnt
	applySummary.UsageStale = summary.UsageStale
	applySummary.UsageError = summary.UsageError
	return applySummary, nil
}

func (w *ProjectionSyncWorker) listAllAccounts(ctx context.Context) ([]RPCAccountSummary, error) {
	pageSize := w.pageSize
	if pageSize <= 0 {
		pageSize = defaultProjectionSyncPages
	}
	if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}

	all := make([]RPCAccountSummary, 0, pageSize)
	page := 1
	maxPages := 2000
	for page <= maxPages {
		result, err := w.service.ListAccounts(ctx, RPCAccountListParams{Page: page, PageSize: pageSize})
		if err != nil {
			return nil, err
		}
		if len(result.Items) == 0 {
			break
		}
		all = append(all, result.Items...)
		if result.Total > 0 && int64(len(all)) >= result.Total {
			break
		}
		if len(result.Items) < pageSize {
			break
		}
		page++
	}
	return all, nil
}

func (w *ProjectionSyncWorker) listUsageByAccount(ctx context.Context) (map[string]RPCUsageSnapshot, error) {
	result, err := w.service.ListUsage(ctx)
	if err != nil {
		return nil, err
	}
	usageByAccount := make(map[string]RPCUsageSnapshot, len(result.Items))
	for i := range result.Items {
		item := result.Items[i]
		if item.AccountID == nil {
			continue
		}
		accountID := strings.TrimSpace(*item.AccountID)
		if accountID == "" {
			continue
		}
		usageByAccount[accountID] = item
	}
	return usageByAccount, nil
}

func buildProjectionAccount(account RPCAccountSummary, usage *RPCUsageSnapshot, syncedAt time.Time) ProjectionAccount {
	accountID := strings.TrimSpace(account.ID)
	projectionID := projectionIDFromAccountID(accountID)
	status := strings.TrimSpace(account.Status)
	health := projectionHealthFromUsage(usage, status, syncedAt)
	groupName := ""
	if account.GroupName != nil {
		groupName = strings.TrimSpace(*account.GroupName)
	}
	projection := ProjectionAccount{
		ProjectionID:   projectionID,
		AccountID:      accountID,
		ExternalRef:    accountID,
		Label:          strings.TrimSpace(account.Label),
		GroupName:      groupName,
		RelayEnabled:   true,
		Source:         ProjectionSourceCodexMGR,
		LastSyncedAt:   syncedAt.UTC(),
		UpstreamSort:   account.Sort,
		UpstreamStatus: status,
		UsageSummary:   projectionUsageSummaryFromUsage(usage),
		Health:         health,
		Tombstone:      false,
		SchemaVersion:  projectionRecordSchemaV1,
	}
	projection.VersionHash = projectionVersionHash(projection, account)
	return projection
}

func projectionUsageSummaryFromUsage(usage *RPCUsageSnapshot) ProjectionUsageSummary {
	summary := ProjectionUsageSummary{}
	if usage == nil {
		return summary
	}
	if usage.AvailabilityStatus != nil {
		summary.AvailabilityStatus = strings.TrimSpace(*usage.AvailabilityStatus)
	}
	if usage.UsedPercent != nil {
		usedPercent := *usage.UsedPercent
		summary.UsedPercent = &usedPercent
	}
	if usage.WindowMinutes != nil {
		windowMinutes := *usage.WindowMinutes
		summary.WindowMinutes = &windowMinutes
	}
	if usage.CapturedAt != nil && *usage.CapturedAt > 0 {
		capturedAt := time.Unix(*usage.CapturedAt, 0).UTC()
		summary.CapturedAt = &capturedAt
	}
	return summary
}

func projectionHealthFromUsage(usage *RPCUsageSnapshot, upstreamStatus string, syncedAt time.Time) ProjectionHealth {
	health := ProjectionHealth{}
	if usage != nil && usage.AvailabilityStatus != nil {
		health.AvailabilityStatus = strings.TrimSpace(*usage.AvailabilityStatus)
	}
	statusLower := strings.ToLower(strings.TrimSpace(health.AvailabilityStatus))
	if statusLower != "" && statusLower != "ok" && statusLower != "available" {
		health.BackoffLevel = 1
		health.Reason = health.AvailabilityStatus
	}

	used := float64Value(usage, func(snapshot *RPCUsageSnapshot) *float64 { return snapshot.UsedPercent })
	secondaryUsed := float64Value(usage, func(snapshot *RPCUsageSnapshot) *float64 { return snapshot.SecondaryUsedPercent })

	if used >= 100 || secondaryUsed >= 100 {
		if health.BackoffLevel < 2 {
			health.BackoffLevel = 2
		}
		health.Reason = "quota_exhausted"
	} else if used >= 80 || secondaryUsed >= 80 {
		if health.BackoffLevel < 1 {
			health.BackoffLevel = 1
		}
		if strings.TrimSpace(health.Reason) == "" {
			health.Reason = "low_quota"
		}
	}

	if strings.EqualFold(strings.TrimSpace(upstreamStatus), "inactive") {
		if health.BackoffLevel < 1 {
			health.BackoffLevel = 1
		}
		health.Reason = "inactive"
	}

	resetsAt := maxInt64(
		int64Value(usage, func(snapshot *RPCUsageSnapshot) *int64 { return snapshot.ResetsAt }),
		int64Value(usage, func(snapshot *RPCUsageSnapshot) *int64 { return snapshot.SecondaryResetsAt }),
	)
	if resetsAt > syncedAt.UTC().Unix() {
		ts := time.Unix(resetsAt, 0).UTC()
		health.BackoffUntil = &ts
		if health.BackoffLevel == 0 {
			health.BackoffLevel = 1
		}
	}

	if strings.TrimSpace(health.Reason) == "" && health.BackoffLevel > 0 {
		health.Reason = "backoff"
	}

	return health
}

func projectionVersionHash(item ProjectionAccount, account RPCAccountSummary) string {
	group := ""
	if account.GroupName != nil {
		group = strings.TrimSpace(*account.GroupName)
	}
	backoffUnix := ""
	if item.Health.BackoffUntil != nil {
		backoffUnix = strconv.FormatInt(item.Health.BackoffUntil.UTC().Unix(), 10)
	}
	usageUsedPercent := ""
	if item.UsageSummary.UsedPercent != nil {
		usageUsedPercent = strconv.FormatFloat(*item.UsageSummary.UsedPercent, 'f', -1, 64)
	}
	usageWindowMinutes := ""
	if item.UsageSummary.WindowMinutes != nil {
		usageWindowMinutes = strconv.FormatInt(*item.UsageSummary.WindowMinutes, 10)
	}
	usageCapturedAt := ""
	if item.UsageSummary.CapturedAt != nil {
		usageCapturedAt = strconv.FormatInt(item.UsageSummary.CapturedAt.UTC().Unix(), 10)
	}
	payload := strings.Join([]string{
		item.ProjectionID,
		item.AccountID,
		item.ExternalRef,
		item.Label,
		item.GroupName,
		item.Source,
		item.UpstreamStatus,
		strconv.FormatInt(item.UpstreamSort, 10),
		group,
		item.UsageSummary.AvailabilityStatus,
		usageUsedPercent,
		usageWindowMinutes,
		usageCapturedAt,
		item.Health.AvailabilityStatus,
		strconv.Itoa(item.Health.BackoffLevel),
		backoffUnix,
		item.Health.Reason,
	}, "|")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:8])
}

func float64Value(snapshot *RPCUsageSnapshot, getter func(*RPCUsageSnapshot) *float64) float64 {
	if snapshot == nil || getter == nil {
		return 0
	}
	ptr := getter(snapshot)
	if ptr == nil {
		return 0
	}
	return *ptr
}

func int64Value(snapshot *RPCUsageSnapshot, getter func(*RPCUsageSnapshot) *int64) int64 {
	if snapshot == nil || getter == nil {
		return 0
	}
	ptr := getter(snapshot)
	if ptr == nil {
		return 0
	}
	return *ptr
}

func maxInt64(values ...int64) int64 {
	if len(values) == 0 {
		return 0
	}
	out := values[0]
	for i := 1; i < len(values); i++ {
		if values[i] > out {
			out = values[i]
		}
	}
	return out
}
