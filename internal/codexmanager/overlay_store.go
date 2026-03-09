package codexmanager

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type relayOverlayState struct {
	RelayEnabled bool      `json:"relayEnabled"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type overlayStoreState struct {
	SchemaVersion int                          `json:"schemaVersion"`
	UpdatedAt     time.Time                    `json:"updatedAt"`
	Relay         map[string]relayOverlayState `json:"relay"`
}

type OverlayStore struct {
	path  string
	mu    sync.RWMutex
	state overlayStoreState
}

func NewOverlayStore(path string) (*OverlayStore, error) {
	store := &OverlayStore{path: strings.TrimSpace(path)}
	if store.path == "" {
		return nil, fmt.Errorf("overlay state file path is required")
	}
	store.state = overlayStoreState{
		SchemaVersion: overlayStoreSchemaV1,
		Relay:         make(map[string]relayOverlayState),
	}
	if exists, err := readJSONFile(store.path, &store.state); err != nil {
		return nil, err
	} else if exists {
		store.normalizeLoadedState()
	}
	return store, nil
}

func (s *OverlayStore) GetRelayEnabled(projectionID string) (bool, bool) {
	if s == nil {
		return false, false
	}
	id := strings.TrimSpace(projectionID)
	if id == "" {
		return false, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.state.Relay[id]
	if !ok {
		return false, false
	}
	return entry.RelayEnabled, true
}

func (s *OverlayStore) SetRelayEnabled(projectionID string, relayEnabled bool, updatedAt time.Time) error {
	if s == nil {
		return fmt.Errorf("overlay store is nil")
	}
	id := strings.TrimSpace(projectionID)
	if id == "" {
		return ErrInvalidAccountID
	}
	now := updatedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state.Relay == nil {
		s.state.Relay = make(map[string]relayOverlayState)
	}
	s.state.Relay[id] = relayOverlayState{RelayEnabled: relayEnabled, UpdatedAt: now}
	s.state.UpdatedAt = now
	if s.state.SchemaVersion == 0 {
		s.state.SchemaVersion = overlayStoreSchemaV1
	}
	return writeJSONFile(s.path, s.state)
}

func (s *OverlayStore) ListProjectionIDs() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.state.Relay) == 0 {
		return nil
	}
	ids := make([]string, 0, len(s.state.Relay))
	for id := range s.state.Relay {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (s *OverlayStore) normalizeLoadedState() {
	if s.state.SchemaVersion == 0 {
		s.state.SchemaVersion = overlayStoreSchemaV1
	}
	if s.state.Relay == nil {
		s.state.Relay = make(map[string]relayOverlayState)
		return
	}
	normalized := make(map[string]relayOverlayState, len(s.state.Relay))
	for id, entry := range s.state.Relay {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		normalized[trimmed] = relayOverlayState{
			RelayEnabled: entry.RelayEnabled,
			UpdatedAt:    entry.UpdatedAt.UTC(),
		}
	}
	s.state.Relay = normalized
}
