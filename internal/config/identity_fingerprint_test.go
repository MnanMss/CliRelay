package config

import "testing"

func TestDefaultCodexIdentityFingerprintUsesCurrentVersionAndDynamicSessions(t *testing.T) {
	t.Parallel()

	got := DefaultCodexIdentityFingerprint()

	if got.Enabled {
		t.Fatalf("Enabled = true, want false by default")
	}
	if got.Version != "0.120.0" {
		t.Fatalf("Version = %q, want 0.120.0", got.Version)
	}
	if got.UserAgent != "codex_cli_rs/0.120.0 (Mac OS 26.0.1; arm64) Apple_Terminal/464" {
		t.Fatalf("UserAgent = %q, want current Codex user agent", got.UserAgent)
	}
	if got.SessionMode != "per-request" {
		t.Fatalf("SessionMode = %q, want per-request", got.SessionMode)
	}
}

func TestNormalizeCodexIdentityFingerprintAppliesCurrentDefaults(t *testing.T) {
	t.Parallel()

	got := NormalizeCodexIdentityFingerprint(CodexIdentityFingerprintConfig{})

	if got.Version != "0.120.0" {
		t.Fatalf("Version = %q, want 0.120.0", got.Version)
	}
	if got.UserAgent != "codex_cli_rs/0.120.0 (Mac OS 26.0.1; arm64) Apple_Terminal/464" {
		t.Fatalf("UserAgent = %q, want current Codex user agent", got.UserAgent)
	}
	if got.SessionMode != "per-request" {
		t.Fatalf("SessionMode = %q, want per-request", got.SessionMode)
	}
}
