package codexmanager

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	log "github.com/sirupsen/logrus"
)

type Handler struct {
	cfg      *config.Config
	stateDir string
	service  *RPCService
}

const usageRefreshBatchConcurrency = 4

func NewHandler(cfg *config.Config) *Handler {
	handler := &Handler{
		cfg:      cfg,
		stateDir: ProjectionStateDir(cfg),
	}

	if endpoint := strings.TrimSpace(handler.rpcEndpoint()); endpoint != "" {
		client, err := NewRPCClient(RPCClientConfig{
			Endpoint:       endpoint,
			RequestTimeout: codexManagerRPCRequestTimeout(cfg),
		})
		if err == nil {
			handler.service = NewRPCService(client)
		}
	}

	return handler
}

func (h *Handler) ListAccounts(c *gin.Context) {
	repository, err := h.currentRepository()
	if err != nil {
		h.writeError(c, err)
		return
	}

	paging := ParsePagination(c.Query("page"), c.Query("pageSize"))
	query := strings.TrimSpace(c.Query("query"))
	filtered := filterProjectionAccounts(repository.List(), query)

	data := AccountListData{
		Items:       accountDTOsFromProjection(paginateProjectionAccounts(filtered, paging)),
		Total:       len(filtered),
		Page:        paging.Page,
		PageSize:    paging.PageSize,
		MaxPageSize: MaxPageSize,
	}

	c.JSON(http.StatusOK, Success(data))
}

func (h *Handler) ListUsage(c *gin.Context) {
	repository, err := h.currentRepository()
	if err != nil {
		h.writeError(c, err)
		return
	}

	paging := ParsePagination(c.Query("page"), c.Query("pageSize"))
	query := strings.TrimSpace(c.Query("query"))
	filtered := filterProjectionAccounts(repository.List(), query)
	paged := paginateProjectionAccounts(filtered, paging)

	data := AccountListData{
		Items:       h.accountDTOsWithUsageSummary(c.Request.Context(), paged),
		Total:       len(filtered),
		Page:        paging.Page,
		PageSize:    paging.PageSize,
		MaxPageSize: MaxPageSize,
	}

	c.JSON(http.StatusOK, Success(data))
}

func (h *Handler) GetAccount(c *gin.Context) {
	repository, err := h.currentRepository()
	if err != nil {
		h.writeError(c, err)
		return
	}

	projection, err := requireProjectionAccount(repository, c.Param("accountId"))
	if err != nil {
		h.writeError(c, err)
		return
	}

	usageSnapshot := h.readUsageSnapshot(c.Request.Context(), projection.AccountID)
	data := accountDetailDTOFromProjection(projection, usageSnapshot)
	c.JSON(http.StatusOK, Success(data))
}

func (h *Handler) GetAccountUsage(c *gin.Context) {
	repository, err := h.currentRepository()
	if err != nil {
		h.writeError(c, err)
		return
	}

	projection, err := requireProjectionAccount(repository, c.Param("accountId"))
	if err != nil {
		h.writeError(c, err)
		return
	}

	snapshot := h.readUsageSnapshot(c.Request.Context(), projection.AccountID)
	c.JSON(http.StatusOK, Success(accountUsageDataFromProjection(projection, snapshot)))
}

func (h *Handler) RefreshAccountUsage(c *gin.Context) {
	repository, err := h.currentRepository()
	if err != nil {
		h.writeError(c, err)
		return
	}

	projection, err := requireProjectionAccount(repository, c.Param("accountId"))
	if err != nil {
		h.writeError(c, err)
		return
	}

	if err := h.service.RefreshUsage(c.Request.Context(), projection.AccountID); err != nil {
		h.writeError(c, err)
		return
	}

	snapshot := h.readUsageSnapshot(c.Request.Context(), projection.AccountID)
	c.JSON(http.StatusOK, Success(accountUsageDataFromProjection(projection, snapshot)))
}

