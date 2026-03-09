package codexmanager

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	log "github.com/sirupsen/logrus"
)

func ProjectionStateDir(cfg *config.Config) string {
	base := ""
	if cfg != nil {
		base = strings.TrimSpace(cfg.AuthDir)
	}
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, projectionStateDirName)
}

func StartProjectionSyncOnceAsync(cfg *config.Config) {
	if cfg == nil || !cfg.CodexManager.Enabled {
		return
	}
	endpoint := strings.TrimSpace(cfg.CodexManager.Endpoint)
	if endpoint == "" {
		return
	}

	repository, err := NewProjectionRepository(ProjectionStateDir(cfg))
	if err != nil {
		log.WithError(err).Warn("codex-manager projection sync skipped: initialize repository failed")
		return
	}

	requestTimeout := codexManagerRPCRequestTimeout(cfg)
	if requestTimeout <= 0 {
		requestTimeout = defaultRPCRequestTimeout
	}
	rpcClient, err := NewRPCClient(RPCClientConfig{
		Endpoint:       endpoint,
		RequestTimeout: requestTimeout,
	})
	if err != nil {
		log.WithError(err).Warn("codex-manager projection sync skipped: initialize rpc client failed")
		return
	}

	worker, err := NewProjectionSyncWorker(NewRPCService(rpcClient), repository)
	if err != nil {
		log.WithError(err).Warn("codex-manager projection sync skipped: initialize worker failed")
		return
	}

	go func() {
		timeout := requestTimeout * 2
		if timeout < 5*time.Second {
			timeout = 5 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		summary, syncErr := worker.Sync(ctx)
		if syncErr != nil {
			log.WithError(syncErr).Warn("codex-manager projection sync failed")
			return
		}

		fields := log.Fields{
			"upstream_accounts": summary.UpstreamAccountCnt,
			"usage_snapshots":   summary.UsageSnapshotCnt,
			"added":             summary.Added,
			"updated":           summary.Updated,
			"tombstoned":        summary.Tombstoned,
			"total_after":       summary.TotalAfter,
		}
		if summary.UsageStale {
			fields["usage_stale"] = true
		}
		log.WithFields(fields).Info("codex-manager projection sync completed")
	}()
}

func codexManagerRPCRequestTimeout(cfg *config.Config) time.Duration {
	if cfg == nil {
		return 0
	}
	if cfg.CodexManager.RequestTimeoutSeconds <= 0 {
		return 0
	}
	return time.Duration(cfg.CodexManager.RequestTimeoutSeconds) * time.Second
}
