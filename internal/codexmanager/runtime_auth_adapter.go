package codexmanager

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func SyncImportedCredentials(stateDir string, contents []string, updatedAt time.Time) (int, error) {
	store, err := NewCredentialStoreForStateDir(stateDir)
	if err != nil {
		return 0, err
	}
	records := ParseCredentialRecords(contents)
	if len(records) == 0 {
		return 0, nil
	}
	return store.UpsertBatch(records, updatedAt)
}

func RemoveStoredCredential(stateDir, accountID string, updatedAt time.Time) error {
	store, err := NewCredentialStoreForStateDir(stateDir)
	if err != nil {
		return err
	}
	return store.Delete(accountID, updatedAt)
}

func LoadProjectedRuntimeCodexAuths(stateDir string) ([]*coreauth.Auth, error) {
	repo, err := NewProjectionRepository(stateDir)
	if err != nil {
		return nil, err
	}
	store, err := NewCredentialStoreForStateDir(stateDir)
	if err != nil {
		return nil, err
	}
	return BuildProjectedRuntimeCodexAuths(repo.List(), store.List()), nil
}

func BuildProjectedRuntimeCodexAuths(projected []ProjectionAccount, credentials []CredentialRecord) []*coreauth.Auth {
	if len(projected) == 0 || len(credentials) == 0 {
		return nil
	}
	credentialByAccount := make(map[string]CredentialRecord, len(credentials))
	for i := range credentials {
		normalized := normalizeCredentialRecord(credentials[i], time.Time{})
		if normalized.AccountID == "" || !credentialHasExecutableSecret(normalized) {
			continue
		}
		credentialByAccount[normalized.AccountID] = normalized
	}
	if len(credentialByAccount) == 0 {
		return nil
	}

	orderedProjected := append([]ProjectionAccount(nil), projected...)
	for i := range orderedProjected {
		orderedProjected[i] = normalizeProjectionAccount(orderedProjected[i], time.Time{})
	}
	sort.SliceStable(orderedProjected, func(i, j int) bool {
		if orderedProjected[i].UpstreamSort != orderedProjected[j].UpstreamSort {
			return orderedProjected[i].UpstreamSort < orderedProjected[j].UpstreamSort
		}
		return strings.ToLower(orderedProjected[i].AccountID) < strings.ToLower(orderedProjected[j].AccountID)
	})

	runtimeAuths := make([]*coreauth.Auth, 0, len(orderedProjected))
	for i := range orderedProjected {
		projection := orderedProjected[i]
		if projection.Tombstone || !projection.RelayEnabled {
			continue
		}
		accountID := normalizeCredentialAccountID(projection.AccountID)
		if accountID == "" {
			accountID = normalizeCredentialAccountID(projection.ProjectionID)
		}
		if accountID == "" {
			continue
		}
		credential, ok := credentialByAccount[accountID]
		if !ok || !credentialHasExecutableSecret(credential) {
			continue
		}
		auth := runtimeAuthFromProjectionCredential(projection, credential)
		if auth == nil {
			continue
		}
		runtimeAuths = append(runtimeAuths, auth)
	}
	return runtimeAuths
}

func runtimeAuthFromProjectionCredential(projection ProjectionAccount, credential CredentialRecord) *coreauth.Auth {
	accountID := normalizeCredentialAccountID(projection.AccountID)
	if accountID == "" {
		accountID = normalizeCredentialAccountID(credential.AccountID)
	}
	if accountID == "" {
		return nil
	}
	id := projectionIDFromAccountID(accountID)
	if id == "" {
		return nil
	}
	now := projection.LastSyncedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	label := strings.TrimSpace(projection.Label)
	if label == "" {
		label = accountID
	}
	attributes := map[string]string{
		"runtime_only": "true",
		"source":       RuntimeSourceCodexManager,
		"priority":     strconv.Itoa(codexManagerRuntimePriority),
	}
	if strings.TrimSpace(credential.BaseURL) != "" {
		attributes["base_url"] = strings.TrimSpace(credential.BaseURL)
	}
	if strings.TrimSpace(credential.APIKey) != "" {
		attributes["api_key"] = strings.TrimSpace(credential.APIKey)
		attributes["auth_kind"] = "api_key"
	} else {
		attributes["auth_kind"] = "oauth"
	}
	for headerName, headerValue := range normalizeCredentialHeaders(credential.Headers) {
		attributes["header:"+headerName] = headerValue
	}

	metadata := map[string]any{
		"type":          "codex",
		"source":        RuntimeSourceCodexManager,
		"projection_id": strings.TrimSpace(projection.ProjectionID),
		"external_ref":  strings.TrimSpace(projection.ExternalRef),
	}
	chatGPTAccountID := strings.TrimSpace(credential.ChatGPTAccountID)
	if chatGPTAccountID == "" {
		chatGPTAccountID = strings.TrimSpace(credential.AccountRef)
	}
	if chatGPTAccountID == "" {
		chatGPTAccountID = accountID
	}
	metadata["account_id"] = chatGPTAccountID
	if email := strings.TrimSpace(credential.Email); email != "" {
		metadata["email"] = email
	}
	if workspaceID := strings.TrimSpace(credential.WorkspaceID); workspaceID != "" {
		metadata["workspace_id"] = workspaceID
	}
	if accessToken := strings.TrimSpace(credential.AccessToken); accessToken != "" {
		metadata["access_token"] = accessToken
	}
	if refreshToken := strings.TrimSpace(credential.RefreshToken); refreshToken != "" {
		metadata["refresh_token"] = refreshToken
	}
	if idToken := strings.TrimSpace(credential.IDToken); idToken != "" {
		metadata["id_token"] = idToken
	}
	if lastRefresh := strings.TrimSpace(credential.LastRefresh); lastRefresh != "" {
		metadata["last_refresh"] = lastRefresh
	}
	proxyURL := strings.TrimSpace(credential.ProxyURL)
	prefix := strings.Trim(strings.TrimSpace(credential.Prefix), "/")

	return &coreauth.Auth{
		ID:         id,
		Provider:   "codex",
		Label:      label,
		Prefix:     prefix,
		Status:     coreauth.StatusActive,
		ProxyURL:   proxyURL,
		Attributes: attributes,
		Metadata:   metadata,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

func ValidateProjectedRuntimeAuths(stateDir string) error {
	auths, err := LoadProjectedRuntimeCodexAuths(stateDir)
	if err != nil {
		return err
	}
	for i := range auths {
		auth := auths[i]
		if auth == nil {
			continue
		}
		if strings.TrimSpace(auth.Provider) != "codex" {
			return fmt.Errorf("runtime auth %q provider must be codex", auth.ID)
		}
		if auth.Attributes == nil || strings.ToLower(strings.TrimSpace(auth.Attributes["runtime_only"])) != "true" {
			return fmt.Errorf("runtime auth %q must set runtime_only=true", auth.ID)
		}
	}
	return nil
}
