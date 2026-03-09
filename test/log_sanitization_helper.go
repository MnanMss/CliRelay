package test

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

// SensitiveFieldMasks defines the sensitive fields that should be masked in logs
var SensitiveFieldMasks = []string{
	"access_token",
	"refresh_token",
	"id_token",
	"X-CodexManager-Rpc-Token",
	"Authorization",
}

// FakeTokenValues defines fake token values used in tests (should never appear in logs)
var FakeTokenValues = []string{
	"fake_access_token_abc123_not_real",
	"fake_refresh_token_xyz789_not_real",
	"fake_id_token_def456_not_real",
	"bearer_fake_auth_token_123_not_real",
	"fake_rpc_token_secret_456_not_real",
}

// LogCapture is a thread-safe log capture helper for testing log output
type LogCapture struct {
	mu     sync.RWMutex
	buffer bytes.Buffer
}

// NewLogCapture creates a new log capture instance
func NewLogCapture() *LogCapture {
	return &LogCapture{}
}

// Write implements io.Writer to capture log output
func (lc *LogCapture) Write(p []byte) (n int, err error) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	return lc.buffer.Write(p)
}

// String returns the captured log content
func (lc *LogCapture) String() string {
	lc.mu.RLock()
	defer lc.mu.RUnlock()
	return lc.buffer.String()
}

// Contains checks if the captured logs contain a substring
func (lc *LogCapture) Contains(substr string) bool {
	lc.mu.RLock()
	defer lc.mu.RUnlock()
	return strings.Contains(lc.buffer.String(), substr)
}

// Reset clears the captured log content
func (lc *LogCapture) Reset() {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	lc.buffer.Reset()
}

// AssertNoSensitiveTokens asserts that no sensitive token values appear in logs
func (lc *LogCapture) AssertNoSensitiveTokens(t *testing.T) {
	t.Helper()
	logs := lc.String()

	for _, token := range FakeTokenValues {
		if strings.Contains(logs, token) {
			t.Errorf("Log contains sensitive token value %q - logs must be sanitized", token)
		}
	}
}

// AssertNoSensitiveFieldNames asserts that sensitive field names don't appear with their values
// This is a weaker check that allows field names but not their associated values
func (lc *LogCapture) AssertNoSensitiveFieldNames(t *testing.T) {
	t.Helper()
	logs := lc.String()

	// Check that sensitive field values are masked/redacted
	for _, field := range []string{"access_token", "refresh_token", "id_token"} {
		// Look for patterns like "access_token":"real_value" or access_token=real_value
		// These patterns indicate the value is NOT masked
		if containsUnmaskedValue(logs, field) {
			t.Errorf("Log may contain unmasked value for field %q - logs must be sanitized", field)
		}
	}
}

// containsUnmaskedValue checks if a field appears to have an unmasked value
// This is a heuristic that looks for the field name followed by what looks like a real token
func containsUnmaskedValue(logs, field string) bool {
	// Check for JSON-style: "field":"value" where value is not [REDACTED] or ***
	patterns := []string{
		`"` + field + `":"`,
		field + `=`,
		field + `:\s*`,
	}

	for _, pattern := range patterns {
		idx := strings.Index(logs, pattern)
		if idx == -1 {
			continue
		}

		// Get the value after the pattern
		after := logs[idx+len(pattern):]

		// Find the end of the value (quote, space, or end of string)
		endIdx := strings.IndexAny(after, `"\s,}]`)
		if endIdx == -1 {
			endIdx = len(after)
		}

		if endIdx > 0 {
			value := after[:endIdx]
			// If value is not empty and not a known mask pattern, it's likely unmasked
			if value != "" &&
				!strings.Contains(value, "[REDACTED]") &&
				!strings.Contains(value, "***") &&
				!strings.Contains(value, "[MASKED]") &&
				len(value) > 10 { // Real tokens are typically longer
				return true
			}
		}
	}

	return false
}

// AssertContainsFieldButMasked asserts that a field appears but its value is masked
func (lc *LogCapture) AssertContainsFieldButMasked(t *testing.T, field string) {
	t.Helper()
	logs := lc.String()

	// Field should appear
	if !strings.Contains(logs, field) {
		t.Errorf("Expected field %q to appear in logs", field)
		return
	}

	// But should not have unmasked value
	if containsUnmaskedValue(logs, field) {
		t.Errorf("Field %q appears to have unmasked value in logs", field)
	}
}

// AssertLogContains asserts that logs contain expected content
func (lc *LogCapture) AssertLogContains(t *testing.T, expected string) {
	t.Helper()
	if !lc.Contains(expected) {
		t.Errorf("Expected logs to contain %q, but got:\n%s", expected, lc.String())
	}
}

// AssertLogNotContains asserts that logs do NOT contain forbidden content
func (lc *LogCapture) AssertLogNotContains(t *testing.T, forbidden string) {
	t.Helper()
	if lc.Contains(forbidden) {
		t.Errorf("Logs should NOT contain %q, but got:\n%s", forbidden, lc.String())
	}
}

// SanitizeForLogging returns a sanitized version of a string safe for logging
// This is a helper for production code, shown here for reference
func SanitizeForLogging(input string, sensitiveFields []string) string {
	result := input
	for _, field := range sensitiveFields {
		// Simple masking for demonstration - production code would use proper JSON parsing
		result = maskFieldInJSON(result, field)
	}
	return result
}

// maskFieldInJSON masks a field value in JSON-like content
func maskFieldInJSON(input, field string) string {
	// Look for "field":"value" pattern
	prefix := `"` + field + `":"`
	idx := strings.Index(input, prefix)
	if idx == -1 {
		// Try field=value pattern
		prefix = field + `="`
		idx = strings.Index(input, prefix)
	}
	if idx == -1 {
		return input
	}

	start := idx + len(prefix)
	end := strings.Index(input[start:], `"`)
	if end == -1 {
		return input
	}

	return input[:start] + "[REDACTED]" + input[start+end:]
}