func (h *Handler) RefreshUsageBatch(c *gin.Context) {
	var request usageRefreshBatchRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		h.writeError(c, badRequestError("invalid usage refresh batch payload"))
		return
	}

	accountIDs, hasSelection := normalizeRequestedAccountIDs(request.AccountIDs)
	if !hasSelection {
		h.writeError(c, badRequestError("accountIds is required"))
		return
	}

	repository, err := h.currentRepository()
	if err != nil {
		h.writeError(c, err)
		return
	}

	projectionsByAccount := make(map[string]ProjectionAccount)
	projected := repository.List()
	for i := range projected {
		accountID := strings.TrimSpace(projected[i].AccountID)
		if accountID == "" {
			continue
		}
		projectionsByAccount[accountID] = projected[i]
	}

	items := h.refreshUsageBatchResults(c.Request.Context(), accountIDs, projectionsByAccount)
	data := UsageRefreshBatchData{
		Items: items,
		Total: len(items),
	}
	for i := range items {
		if items[i].Success {
			data.SuccessCount++
		} else {
			data.FailedCount++
		}
	}

	c.JSON(http.StatusOK, Success(data))
}

func (h *Handler) StartLogin(c *gin.Context) {
	var request loginStartActionRequest
	if err := decodeOptionalJSON(c, &request); err != nil {
		h.writeError(c, badRequestError("invalid login start payload"))
		return
	}

	result, err := h.service.StartLogin(c.Request.Context(), RPCLoginStartRequest{
		Type:        strings.TrimSpace(request.Type),
		OpenBrowser: request.OpenBrowser,
		Note:        strings.TrimSpace(request.Note),
		Tags:        strings.TrimSpace(request.Tags),
		GroupName:   strings.TrimSpace(request.GroupName),
		WorkspaceID: strings.TrimSpace(request.WorkspaceID),
	})
	if err != nil {
		h.writeError(c, err)
		return
	}

	data := LoginStartActionData{
		LoginID:     strings.TrimSpace(result.LoginID),
		AuthURL:     strings.TrimSpace(result.AuthURL),
		LoginType:   strings.TrimSpace(result.LoginType),
		Issuer:      strings.TrimSpace(result.Issuer),
		ClientID:    strings.TrimSpace(result.ClientID),
		RedirectURI: strings.TrimSpace(result.RedirectURI),
		Warning:     normalizeStringPointer(result.Warning),
		Device:      result.Device,
	}

	c.JSON(http.StatusOK, Success(data))
}

func (h *Handler) GetLoginStatus(c *gin.Context) {
	loginID := strings.TrimSpace(c.Param("loginId"))
	if loginID == "" {
		h.writeError(c, badRequestError("invalid loginId"))
		return
	}

	result, err := h.service.GetLoginStatus(c.Request.Context(), loginID)
	if err != nil {
		h.writeError(c, err)
		return
	}

	status, terminal := normalizeLoginFlowStatus(result.Status)
	upstreamStatus := strings.TrimSpace(result.Status)
	if upstreamStatus == "" {
		upstreamStatus = LoginFlowStatusUnknown
	}

	data := LoginStatusActionData{
		LoginID:        loginID,
		Status:         status,
		UpstreamStatus: upstreamStatus,
		Terminal:       terminal,
		Error:          normalizeStringPointer(result.Error),
		UpdatedAt:      unixSecondsToTimePointer(result.UpdatedAt),
	}

	c.JSON(http.StatusOK, Success(data))
}

func (h *Handler) CompleteLogin(c *gin.Context) {
	var request loginCompleteActionRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		h.writeError(c, badRequestError("invalid login complete payload"))
		return
	}

	request.State = strings.TrimSpace(request.State)
	request.Code = strings.TrimSpace(request.Code)
	request.RedirectURI = strings.TrimSpace(request.RedirectURI)
	if request.State == "" || request.Code == "" {
		h.writeError(c, badRequestError("state and code are required"))
		return
	}

	err := h.service.CompleteLogin(c.Request.Context(), RPCLoginCompleteRequest{
		State:       request.State,
		Code:        request.Code,
		RedirectURI: request.RedirectURI,
	})
	if err != nil {
		h.writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, Success(LoginCompleteActionData{Status: LoginFlowStatusSuccess, Completed: true}))
}

func (h *Handler) ImportAccounts(c *gin.Context) {
	var request importActionRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		h.writeError(c, badRequestError("invalid import payload"))
		return
	}

	contents := normalizeImportContents(request.Contents, request.Content)
	if len(contents) == 0 {
		h.writeError(c, badRequestError("contents or content is required"))
		return
	}

	result, err := h.service.ImportAccounts(c.Request.Context(), contents)
	if err != nil {
		h.writeError(c, err)
		return
	}

	importedAt := time.Now().UTC()
	if err := h.syncImportedExecutableCredentials(contents, importedAt); err != nil {
		h.writeError(c, err)
		return
	}
	if err := h.syncImportedProjectionAccounts(contents, importedAt); err != nil {
		h.writeError(c, err)
		return
	}

	data := ImportActionData{
		Total:   result.Total,
		Created: result.Created,
		Updated: result.Updated,
		Failed:  result.Failed,
		Errors:  importActionErrorsFromRPC(result.Errors),
	}

	c.JSON(http.StatusOK, Success(data))
}

