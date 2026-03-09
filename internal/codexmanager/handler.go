package codexmanager

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	data, err := h.importAccountsWithFallback(c.Request.Context(), contents, time.Now().UTC())
	if err != nil {
		h.writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, Success(data))
}

func (h *Handler) importAccountsWithFallback(ctx context.Context, contents []string, importedAt time.Time) (ImportActionData, error) {
	if h == nil || h.service == nil {
		log.Warn("codex-manager upstream import endpoint is not configured, falling back to local state import")
		return h.importAccountsLocally(contents, importedAt)
	}

	result, err := h.service.ImportAccounts(ctx, contents)
	if err != nil {
		if !shouldFallbackToLocalImport(err) {
			return ImportActionData{}, err
		}
		log.WithError(err).Warn("codex-manager upstream import unavailable, falling back to local state import")
		return h.importAccountsLocally(contents, importedAt)
	}

	if err := h.syncImportedExecutableCredentials(contents, importedAt); err != nil {
		return ImportActionData{}, err
	}
	if err := h.syncImportedProjectionAccounts(contents, importedAt); err != nil {
		return ImportActionData{}, err
	}

	return ImportActionData{
		Total:   result.Total,
		Created: result.Created,
		Updated: result.Updated,
		Failed:  result.Failed,
		Errors:  importActionErrorsFromRPC(result.Errors),
	}, nil
}

func (h *Handler) importAccountsLocally(contents []string, importedAt time.Time) (ImportActionData, error) {
	records := ParseCredentialRecords(contents)
	if len(records) == 0 {
		return ImportActionData{}, NewCodedError(http.StatusInternalServerError, CodeInternalError, "codex-manager import succeeded but import contents produced no executable local credentials", false)
	}

	store, err := NewCredentialStoreForStateDir(h.stateDir)
	if err != nil {
		return ImportActionData{}, NewCodedError(http.StatusInternalServerError, CodeInternalError, "codex-manager local credential store is not initialized", false)
	}

	data := summarizeLocalImportResult(store, records, importedAt)
	if err := h.syncImportedExecutableCredentials(contents, importedAt); err != nil {
		return ImportActionData{}, err
	}
	if err := h.syncImportedProjectionAccounts(contents, importedAt); err != nil {
		return ImportActionData{}, err
	}

	return data, nil
}

func (h *Handler) ExportAccounts(c *gin.Context) {
	archive, fileName, err := h.buildExportArchive(time.Now().UTC())
	if err != nil {
		h.writeError(c, err)
		return
	}

	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fileName))
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "application/zip", archive)
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

