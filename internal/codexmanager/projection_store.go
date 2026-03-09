package codexmanager

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type projectionStoreState struct {
	SchemaVersion int                          `json:"schemaVersion"`
	UpdatedAt     time.Time                    `json:"updatedAt"`
	Accounts      map[string]ProjectionAccount `json:"accounts"`
}

type ProjectionStore struct {
	path  string
	mu    sync.RWMutex
	state projectionStoreState
}

func NewProjectionStore(path string) (*ProjectionStore, error) {
	store := &ProjectionStore{path: strings.TrimSpace(path)}
	if store.path == "" {
		return nil, fmt.Errorf("projection state file path is required")
	}
	store.state = projectionStoreState{
		SchemaVersion: projectionStoreSchemaV1,
		Accounts:      make(map[string]ProjectionAccount),
	}
	if exists, err := readJSONFile(store.path, &store.state); err != nil {
		return nil, err
	} else if exists {
		store.normalizeLoadedState()
	}
	return store, nil
}

func (s *ProjectionStore) List() []ProjectionAccount {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.state.Accounts) == 0 {
		return nil
	}
	ids := make([]string, 0, len(s.state.Accounts))
	for id := range s.state.Accounts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]ProjectionAccount, 0, len(ids))
	for _, id := range ids {
		out = append(out, cloneProjectionAccount(s.state.Accounts[id]))
	}
	return out
}