func (h *Handler) syncImportedExecutableCredentials(contents []string, updatedAt time.Time) error {
	syncedCount, err := SyncImportedCredentials(h.stateDir, contents, updatedAt)
	if err != nil {
		log.WithError(err).Error("codex-manager import succeeded but local credential sync failed")
		return NewCodedError(http.StatusInternalServerError, CodeInternalError, "codex-manager import succeeded but local credential sync failed", false)
	}
	if syncedCount == 0 && len(ParseCredentialRecords(contents)) == 0 {
		log.Error("codex-manager import succeeded but import contents produced no executable local credentials")
		return NewCodedError(http.StatusInternalServerError, CodeInternalError, "codex-manager import succeeded but import contents produced no executable local credentials", false)
	}
	return nil
}

func (h *Handler) syncImportedProjectionAccounts(contents []string, updatedAt time.Time) error {
	repository, err := h.currentRepository()
	if err != nil {
		log.WithError(err).Error("codex-manager import succeeded but local projection sync failed")
		return NewCodedError(http.StatusInternalServerError, CodeInternalError, "codex-manager import succeeded but local projection sync failed", false)
	}

	records := ParseCredentialRecords(contents)
	if len(records) == 0 {
		return nil
	}

	current := repository.List()
	mergedByProjectionID := make(map[string]ProjectionAccount, len(current)+len(records))
	var nextSort int64
	for i := range current {
		projection := current[i]
		projectionID := strings.TrimSpace(projection.ProjectionID)
		if projectionID == "" {
			projectionID = projectionIDFromAccountID(projection.AccountID)
		}
		if projectionID == "" {
			continue
		}
		mergedByProjectionID[projectionID] = projection
		if projection.UpstreamSort > nextSort {
			nextSort = projection.UpstreamSort
		}
	}

	for i := range records {
		projection, ok := mergeImportedProjectionAccount(mergedByProjectionID, records[i], updatedAt, &nextSort)
		if !ok {
			continue
		}
		mergedByProjectionID[projection.ProjectionID] = projection
	}

	merged := make([]ProjectionAccount, 0, len(mergedByProjectionID))
	for _, projection := range mergedByProjectionID {
		merged = append(merged, projection)
	}
	if _, err := repository.ApplySync(merged, updatedAt); err != nil {
		log.WithError(err).Error("codex-manager import succeeded but local projection sync failed")
		return NewCodedError(http.StatusInternalServerError, CodeInternalError, "codex-manager import succeeded but local projection sync failed", false)
	}
	return nil
}

func mergeImportedProjectionAccount(current map[string]ProjectionAccount, record CredentialRecord, updatedAt time.Time, nextSort *int64) (ProjectionAccount, bool) {
	accountID := normalizeCredentialAccountID(record.AccountID)
	if accountID == "" {
		return ProjectionAccount{}, false
	}
	projectionID := projectionIDFromAccountID(accountID)
	if projectionID == "" {
		return ProjectionAccount{}, false
	}
	now := updatedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	projection, existed := current[projectionID]
	wasTombstoned := existed && projection.Tombstone
	if !existed {
		projection = ProjectionAccount{
			ProjectionID:  projectionID,
			AccountID:     accountID,
			ExternalRef:   accountID,
			RelayEnabled:  true,
			Source:        ProjectionSourceCodexMGR,
			LastSyncedAt:  now,
			Tombstone:     false,
			SchemaVersion: projectionRecordSchemaV1,
		}
		if nextSort != nil {
			*nextSort += 1
			projection.UpstreamSort = *nextSort
		}
	}

	projection.ProjectionID = projectionID
	projection.AccountID = accountID
	if strings.TrimSpace(projection.ExternalRef) == "" {
		projection.ExternalRef = accountID
	}
	if strings.TrimSpace(projection.Label) == "" {
		projection.Label = importedProjectionLabel(record)
	}
	if strings.TrimSpace(projection.Source) == "" {
		projection.Source = ProjectionSourceCodexMGR
	}
	projection.LastSyncedAt = now
	projection.Tombstone = false
	projection.SchemaVersion = projectionRecordSchemaV1
	if wasTombstoned {
		projection.Health = ProjectionHealth{}
		projection.UsageSummary = ProjectionUsageSummary{}
	}
	return projection, true
}