func (h *Handler) DeleteUnavailableFreeAccounts(c *gin.Context) {
	result, err := h.service.DeleteUnavailableFreeAccounts(c.Request.Context())
	if err != nil {
		h.writeError(c, err)
		return
	}

	deletedAccountIDs := normalizeDeletedAccountIDs(result.DeletedAccountIDs)
	cleanupSummary, err := h.cleanupDeletedUnavailableFreeAccounts(deletedAccountIDs, time.Now().UTC())
	if err != nil {
		h.writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, Success(DeleteUnavailableFreeActionData{
		Scanned:                    result.Scanned,
		Deleted:                    result.Deleted,
		SkippedAvailable:           result.SkippedAvailable,
		SkippedNonFree:             result.SkippedNonFree,
		SkippedMissingUsage:        result.SkippedMissingUsage,
		SkippedMissingToken:        result.SkippedMissingToken,
		DeletedAccountIDs:          deletedAccountIDs,
		LocalCredentialsRemoved:    cleanupSummary.CredentialsRemoved,
		LocalProjectionsTombstoned: cleanupSummary.ProjectionsTombstoned,
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

func summarizeLocalImportResult(store *CredentialStore, records []CredentialRecord, importedAt time.Time) ImportActionData {
	now := importedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	var created int64
	var updated int64
	for i := range records {
		normalized := normalizeCredentialRecord(records[i], now)
		if normalized.AccountID == "" || !credentialHasExecutableSecret(normalized) {
			continue
		}
		existing, exists := store.Get(normalized.AccountID)
		if !exists {
			created++
			continue
		}
		if !credentialRecordEquals(existing, normalized) {
			updated++
		}
	}

	return ImportActionData{
		Total:   int64(len(records)),
		Created: created,
		Updated: updated,
		Failed:  0,
		Errors:  []ImportActionErrorData{},
	}
}

func shouldFallbackToLocalImport(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrUpstreamUnavailable) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	var codedErr *CodedError
	if errors.As(err, &codedErr) {
		switch codedErr.Code {
		case CodeUpstreamRejected, CodeUpstreamUnavailable, CodeUpstreamTimeout:
			return true
		}
	}
	return false
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

type deleteUnavailableFreeLocalCleanupSummary struct {
	CredentialsRemoved    int64
	ProjectionsTombstoned int64
}

type exportArchiveAccount struct {
	AccountID   string
	Projection  *ProjectionAccount
	Credential  CredentialRecord
	DisplayName string
}

func (h *Handler) buildExportArchive(exportedAt time.Time) ([]byte, string, error) {
	if h == nil {
		return nil, "", NewCodedError(http.StatusInternalServerError, CodeInternalError, "codex-manager handler is not initialized", false)
	}

	store, err := NewCredentialStoreForStateDir(h.stateDir)
	if err != nil {
		return nil, "", NewCodedError(http.StatusInternalServerError, CodeInternalError, "codex-manager credential store is not initialized", false)
	}
	repository, err := h.currentRepository()
	if err != nil {
		return nil, "", err
	}

	entries := buildExportArchiveAccounts(repository.List(), store.List())
	buffer := bytes.NewBuffer(nil)
	archive := zip.NewWriter(buffer)
	fileNames := make(map[string]int, len(entries))
	for i := range entries {
		entry := entries[i]
		payload := buildExportAccountPayload(entry, exportedAt)
		encoded, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			_ = archive.Close()
			return nil, "", NewCodedError(http.StatusInternalServerError, CodeInternalError, "failed to encode codex-manager export payload", false)
		}
		writer, err := archive.Create(buildExportAccountFileName(entry.DisplayName, entry.AccountID, fileNames))
		if err != nil {
			_ = archive.Close()
			return nil, "", NewCodedError(http.StatusInternalServerError, CodeInternalError, "failed to create codex-manager export archive", false)
		}
		if _, err := writer.Write(encoded); err != nil {
			_ = archive.Close()
			return nil, "", NewCodedError(http.StatusInternalServerError, CodeInternalError, "failed to write codex-manager export archive", false)
		}
	}
	if err := archive.Close(); err != nil {
		return nil, "", NewCodedError(http.StatusInternalServerError, CodeInternalError, "failed to finalize codex-manager export archive", false)
	}

	now := exportedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return buffer.Bytes(), fmt.Sprintf("codex-manager-accounts-%s.zip", now.Format("20060102T150405Z")), nil
}

func buildExportArchiveAccounts(projected []ProjectionAccount, credentials []CredentialRecord) []exportArchiveAccount {
	if len(credentials) == 0 {
		return nil
	}
	projectionByAccount := make(map[string]ProjectionAccount, len(projected))
	for i := range projected {
		projection := normalizeProjectionAccount(projected[i], time.Time{})
		accountID := normalizeCredentialAccountID(projection.AccountID)
		if accountID == "" {
			continue
		}
		projectionByAccount[accountID] = projection
	}

	entries := make([]exportArchiveAccount, 0, len(credentials))
	for i := range credentials {
		credential := normalizeCredentialRecord(credentials[i], time.Time{})
		accountID := normalizeCredentialAccountID(credential.AccountID)
		if accountID == "" || !credentialHasExecutableSecret(credential) {
			continue
		}
		entry := exportArchiveAccount{
			AccountID:   accountID,
			Credential:  credential,
			DisplayName: exportDisplayName(nil, credential),
		}
		if projection, ok := projectionByAccount[accountID]; ok {
			projectionCopy := projection
			entry.Projection = &projectionCopy
			entry.DisplayName = exportDisplayName(&projectionCopy, credential)
		}
		entries = append(entries, entry)
	}

	sort.SliceStable(entries, func(i, j int) bool {
		leftProjection := entries[i].Projection
		rightProjection := entries[j].Projection
		switch {
		case leftProjection != nil && rightProjection != nil && leftProjection.UpstreamSort != rightProjection.UpstreamSort:
			return leftProjection.UpstreamSort < rightProjection.UpstreamSort
		case leftProjection != nil && rightProjection == nil:
			return true
		case leftProjection == nil && rightProjection != nil:
			return false
		default:
			return strings.ToLower(entries[i].AccountID) < strings.ToLower(entries[j].AccountID)
		}
	})
	return entries
}

func buildExportAccountPayload(entry exportArchiveAccount, exportedAt time.Time) ExportAccountPayload {
	credential := normalizeCredentialRecord(entry.Credential, exportedAt)
	var projection *ProjectionAccount
	if entry.Projection != nil {
		projectionCopy := normalizeProjectionAccount(*entry.Projection, exportedAt)
		projection = &projectionCopy
	}
	now := exportedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return ExportAccountPayload{
		Tokens: ExportTokensPayload{
			AccessToken:       credential.AccessToken,
			IDToken:           credential.IDToken,
			RefreshToken:      credential.RefreshToken,
			AccountID:         normalizeCredentialAccountID(credential.AccountID),
			APIKeyAccessToken: credential.APIKey,
			LastRefresh:       credential.LastRefresh,
		},
		Meta: ExportMetaPayload{
			Label:            exportDisplayName(projection, credential),
			Issuer:           exportIssuer(projection),
			GroupName:        exportGroupName(projection),
			Status:           exportStatus(projection),
			WorkspaceID:      stringPointer(credential.WorkspaceID),
			ChatGPTAccountID: exportChatGPTAccountID(credential),
			Email:            stringPointer(credential.Email),
			BaseURL:          stringPointer(credential.BaseURL),
			ProxyURL:         stringPointer(credential.ProxyURL),
			Prefix:           stringPointer(credential.Prefix),
			Headers:          cloneExportHeaders(credential.Headers),
			ExportedAt:       now.Unix(),
		},
	}
}

func exportDisplayName(projection *ProjectionAccount, credential CredentialRecord) string {
	if projection != nil {
		if label := strings.TrimSpace(projection.Label); label != "" {
			return label
		}
	}
	if email := strings.TrimSpace(credential.Email); email != "" {
		return email
	}
	return normalizeCredentialAccountID(credential.AccountID)
}

func exportIssuer(_ *ProjectionAccount) string {
	return "codex"
}

func exportGroupName(projection *ProjectionAccount) *string {
	if projection == nil {
		return nil
	}
	return stringPointer(projection.GroupName)
}

func exportStatus(projection *ProjectionAccount) string {
	if projection != nil {
		if status := strings.TrimSpace(projection.UpstreamStatus); status != "" {
			return status
		}
	}
	return "active"
}

func exportChatGPTAccountID(credential CredentialRecord) *string {
	if value := strings.TrimSpace(credential.ChatGPTAccountID); value != "" {
		return stringPointer(value)
	}
	if value := strings.TrimSpace(credential.AccountRef); value != "" {
		return stringPointer(value)
	}
	return nil
}

func cloneExportHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(headers))
	for key, value := range normalizeCredentialHeaders(headers) {
		cloned[key] = value
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}

func buildExportAccountFileName(displayName, accountID string, counter map[string]int) string {
	labelPart := sanitizeExportFileStem(displayName)
	idPart := sanitizeExportFileStem(accountID)
	stem := idPart
	if labelPart != "" && idPart != "" {
		stem = labelPart + "_" + idPart
	} else if labelPart != "" {
		stem = labelPart
	}
	if stem == "" {
		stem = "account"
	}
	sequence := counter[stem]
	counter[stem] = sequence + 1
	if sequence > 0 {
		stem = fmt.Sprintf("%s_%d", stem, sequence)
	}
	return stem + ".json"
}

func sanitizeExportFileStem(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	var builder strings.Builder
	builder.Grow(len(trimmed))
	for _, ch := range trimmed {
		if builder.Len() >= 96 {
			break
		}
		if ch < 32 || strings.ContainsRune(`<>:"/\\|?*`, ch) {
			builder.WriteByte('_')
			continue
		}
		builder.WriteRune(ch)
	}
	out := strings.TrimSpace(strings.Trim(builder.String(), " ."))
	return out
}

func normalizeDeletedAccountIDs(accountIDs []string) []string {
	if len(accountIDs) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(accountIDs))
	seen := make(map[string]struct{}, len(accountIDs))
	for i := range accountIDs {
		accountID := normalizeCredentialAccountID(accountIDs[i])
		if accountID == "" {
			continue
		}
		if _, exists := seen[accountID]; exists {
			continue
		}
		seen[accountID] = struct{}{}
		normalized = append(normalized, accountID)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func (h *Handler) cleanupDeletedUnavailableFreeAccounts(accountIDs []string, updatedAt time.Time) (deleteUnavailableFreeLocalCleanupSummary, error) {
	if len(accountIDs) == 0 {
		return deleteUnavailableFreeLocalCleanupSummary{}, nil
	}
	store, err := NewCredentialStoreForStateDir(h.stateDir)
	if err != nil {
		return deleteUnavailableFreeLocalCleanupSummary{}, NewCodedError(http.StatusInternalServerError, CodeInternalError, "codex-manager delete unavailable free local credential cleanup failed", false)
	}
	repository, err := h.currentRepository()
	if err != nil {
		return deleteUnavailableFreeLocalCleanupSummary{}, NewCodedError(http.StatusInternalServerError, CodeInternalError, "codex-manager delete unavailable free local projection cleanup failed", false)
	}
	deletedSet := make(map[string]struct{}, len(accountIDs))
	for i := range accountIDs {
		deletedSet[accountIDs[i]] = struct{}{}
	}

	current := repository.List()
	next := make([]ProjectionAccount, 0, len(current))
	for i := range current {
		accountID := normalizeCredentialAccountID(current[i].AccountID)
		if _, deleted := deletedSet[accountID]; deleted {
			continue
		}
		next = append(next, current[i])
	}
	applySummary, err := repository.ApplySync(next, updatedAt)
	if err != nil {
		return deleteUnavailableFreeLocalCleanupSummary{}, NewCodedError(http.StatusInternalServerError, CodeInternalError, "codex-manager delete unavailable free local projection cleanup failed", false)
	}

	var credentialsRemoved int64
	for i := range accountIDs {
		if _, exists := store.Get(accountIDs[i]); exists {
			credentialsRemoved++
		}
		if err := store.Delete(accountIDs[i], updatedAt); err != nil {
			return deleteUnavailableFreeLocalCleanupSummary{}, NewCodedError(http.StatusInternalServerError, CodeInternalError, "codex-manager delete unavailable free local credential cleanup failed", false)
		}
	}

	return deleteUnavailableFreeLocalCleanupSummary{
		CredentialsRemoved:    credentialsRemoved,
		ProjectionsTombstoned: int64(applySummary.Tombstoned),
	}, nil
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
