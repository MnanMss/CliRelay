package codexmanager

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type ProjectionRepository struct {
	projections *ProjectionStore
	overlays    *OverlayStore
}

func NewProjectionRepository(stateDir string) (*ProjectionRepository, error) {
	dir := strings.TrimSpace(stateDir)
	if dir == "" {
		return nil, fmt.Errorf("projection state directory is required")
	}
	projectionPath := filepath.Join(dir, projectionIndexFileName)
	overlayPath := filepath.Join(dir, overlayStateFileName)
	projections, err := NewProjectionStore(projectionPath)
	if err != nil {
		return nil, err
	}
	overlays, err := NewOverlayStore(overlayPath)
	if err != nil {
		return nil, err
	}
	return NewProjectionRepositoryWithStores(projections, overlays)
}

func NewProjectionRepositoryWithStores(projections *ProjectionStore, overlays *OverlayStore) (*ProjectionRepository, error) {
	if projections == nil {
		return nil, fmt.Errorf("projection store is required")
	}
	if overlays == nil {
		return nil, fmt.Errorf("overlay store is required")
	}
	return &ProjectionRepository{projections: projections, overlays: overlays}, nil
}

func (r *ProjectionRepository) List() []ProjectionAccount {
	if r == nil || r.projections == nil {
		return nil
	}
	return r.projections.List()
}

func (r *ProjectionRepository) Get(projectionID string) (ProjectionAccount, bool) {
	if r == nil || r.projections == nil {
		return ProjectionAccount{}, false
	}
	return r.projections.Get(projectionID)
}

func (r *ProjectionRepository) ApplySync(next []ProjectionAccount, syncedAt time.Time) (ProjectionSyncSummary, error) {
	if r == nil || r.projections == nil || r.overlays == nil {
		return ProjectionSyncSummary{}, fmt.Errorf("projection repository is not initialized")
	}
	resolved := make([]ProjectionAccount, 0, len(next))
	for i := range next {
		item := next[i]
		if strings.TrimSpace(item.ProjectionID) == "" {
			item.ProjectionID = projectionIDFromAccountID(item.AccountID)
		}
		if strings.TrimSpace(item.ProjectionID) == "" {
			continue
		}
		if override, ok := r.overlays.GetRelayEnabled(item.ProjectionID); ok {
			item.RelayEnabled = override
		} else {
			item.RelayEnabled = true
		}
		resolved = append(resolved, item)
	}
	return r.projections.ApplySync(resolved, syncedAt)
}

func (r *ProjectionRepository) SetRelayEnabled(projectionID string, relayEnabled bool, updatedAt time.Time) (ProjectionAccount, error) {
	if r == nil || r.projections == nil || r.overlays == nil {
		return ProjectionAccount{}, fmt.Errorf("projection repository is not initialized")
	}
	id := strings.TrimSpace(projectionID)
	if id == "" {
		return ProjectionAccount{}, ErrInvalidAccountID
	}
	if _, exists := r.projections.Get(id); !exists {
		return ProjectionAccount{}, ErrAccountNotFound
	}
	now := updatedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if err := r.overlays.SetRelayEnabled(id, relayEnabled, now); err != nil {
		return ProjectionAccount{}, err
	}
	return r.projections.SetRelayEnabled(id, relayEnabled, now)
}

func ProjectionIDForAccountID(accountID string) (string, error) {
	id := strings.TrimSpace(accountID)
	if id == "" {
		return "", ErrInvalidAccountID
	}
	return projectionIDFromAccountID(id), nil
}

func projectionIDFromAccountID(accountID string) string {
	id := strings.TrimSpace(accountID)
	if id == "" {
		return ""
	}
	if strings.HasPrefix(id, ProjectionIDPrefix) {
		return id
	}
	return ProjectionIDPrefix + id
}