func importedProjectionLabel(record CredentialRecord) string {
	if email := strings.TrimSpace(record.Email); email != "" {
		return email
	}
	return normalizeCredentialAccountID(record.AccountID)
}

func (h *Handler) DeleteAccount(c *gin.Context) {
	accountID := strings.TrimSpace(c.Param("accountId"))
	if accountID == "" {
		h.writeError(c, ErrInvalidAccountID)
		return
	}

	err := h.service.DeleteAccount(c.Request.Context(), accountID)
	if err != nil {
		if errors.Is(err, ErrAccountNotFound) {
			h.removeLocalCredential(accountID)
			c.JSON(http.StatusOK, Success(DeleteActionData{
				AccountID:          accountID,
				Removed:            false,
				AlreadyRemoved:     true,
				NotFoundButHandled: true,
			}))
			return
		}
		h.writeError(c, err)
		return
	}

	h.removeLocalCredential(accountID)

	c.JSON(http.StatusOK, Success(DeleteActionData{
		AccountID:          accountID,
		Removed:            true,
		AlreadyRemoved:     false,
		NotFoundButHandled: false,
	}))
}

func (h *Handler) removeLocalCredential(accountID string) {
	if h == nil {
		return
	}
	if err := RemoveStoredCredential(h.stateDir, accountID, time.Now().UTC()); err != nil && !errors.Is(err, ErrInvalidAccountID) {
		log.WithError(err).Warn("codex-manager local credential cleanup failed")
	}
}

func (h *Handler) PatchRelayState(c *gin.Context) {
	repository, err := h.currentRepository()
	if err != nil {
		h.writeError(c, err)
		return
	}

	accountID := strings.TrimSpace(c.Param("accountId"))
	if accountID == "" {
		h.writeError(c, ErrInvalidAccountID)
		return
	}

	var request relayStatePatchRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		h.writeError(c, badRequestError("invalid relay-state payload"))
		return
	}

	relayEnabled, err := parseRelayEnabledFromRequest(request)
	if err != nil {
		h.writeError(c, badRequestError(err.Error()))
		return
	}

	projectionID, err := ProjectionIDForAccountID(accountID)
	if err != nil {
		h.writeError(c, err)
		return
	}

	updatedProjection, err := repository.SetRelayEnabled(projectionID, relayEnabled, time.Now().UTC())
	if err != nil {
		h.writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, Success(accountDTOFromProjection(updatedProjection)))
}

func (h *Handler) currentRepository() (*ProjectionRepository, error) {
	if h == nil {
		return nil, NewCodedError(http.StatusInternalServerError, CodeInternalError, "codex-manager handler is not initialized", false)
	}
	repository, err := NewProjectionRepository(h.stateDir)
	if err != nil {
		return nil, NewCodedError(http.StatusInternalServerError, CodeInternalError, "codex-manager projection repository is not initialized", false)
	}
	return repository, nil
}

func (h *Handler) rpcEndpoint() string {
	if h == nil || h.cfg == nil {
		return ""
	}
	return h.cfg.CodexManager.Endpoint
}

func (h *Handler) readUsageSnapshot(ctx context.Context, accountID string) *RPCUsageSnapshot {
	if h == nil || h.service == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := h.service.ReadUsage(ctx, accountID)
	if err != nil || result.Snapshot == nil {
		return nil
	}
	copy := *result.Snapshot
	return &copy
}

func filterProjectionAccounts(accounts []ProjectionAccount, query string) []ProjectionAccount {
	filtered := make([]ProjectionAccount, 0, len(accounts))
	for i := range accounts {
		candidate := accounts[i]
		if matchesAccountQuery(candidate, query) {
			filtered = append(filtered, candidate)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		left := filtered[i]
		right := filtered[j]
		if left.UpstreamSort != right.UpstreamSort {
			return left.UpstreamSort < right.UpstreamSort
		}
		return strings.ToLower(left.AccountID) < strings.ToLower(right.AccountID)
	})
	return filtered
}

