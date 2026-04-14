package util

import (
	"fmt"
	"io"
	"strings"
)

const (
	defaultProviderHTTPResponseLimit = 1 << 20 // 1 MiB
)

func ProviderHTTPResponseLimit(provider string) int64 {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "codex-device", "codex-oauth", "claude-oauth", "gemini-oauth", "qwen-oauth", "iflow-oauth", "kimi-oauth", "antigravity-oauth":
		return defaultProviderHTTPResponseLimit
	default:
		return defaultProviderHTTPResponseLimit
	}
}

func ReadHTTPResponseBody(provider string, r io.Reader) ([]byte, error) {
	limit := ProviderHTTPResponseLimit(provider)
	if r == nil {
		return nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return data, err
	}
	if int64(len(data)) > limit {
		return data[:limit], fmt.Errorf("%s response body exceeds %d byte read limit", providerLabel(provider), limit)
	}
	return data, nil
}

func providerLabel(provider string) string {
	if trimmed := strings.TrimSpace(provider); trimmed != "" {
		return trimmed
	}
	return "provider"
}
