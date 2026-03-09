package codexmanager

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type CredentialRecord struct {
	AccountID        string            `json:"accountId"`
	AccountRef       string            `json:"accountID,omitempty"`
	ChatGPTAccountID string            `json:"chatgptAccountId,omitempty"`
	WorkspaceID      string            `json:"workspaceId,omitempty"`
	Email            string            `json:"email,omitempty"`
	APIKey           string            `json:"apiKey,omitempty"`
	AccessToken      string            `json:"accessToken,omitempty"`
	RefreshToken     string            `json:"refreshToken,omitempty"`
	IDToken          string            `json:"idToken,omitempty"`
	BaseURL          string            `json:"baseUrl,omitempty"`
	ProxyURL         string            `json:"proxyUrl,omitempty"`
	Prefix           string            `json:"prefix,omitempty"`
	LastRefresh      string            `json:"lastRefresh,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
	UpdatedAt        time.Time         `json:"updatedAt"`
}

type credentialStoreState struct {
	SchemaVersion int                         `json:"schemaVersion"`
	UpdatedAt     time.Time                   `json:"updatedAt"`
	Credentials   map[string]CredentialRecord `json:"credentials"`
}

type CredentialStore struct {
	path  string
	mu    sync.RWMutex
	state credentialStoreState
}

func CredentialStatePath(stateDir string) string {
	trimmed := strings.TrimSpace(stateDir)
	if trimmed == "" {
		return ""
	}
	return filepath.Join(trimmed, credentialStateFileName)
}

func NewCredentialStoreForStateDir(stateDir string) (*CredentialStore, error) {
	path := CredentialStatePath(stateDir)
	if path == "" {
		return nil, fmt.Errorf("credential state directory is required")
	}
	return NewCredentialStore(path)
}

func NewCredentialStore(path string) (*CredentialStore, error) {
	store := &CredentialStore{path: strings.TrimSpace(path)}
	if store.path == "" {
		return nil, fmt.Errorf("credential state file path is required")
	}
	store.state = credentialStoreState{SchemaVersion: credentialStoreSchemaV1, Credentials: make(map[string]CredentialRecord)}
	if err := store.Load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *CredentialStore) Load() error {
	if s == nil {
		return fmt.Errorf("credential store is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state := credentialStoreState{
		SchemaVersion: credentialStoreSchemaV1,
		Credentials:   make(map[string]CredentialRecord),
	}
	if exists, err := readJSONFile(s.path, &state); err != nil {
		return err
	} else if exists {
		s.state = state
		s.normalizeLoadedState()
		return nil
	}
	s.state = state
	return nil
}

func (s *CredentialStore) List() []CredentialRecord {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.state.Credentials) == 0 {
		return nil
	}
	ids := make([]string, 0, len(s.state.Credentials))
	for accountID := range s.state.Credentials {
		ids = append(ids, accountID)
	}
	sort.Strings(ids)
	out := make([]CredentialRecord, 0, len(ids))
	for _, accountID := range ids {
		out = append(out, cloneCredentialRecord(s.state.Credentials[accountID]))
	}
	return out
}

func (s *CredentialStore) Get(accountID string) (CredentialRecord, bool) {
	if s == nil {
		return CredentialRecord{}, false
	}
	id := normalizeCredentialAccountID(accountID)
	if id == "" {
		return CredentialRecord{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.state.Credentials[id]
	if !ok {
		return CredentialRecord{}, false
	}
	return cloneCredentialRecord(record), true
}

func (s *CredentialStore) Upsert(entry CredentialRecord, updatedAt time.Time) (CredentialRecord, error) {
	if s == nil {
		return CredentialRecord{}, fmt.Errorf("credential store is nil")
	}
	normalizedAccountID := normalizeCredentialAccountID(entry.AccountID)
	if normalizedAccountID == "" {
		return CredentialRecord{}, ErrInvalidAccountID
	}
	entry.AccountID = normalizedAccountID
	if !credentialHasExecutableSecret(entry) {
		return CredentialRecord{}, fmt.Errorf("credential record requires api key or access token")
	}
	if _, err := s.UpsertBatch([]CredentialRecord{entry}, updatedAt); err != nil {
		return CredentialRecord{}, err
	}
	record, ok := s.Get(normalizedAccountID)
	if !ok {
		return CredentialRecord{}, fmt.Errorf("credential record %q not found after upsert", normalizedAccountID)
	}
	return record, nil
}

func (s *CredentialStore) UpsertBatch(entries []CredentialRecord, updatedAt time.Time) (int, error) {
	if s == nil {
		return 0, fmt.Errorf("credential store is nil")
	}
	if len(entries) == 0 {
		return 0, nil
	}
	now := updatedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state.Credentials == nil {
		s.state.Credentials = make(map[string]CredentialRecord)
	}
	changed := 0
	for i := range entries {
		normalized := normalizeCredentialRecord(entries[i], now)
		if normalized.AccountID == "" || !credentialHasExecutableSecret(normalized) {
			continue
		}
		previous, exists := s.state.Credentials[normalized.AccountID]
		if exists && credentialRecordEquals(previous, normalized) {
			continue
		}
		s.state.Credentials[normalized.AccountID] = normalized
		changed++
	}
	if changed == 0 {
		return 0, nil
	}
	if s.state.SchemaVersion == 0 {
		s.state.SchemaVersion = credentialStoreSchemaV1
	}
	s.state.UpdatedAt = now
	if err := s.saveLocked(); err != nil {
		return 0, err
	}
	return changed, nil
}

func (s *CredentialStore) Delete(accountID string, updatedAt time.Time) error {
	if s == nil {
		return fmt.Errorf("credential store is nil")
	}
	id := normalizeCredentialAccountID(accountID)
	if id == "" {
		return ErrInvalidAccountID
	}
	now := updatedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.state.Credentials) == 0 {
		return nil
	}
	if _, exists := s.state.Credentials[id]; !exists {
		return nil
	}
	delete(s.state.Credentials, id)
	s.state.UpdatedAt = now
	if s.state.SchemaVersion == 0 {
		s.state.SchemaVersion = credentialStoreSchemaV1
	}
	return s.saveLocked()
}

func (s *CredentialStore) saveLocked() error {
	if s.state.Credentials == nil {
		s.state.Credentials = make(map[string]CredentialRecord)
	}
	if s.state.SchemaVersion == 0 {
		s.state.SchemaVersion = credentialStoreSchemaV1
	}
	return writeJSONFile(s.path, s.state)
}

func (s *CredentialStore) normalizeLoadedState() {
	if s.state.SchemaVersion == 0 {
		s.state.SchemaVersion = credentialStoreSchemaV1
	}
	if s.state.Credentials == nil {
		s.state.Credentials = make(map[string]CredentialRecord)
		return
	}
	normalized := make(map[string]CredentialRecord, len(s.state.Credentials))
	fallback := s.state.UpdatedAt.UTC()
	for accountID, record := range s.state.Credentials {
		candidate := normalizeCredentialRecord(record, fallback)
		if candidate.AccountID == "" {
			candidate.AccountID = normalizeCredentialAccountID(accountID)
		}
		if candidate.AccountID == "" || !credentialHasExecutableSecret(candidate) {
			continue
		}
		normalized[candidate.AccountID] = candidate
	}
	s.state.Credentials = normalized
}

func normalizeCredentialRecord(record CredentialRecord, fallbackUpdatedAt time.Time) CredentialRecord {
	normalized := record
	normalized.AccountID = normalizeCredentialAccountID(normalized.AccountID)
	normalized.AccountRef = strings.TrimSpace(normalized.AccountRef)
	normalized.ChatGPTAccountID = strings.TrimSpace(normalized.ChatGPTAccountID)
	normalized.WorkspaceID = strings.TrimSpace(normalized.WorkspaceID)
	normalized.Email = strings.TrimSpace(normalized.Email)
	normalized.APIKey = strings.TrimSpace(normalized.APIKey)
	normalized.AccessToken = strings.TrimSpace(normalized.AccessToken)
	normalized.RefreshToken = strings.TrimSpace(normalized.RefreshToken)
	normalized.IDToken = strings.TrimSpace(normalized.IDToken)
	normalized.BaseURL = strings.TrimSpace(normalized.BaseURL)
	normalized.ProxyURL = strings.TrimSpace(normalized.ProxyURL)
	normalized.Prefix = strings.Trim(strings.TrimSpace(normalized.Prefix), "/")
	normalized.LastRefresh = strings.TrimSpace(normalized.LastRefresh)
	normalized.Headers = normalizeCredentialHeaders(normalized.Headers)
	if normalized.UpdatedAt.IsZero() {
		normalized.UpdatedAt = fallbackUpdatedAt.UTC()
	}
	if normalized.ChatGPTAccountID == "" {
		normalized.ChatGPTAccountID = normalized.AccountRef
	}
	if normalized.AccountRef == "" {
		normalized.AccountRef = normalized.ChatGPTAccountID
	}
	if normalized.ChatGPTAccountID == "" {
		normalized.ChatGPTAccountID = normalized.AccountID
	}
	if normalized.AccountRef == "" {
		normalized.AccountRef = normalized.AccountID
	}
	return normalized
}

func normalizeCredentialHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	normalized := make(map[string]string, len(headers))
	for headerName, headerValue := range headers {
		name := strings.TrimSpace(headerName)
		value := strings.TrimSpace(headerValue)
		if name == "" || value == "" {
			continue
		}
		normalized[name] = value
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func normalizeCredentialAccountID(accountID string) string {
	trimmed := strings.TrimSpace(accountID)
	if strings.HasPrefix(trimmed, ProjectionIDPrefix) {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, ProjectionIDPrefix))
	}
	return trimmed
}

func credentialHasExecutableSecret(record CredentialRecord) bool {
	return strings.TrimSpace(record.APIKey) != "" || strings.TrimSpace(record.AccessToken) != ""
}

func cloneCredentialRecord(record CredentialRecord) CredentialRecord {
	clone := record
	clone.UpdatedAt = clone.UpdatedAt.UTC()
	if len(record.Headers) > 0 {
		clone.Headers = make(map[string]string, len(record.Headers))
		for key, value := range record.Headers {
			clone.Headers[key] = value
		}
	}
	return clone
}

func credentialRecordEquals(left, right CredentialRecord) bool {
	if left.AccountID != right.AccountID {
		return false
	}
	if left.AccountRef != right.AccountRef {
		return false
	}
	if left.ChatGPTAccountID != right.ChatGPTAccountID {
		return false
	}
	if left.WorkspaceID != right.WorkspaceID {
		return false
	}
	if left.Email != right.Email {
		return false
	}
	if left.APIKey != right.APIKey {
		return false
	}
	if left.AccessToken != right.AccessToken {
		return false
	}
	if left.RefreshToken != right.RefreshToken {
		return false
	}
	if left.IDToken != right.IDToken {
		return false
	}
	if left.BaseURL != right.BaseURL {
		return false
	}
	if left.ProxyURL != right.ProxyURL {
		return false
	}
	if left.Prefix != right.Prefix {
		return false
	}
	if left.LastRefresh != right.LastRefresh {
		return false
	}
	if !credentialHeadersEqual(left.Headers, right.Headers) {
		return false
	}
	return true
}

func credentialHeadersEqual(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, leftValue := range left {
		rightValue, ok := right[key]
		if !ok || rightValue != leftValue {
			return false
		}
	}
	return true
}

func credentialRecordScore(record CredentialRecord) int {
	score := 0
	for _, value := range []string{
		record.AccountID,
		record.AccountRef,
		record.ChatGPTAccountID,
		record.WorkspaceID,
		record.Email,
		record.APIKey,
		record.AccessToken,
		record.RefreshToken,
		record.IDToken,
		record.BaseURL,
		record.ProxyURL,
		record.Prefix,
		record.LastRefresh,
	} {
		if strings.TrimSpace(value) != "" {
			score++
		}
	}
	if len(normalizeCredentialHeaders(record.Headers)) > 0 {
		score++
	}
	return score
}

func ParseCredentialRecords(contents []string) []CredentialRecord {
	if len(contents) == 0 {
		return nil
	}
	byAccount := make(map[string]CredentialRecord)
	for _, rawContent := range contents {
		trimmed := strings.TrimSpace(rawContent)
		if trimmed == "" {
			continue
		}
		var decoded any
		if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
			continue
		}
		candidates := make([]CredentialRecord, 0)
		collectCredentialRecords(decoded, &candidates)
		for i := range candidates {
			normalized := normalizeCredentialRecord(candidates[i], time.Time{})
			if normalized.AccountID == "" || !credentialHasExecutableSecret(normalized) {
				continue
			}
			if existing, exists := byAccount[normalized.AccountID]; !exists || credentialRecordScore(normalized) >= credentialRecordScore(existing) {
				byAccount[normalized.AccountID] = normalized
			}
		}
	}
	if len(byAccount) == 0 {
		return nil
	}
	ids := make([]string, 0, len(byAccount))
	for accountID := range byAccount {
		ids = append(ids, accountID)
	}
	sort.Strings(ids)
	out := make([]CredentialRecord, 0, len(ids))
	for _, accountID := range ids {
		out = append(out, cloneCredentialRecord(byAccount[accountID]))
	}
	return out
}

func collectCredentialRecords(value any, out *[]CredentialRecord) {
	switch typed := value.(type) {
	case []any:
		for i := range typed {
			collectCredentialRecords(typed[i], out)
		}
	case map[string]any:
		if record, ok := credentialRecordFromObject(typed); ok {
			*out = append(*out, record)
		}
		for _, nested := range typed {
			switch nested.(type) {
			case []any, map[string]any:
				collectCredentialRecords(nested, out)
			}
		}
	}
}

func credentialRecordFromObject(payload map[string]any) (CredentialRecord, bool) {
	if len(payload) == 0 {
		return CredentialRecord{}, false
	}
	account := mapFromAny(payload["account"])
	token := mapFromAny(payload["token"])
	tokens := mapFromAny(payload["tokens"])
	tokenData := mapFromAny(payload["tokenData"])
	metadata := mapFromAny(payload["metadata"])
	meta := mapFromAny(payload["meta"])

	scopes := []map[string]any{payload, account, token, tokens, tokenData, metadata, meta}
	accountID := firstNonEmptyStringFromScopes(scopes,
		"accountId",
		"account_id",
		"accountID",
	)
	if accountID == "" {
		accountID = firstNonEmptyStringFromScopes([]map[string]any{account, payload}, "id")
	}
	accountID = normalizeCredentialAccountID(accountID)
	if accountID == "" {
		return CredentialRecord{}, false
	}

	record := CredentialRecord{
		AccountID: accountID,
		AccountRef: firstNonEmptyStringFromScopes(scopes,
			"accountID",
			"chatgptAccountId",
			"chatgpt_account_id",
		),
		ChatGPTAccountID: firstNonEmptyStringFromScopes(scopes,
			"chatgptAccountId",
			"chatgpt_account_id",
		),
		WorkspaceID: firstNonEmptyStringFromScopes(scopes,
			"workspaceId",
			"workspace_id",
		),
		Email: firstNonEmptyStringFromScopes(scopes,
			"email",
		),
		APIKey: firstNonEmptyStringFromScopes(scopes,
			"apiKey",
			"api_key",
			"api_key_access_token",
		),
		AccessToken: firstNonEmptyStringFromScopes(scopes,
			"accessToken",
			"access_token",
		),
		RefreshToken: firstNonEmptyStringFromScopes(scopes,
			"refreshToken",
			"refresh_token",
		),
		IDToken: firstNonEmptyStringFromScopes(scopes,
			"idToken",
			"id_token",
		),
		BaseURL: firstNonEmptyStringFromScopes(scopes,
			"baseUrl",
			"base_url",
		),
		ProxyURL: firstNonEmptyStringFromScopes(scopes,
			"proxyUrl",
			"proxy_url",
		),
		Prefix: firstNonEmptyStringFromScopes(scopes,
			"prefix",
		),
		LastRefresh: firstNonEmptyStringFromScopes(scopes,
			"lastRefresh",
			"last_refresh",
		),
		Headers: headersFromScopes(scopes,
			"headers",
			"customHeaders",
			"custom_headers",
		),
	}

	if !credentialHasExecutableSecret(record) {
		return CredentialRecord{}, false
	}
	return record, true
}

func headersFromScopes(scopes []map[string]any, keys ...string) map[string]string {
	if len(scopes) == 0 || len(keys) == 0 {
		return nil
	}
	headers := make(map[string]string)
	for i := range scopes {
		scope := scopes[i]
		if len(scope) == 0 {
			continue
		}
		for _, key := range keys {
			raw, ok := scope[key]
			if !ok {
				continue
			}
			parsed := toStringMap(raw)
			for headerName, headerValue := range parsed {
				headers[headerName] = headerValue
			}
		}
	}
	if len(headers) == 0 {
		return nil
	}
	return normalizeCredentialHeaders(headers)
}

func firstNonEmptyStringFromScopes(scopes []map[string]any, keys ...string) string {
	if len(scopes) == 0 || len(keys) == 0 {
		return ""
	}
	for i := range scopes {
		scope := scopes[i]
		if len(scope) == 0 {
			continue
		}
		for _, key := range keys {
			if value, ok := scope[key]; ok {
				if normalized := stringValue(value); normalized != "" {
					return normalized
				}
			}
		}
	}
	return ""
}

func mapFromAny(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case map[string]string:
		out := make(map[string]any, len(typed))
		for key, v := range typed {
			out[key] = v
		}
		return out
	default:
		return nil
	}
}

func toStringMap(value any) map[string]string {
	switch typed := value.(type) {
	case map[string]string:
		return normalizeCredentialHeaders(typed)
	case map[string]any:
		out := make(map[string]string, len(typed))
		for key, raw := range typed {
			name := strings.TrimSpace(key)
			headerValue := stringValue(raw)
			if name == "" || headerValue == "" {
				continue
			}
			out[name] = headerValue
		}
		return normalizeCredentialHeaders(out)
	default:
		return nil
	}
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return strings.TrimSpace(typed.String())
	case float64:
		return strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%f", typed), "000000"), "."))
	case float32:
		return strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%f", typed), "000000"), "."))
	case int:
		return fmt.Sprintf("%d", typed)
	case int8:
		return fmt.Sprintf("%d", typed)
	case int16:
		return fmt.Sprintf("%d", typed)
	case int32:
		return fmt.Sprintf("%d", typed)
	case int64:
		return fmt.Sprintf("%d", typed)
	case uint:
		return fmt.Sprintf("%d", typed)
	case uint8:
		return fmt.Sprintf("%d", typed)
	case uint16:
		return fmt.Sprintf("%d", typed)
	case uint32:
		return fmt.Sprintf("%d", typed)
	case uint64:
		return fmt.Sprintf("%d", typed)
	default:
		return ""
	}
}