func paginateProjectionAccounts(accounts []ProjectionAccount, paging Pagination) []ProjectionAccount {
	if len(accounts) == 0 {
		return []ProjectionAccount{}
	}
	start := (paging.Page - 1) * paging.PageSize
	if start >= len(accounts) {
		return []ProjectionAccount{}
	}
	end := start + paging.PageSize
	if end > len(accounts) {
		end = len(accounts)
	}
	paged := make([]ProjectionAccount, 0, end-start)
	for i := start; i < end; i++ {
		paged = append(paged, accounts[i])
	}
	return paged
}

func accountDTOsFromProjection(accounts []ProjectionAccount) []AccountDTO {
	if len(accounts) == 0 {
		return []AccountDTO{}
	}
	items := make([]AccountDTO, 0, len(accounts))
	for i := range accounts {
		items = append(items, accountDTOFromProjection(accounts[i]))
	}
	return items
}

func (h *Handler) accountDTOsWithUsageSummary(ctx context.Context, accounts []ProjectionAccount) []AccountDTO {
	items := accountDTOsFromProjection(accounts)
	if len(items) == 0 || h == nil || h.service == nil {
		return items
	}

	needsSupplement := false
	for i := range items {
		if items[i].UsageSummary == nil {
			needsSupplement = true
			break
		}
	}
	if !needsSupplement {
		return items
	}

	usageByAccount, err := h.listUsageSnapshotsByAccount(ctx)
	if err != nil {
		return items
	}
	for i := range items {
		if items[i].UsageSummary != nil {
			continue
		}
		snapshot, ok := usageByAccount[items[i].AccountID]
		if !ok {
			continue
		}
		snapshotCopy := snapshot
		items[i].UsageSummary = usageSummaryDTOFromRPCSnapshot(&snapshotCopy)
	}
	return items
}

func accountDetailDTOFromProjection(item ProjectionAccount, usageSnapshot *RPCUsageSnapshot) AccountDetailDTO {
	data := accountDTOFromProjection(item)
	if usageSummary := usageSummaryDTOFromRPCSnapshot(usageSnapshot); usageSummary != nil {
		data.UsageSummary = usageSummary
	}
	return AccountDetailDTO{
		AccountDTO:    data,
		UsageSnapshot: usageSnapshot,
	}
}

func accountUsageDataFromProjection(item ProjectionAccount, usageSnapshot *RPCUsageSnapshot) AccountUsageData {
	data := AccountUsageData{
		AccountID:    strings.TrimSpace(item.AccountID),
		UsageSummary: usageSummaryDTOFromProjection(item.UsageSummary),
		Snapshot:     usageSnapshot,
	}
	if usageSummary := usageSummaryDTOFromRPCSnapshot(usageSnapshot); usageSummary != nil {
		data.UsageSummary = usageSummary
	}
	return data
}

func requireProjectionAccount(repository *ProjectionRepository, accountID string) (ProjectionAccount, error) {
	trimmedAccountID := strings.TrimSpace(accountID)
	if trimmedAccountID == "" {
		return ProjectionAccount{}, ErrInvalidAccountID
	}
	projectionID, err := ProjectionIDForAccountID(trimmedAccountID)
	if err != nil {
		return ProjectionAccount{}, err
	}
	projection, ok := repository.Get(projectionID)
	if !ok {
		return ProjectionAccount{}, ErrAccountNotFound
	}
	return projection, nil
}

func normalizeRequestedAccountIDs(accountIDs []string) ([]string, bool) {
	if len(accountIDs) == 0 {
		return nil, false
	}
	normalized := make([]string, 0, len(accountIDs))
	hasSelection := false
	for i := range accountIDs {
		trimmed := strings.TrimSpace(accountIDs[i])
		if trimmed != "" {
			hasSelection = true
		}
		normalized = append(normalized, trimmed)
	}
	return normalized, hasSelection
}

func (h *Handler) refreshUsageBatchResults(ctx context.Context, accountIDs []string, projectionsByAccount map[string]ProjectionAccount) []UsageRefreshBatchItemData {
	results := make([]UsageRefreshBatchItemData, len(accountIDs))
	if len(accountIDs) == 0 {
		return results
	}

	workerCount := usageRefreshBatchConcurrency
	if workerCount > len(accountIDs) {
		workerCount = len(accountIDs)
	}
	if workerCount < 1 {
		workerCount = 1
	}

	type usageRefreshJob struct {
		index     int
		accountID string
	}

	jobs := make(chan usageRefreshJob)
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				results[job.index] = h.refreshUsageBatchItem(ctx, job.accountID, projectionsByAccount)
			}
		}()
	}

	for i := range accountIDs {
		jobs <- usageRefreshJob{index: i, accountID: accountIDs[i]}
	}
	close(jobs)
	wg.Wait()

	return results
}