func (s *ProjectionStore) Get(projectionID string) (ProjectionAccount, bool) {
	if s == nil {
		return ProjectionAccount{}, false
	}
	id := strings.TrimSpace(projectionID)
	if id == "" {
		return ProjectionAccount{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.state.Accounts[id]
	if !ok {
		return ProjectionAccount{}, false
	}
	return cloneProjectionAccount(item), true
}

func (s *ProjectionStore) ApplySync(next []ProjectionAccount, syncedAt time.Time) (ProjectionSyncSummary, error) {
	if s == nil {
		return ProjectionSyncSummary{}, fmt.Errorf("projection store is nil")
	}
	now := syncedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	summary := ProjectionSyncSummary{
		SyncedAt:           now,
		UpstreamAccountCnt: len(next),
	}

	upstreamByID := make(map[string]ProjectionAccount, len(next))
	for i := range next {
		candidate := normalizeProjectionAccount(next[i], now)
		if candidate.ProjectionID == "" {
			continue
		}
		upstreamByID[candidate.ProjectionID] = candidate
	}

	for id, previous := range s.state.Accounts {
		if _, found := upstreamByID[id]; found {
			continue
		}
		tombstone := previous
		tombstone.LastSyncedAt = now
		tombstone.SchemaVersion = projectionRecordSchemaV1
		if !tombstone.Tombstone {
			tombstone.Tombstone = true
			if strings.TrimSpace(tombstone.Health.Reason) == "" {
				tombstone.Health.Reason = "missing_from_source"
			}
			summary.Tombstoned++
		}
		upstreamByID[id] = tombstone
	}

	for id, current := range upstreamByID {
		previous, existed := s.state.Accounts[id]
		if !existed {
			if !current.Tombstone {
				summary.Added++
			}
			continue
		}
		if !projectionEqualsIgnoringSyncTimestamp(previous, current) {
			if !(current.Tombstone && !previous.Tombstone) {
				summary.Updated++
			}
		}
	}

	s.state.SchemaVersion = projectionStoreSchemaV1
	s.state.UpdatedAt = now
	s.state.Accounts = upstreamByID
	summary.TotalAfter = len(s.state.Accounts)

	if err := s.saveLocked(); err != nil {
		return summary, err
	}
	return summary, nil
}

func (s *ProjectionStore) SetRelayEnabled(projectionID string, enabled bool, updatedAt time.Time) (ProjectionAccount, error) {
	if s == nil {
		return ProjectionAccount{}, fmt.Errorf("projection store is nil")
	}
	id := strings.TrimSpace(projectionID)
	if id == "" {
		return ProjectionAccount{}, ErrInvalidAccountID
	}
	now := updatedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	item, ok := s.state.Accounts[id]
	if !ok {
		return ProjectionAccount{}, ErrAccountNotFound
	}
	item.RelayEnabled = enabled
	item.LastSyncedAt = now
	item.SchemaVersion = projectionRecordSchemaV1
	s.state.Accounts[id] = item
	s.state.UpdatedAt = now

	if err := s.saveLocked(); err != nil {
		return ProjectionAccount{}, err
	}
	return cloneProjectionAccount(item), nil
}

func (s *ProjectionStore) saveLocked() error {
	if s.state.Accounts == nil {
		s.state.Accounts = make(map[string]ProjectionAccount)
	}
	if s.state.SchemaVersion == 0 {
		s.state.SchemaVersion = projectionStoreSchemaV1
	}
	return writeJSONFile(s.path, s.state)
}

func (s *ProjectionStore) normalizeLoadedState() {
	if s.state.SchemaVersion == 0 {
		s.state.SchemaVersion = projectionStoreSchemaV1
	}
	if s.state.Accounts == nil {
		s.state.Accounts = make(map[string]ProjectionAccount)
		return
	}
	normalized := make(map[string]ProjectionAccount, len(s.state.Accounts))
	for id, item := range s.state.Accounts {
		candidate := normalizeProjectionAccount(item, s.state.UpdatedAt)
		if candidate.ProjectionID == "" {
			candidate.ProjectionID = strings.TrimSpace(id)
		}
		if candidate.ProjectionID == "" {
			continue
		}
		normalized[candidate.ProjectionID] = candidate
	}
	s.state.Accounts = normalized
}

func normalizeProjectionAccount(item ProjectionAccount, fallbackSyncedAt time.Time) ProjectionAccount {
	normalized := item
	normalized.ProjectionID = strings.TrimSpace(normalized.ProjectionID)
	normalized.AccountID = strings.TrimSpace(normalized.AccountID)
	normalized.ExternalRef = strings.TrimSpace(normalized.ExternalRef)
	normalized.Label = strings.TrimSpace(normalized.Label)
	normalized.GroupName = strings.TrimSpace(normalized.GroupName)
	normalized.Source = strings.TrimSpace(normalized.Source)
	normalized.UpstreamStatus = strings.TrimSpace(normalized.UpstreamStatus)
	normalized.UsageSummary = normalizeProjectionUsageSummary(normalized.UsageSummary)
	normalized.Health.AvailabilityStatus = strings.TrimSpace(normalized.Health.AvailabilityStatus)
	normalized.Health.Reason = strings.TrimSpace(normalized.Health.Reason)
	if normalized.ProjectionID == "" && normalized.AccountID != "" {
		normalized.ProjectionID = projectionIDFromAccountID(normalized.AccountID)
	}
	if normalized.AccountID == "" && strings.HasPrefix(normalized.ProjectionID, ProjectionIDPrefix) {
		normalized.AccountID = strings.TrimPrefix(normalized.ProjectionID, ProjectionIDPrefix)
	}
	if normalized.ExternalRef == "" {
		normalized.ExternalRef = normalized.AccountID
	}
	if normalized.Source == "" {
		normalized.Source = ProjectionSourceCodexMGR
	}
	if normalized.LastSyncedAt.IsZero() {
		normalized.LastSyncedAt = fallbackSyncedAt.UTC()
	}
	normalized.SchemaVersion = projectionRecordSchemaV1
	if normalized.Health.BackoffLevel < 0 {
		normalized.Health.BackoffLevel = 0
	}
	if normalized.Health.BackoffUntil != nil {
		ts := normalized.Health.BackoffUntil.UTC()
		normalized.Health.BackoffUntil = &ts
	}
	return normalized
}

func projectionEqualsIgnoringSyncTimestamp(left, right ProjectionAccount) bool {
	if left.ProjectionID != right.ProjectionID {
		return false
	}
	if left.AccountID != right.AccountID {
		return false
	}
	if left.ExternalRef != right.ExternalRef {
		return false
	}
	if left.Label != right.Label {
		return false
	}
	if left.GroupName != right.GroupName {
		return false
	}
	if left.RelayEnabled != right.RelayEnabled {
		return false
	}
	if left.Source != right.Source {
		return false
	}
	if left.VersionHash != right.VersionHash {
		return false
	}
	if left.UpstreamSort != right.UpstreamSort {
		return false
	}
	if left.UpstreamStatus != right.UpstreamStatus {
		return false
	}
	if left.UsageSummary.AvailabilityStatus != right.UsageSummary.AvailabilityStatus {
		return false
	}
	if !float64PtrEqual(left.UsageSummary.UsedPercent, right.UsageSummary.UsedPercent) {
		return false
	}
	if !int64PtrEqual(left.UsageSummary.WindowMinutes, right.UsageSummary.WindowMinutes) {
		return false
	}
	if !timestampsPtrEqual(left.UsageSummary.CapturedAt, right.UsageSummary.CapturedAt) {
		return false
	}
	if left.Tombstone != right.Tombstone {
		return false
	}
	if left.SchemaVersion != right.SchemaVersion {
		return false
	}
	if left.Health.AvailabilityStatus != right.Health.AvailabilityStatus {
		return false
	}
	if left.Health.BackoffLevel != right.Health.BackoffLevel {
		return false
	}
	if left.Health.Reason != right.Health.Reason {
		return false
	}
	if !timestampsPtrEqual(left.Health.BackoffUntil, right.Health.BackoffUntil) {
		return false
	}
	return true
}

func timestampsPtrEqual(left, right *time.Time) bool {
	if left == nil && right == nil {
		return true
	}
	if left == nil || right == nil {
		return false
	}
	return left.UTC().Equal(right.UTC())
}

func float64PtrEqual(left, right *float64) bool {
	if left == nil && right == nil {
		return true
	}
	if left == nil || right == nil {
		return false
	}
	return *left == *right
}

func int64PtrEqual(left, right *int64) bool {
	if left == nil && right == nil {
		return true
	}
	if left == nil || right == nil {
		return false
	}
	return *left == *right
}

func normalizeProjectionUsageSummary(summary ProjectionUsageSummary) ProjectionUsageSummary {
	normalized := summary
	normalized.AvailabilityStatus = strings.TrimSpace(normalized.AvailabilityStatus)
	if normalized.UsedPercent != nil {
		usedPercent := *normalized.UsedPercent
		normalized.UsedPercent = &usedPercent
	}
	if normalized.WindowMinutes != nil {
		windowMinutes := *normalized.WindowMinutes
		normalized.WindowMinutes = &windowMinutes
	}
	if normalized.CapturedAt != nil {
		if normalized.CapturedAt.IsZero() {
			normalized.CapturedAt = nil
		} else {
			capturedAt := normalized.CapturedAt.UTC()
			normalized.CapturedAt = &capturedAt
		}
	}
	return normalized
}

func cloneProjectionAccount(item ProjectionAccount) ProjectionAccount {
	clone := item
	clone.UsageSummary = normalizeProjectionUsageSummary(item.UsageSummary)
	if item.Health.BackoffUntil != nil {
		ts := item.Health.BackoffUntil.UTC()
		clone.Health.BackoffUntil = &ts
	}
	return clone
}