func (h *Handler) refreshUsageBatchItem(ctx context.Context, accountID string, projectionsByAccount map[string]ProjectionAccount) UsageRefreshBatchItemData {
	normalizedAccountID := strings.TrimSpace(accountID)
	if normalizedAccountID == "" {
		return UsageRefreshBatchItemData{
			AccountID: "",
			Success:   false,
			Reason:    stringPointer(ErrInvalidAccountID.Error()),
		}
	}

	projection, ok := projectionsByAccount[normalizedAccountID]
	if !ok {
		return UsageRefreshBatchItemData{
			AccountID: normalizedAccountID,
			Success:   false,
			Reason:    stringPointer(ErrAccountNotFound.Error()),
		}
	}

	if err := h.service.RefreshUsage(ctx, normalizedAccountID); err != nil {
		data := accountUsageDataFromProjection(projection, nil)
		return UsageRefreshBatchItemData{
			AccountID:    normalizedAccountID,
			Success:      false,
			Reason:       stringPointer(err.Error()),
			UsageSummary: data.UsageSummary,
			Snapshot:     data.Snapshot,
		}
	}

	snapshot := h.readUsageSnapshot(ctx, normalizedAccountID)
	data := accountUsageDataFromProjection(projection, snapshot)
	return UsageRefreshBatchItemData{
		AccountID:    normalizedAccountID,
		Success:      true,
		Reason:       nil,
		UsageSummary: data.UsageSummary,
		Snapshot:     data.Snapshot,
	}
}

func (h *Handler) listUsageSnapshotsByAccount(ctx context.Context) (map[string]RPCUsageSnapshot, error) {
	if h == nil || h.service == nil {
		return nil, NewCodedError(http.StatusInternalServerError, CodeInternalError, "codex-manager rpc service is not initialized", false)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := h.service.ListUsage(ctx)
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

func accountDTOFromProjection(item ProjectionAccount) AccountDTO {
	runtimeSource := strings.TrimSpace(item.Source)
	if runtimeSource == "" {
		runtimeSource = RuntimeSourceCodexManager
	}
	var lastSyncedAt *time.Time
	if !item.LastSyncedAt.IsZero() {
		ts := item.LastSyncedAt.UTC()
		lastSyncedAt = &ts
	}
	return AccountDTO{
		AccountID:       strings.TrimSpace(item.AccountID),
		Label:           strings.TrimSpace(item.Label),
		GroupName:       strings.TrimSpace(item.GroupName),
		Status:          strings.TrimSpace(item.UpstreamStatus),
		Sort:            item.UpstreamSort,
		RelayEnabled:    item.RelayEnabled,
		RuntimeSource:   runtimeSource,
		RuntimeIncluded: item.RelayEnabled && !item.Tombstone,
		UsageSummary:    usageSummaryDTOFromProjection(item.UsageSummary),
		LastSyncedAt:    lastSyncedAt,
		Stale:           item.Tombstone,
	}
}

func usageSummaryDTOFromProjection(summary ProjectionUsageSummary) *UsageSummaryDTO {
	if strings.TrimSpace(summary.AvailabilityStatus) == "" && summary.UsedPercent == nil && summary.WindowMinutes == nil && summary.CapturedAt == nil {
		return nil
	}
	dto := &UsageSummaryDTO{
		AvailabilityStatus: strings.TrimSpace(summary.AvailabilityStatus),
		UsedPercent:        cloneFloat64Ptr(summary.UsedPercent),
		WindowMinutes:      cloneInt64Ptr(summary.WindowMinutes),
		CapturedAt:         cloneTimePtr(summary.CapturedAt),
	}
	return dto
}

func usageSummaryDTOFromRPCSnapshot(snapshot *RPCUsageSnapshot) *UsageSummaryDTO {
	if snapshot == nil {
		return nil
	}
	summary := &UsageSummaryDTO{
		UsedPercent:   cloneFloat64Ptr(snapshot.UsedPercent),
		WindowMinutes: cloneInt64Ptr(snapshot.WindowMinutes),
	}
	if snapshot.AvailabilityStatus != nil {
		summary.AvailabilityStatus = strings.TrimSpace(*snapshot.AvailabilityStatus)
	}
	if snapshot.CapturedAt != nil && *snapshot.CapturedAt > 0 {
		capturedAt := time.Unix(*snapshot.CapturedAt, 0).UTC()
		summary.CapturedAt = &capturedAt
	}
	if strings.TrimSpace(summary.AvailabilityStatus) == "" && summary.UsedPercent == nil && summary.WindowMinutes == nil && summary.CapturedAt == nil {
		return nil
	}
	return summary
}

func cloneFloat64Ptr(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneInt64Ptr(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := value.UTC()
	return &copy
}

func matchesAccountQuery(item ProjectionAccount, query string) bool {
	trimmedQuery := strings.ToLower(strings.TrimSpace(query))
	if trimmedQuery == "" {
		return true
	}
	fields := []string{
		item.AccountID,
		item.ExternalRef,
		item.Label,
		item.GroupName,
		item.UpstreamStatus,
	}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(strings.TrimSpace(field)), trimmedQuery) {
			return true
		}
	}
	return false
}

func (h *Handler) writeError(c *gin.Context, err error) {
	httpStatus, payload := MapError(err)
	c.JSON(httpStatus, payload)
}

func decodeOptionalJSON(c *gin.Context, target any) error {
	if c == nil || c.Request == nil || c.Request.Body == nil {
		return nil
	}
	err := c.ShouldBindJSON(target)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func badRequestError(message string) error {
	return NewCodedError(http.StatusBadRequest, CodeBadRequest, strings.TrimSpace(message), false)
}

func normalizeImportContents(contents []string, content string) []string {
	normalized := make([]string, 0, len(contents)+1)
	for i := range contents {
		trimmed := strings.TrimSpace(contents[i])
		if trimmed != "" {
			normalized = append(normalized, trimmed)
		}
	}
	if trimmed := strings.TrimSpace(content); trimmed != "" {
		normalized = append(normalized, trimmed)
	}
	return normalized
}

func importActionErrorsFromRPC(errors []RPCAccountImportError) []ImportActionErrorData {
	if len(errors) == 0 {
		return []ImportActionErrorData{}
	}
	data := make([]ImportActionErrorData, 0, len(errors))
	for i := range errors {
		data = append(data, ImportActionErrorData{
			Index:   errors[i].Index,
			Message: strings.TrimSpace(errors[i].Message),
		})
	}
	return data
}

func normalizeLoginFlowStatus(raw string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch normalized {
	case "pending", "processing", "in_progress", "in-progress", "running", "started", "waiting":
		return LoginFlowStatusInProgress, false
	case "success", "succeeded", "complete", "completed", "done", "ok":
		return LoginFlowStatusSuccess, true
	case "failed", "failure", "error":
		return LoginFlowStatusFailed, true
	case "cancelled", "canceled", "aborted":
		return LoginFlowStatusCancelled, true
	case "timeout", "timed_out", "timed-out", "expired":
		return LoginFlowStatusTimedOut, true
	default:
		return LoginFlowStatusUnknown, false
	}
}

func unixSecondsToTimePointer(value *int64) *time.Time {
	if value == nil || *value <= 0 {
		return nil
	}
	timestamp := time.Unix(*value, 0).UTC()
	return &timestamp
}

func normalizeStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	copy := trimmed
	return &copy
}

func stringPointer(value string) *string {
	return normalizeStringPointer(&value)
}

func parseRelayEnabledFromRequest(request relayStatePatchRequest) (bool, error) {
	if request.RelayEnabled != nil {
		return *request.RelayEnabled, nil
	}
	return parseRelayEnabledState(request.State)
}

func parseRelayEnabledState(state any) (bool, error) {
	switch value := state.(type) {
	case bool:
		return value, nil
	case float64:
		if value == 1 {
			return true, nil
		}
		if value == 0 {
			return false, nil
		}
	case string:
		normalized := strings.ToLower(strings.TrimSpace(value))
		switch normalized {
		case "enabled", "enable", "on", "true", "1", "active":
			return true, nil
		case "disabled", "disable", "off", "false", "0", "inactive":
			return false, nil
		}
	}
	return false, errors.New("state must be boolean or enabled/disabled")
}
