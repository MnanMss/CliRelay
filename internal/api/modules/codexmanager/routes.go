package codexmanager

import (
	"github.com/gin-gonic/gin"
	core "github.com/router-for-me/CLIProxyAPI/v6/internal/codexmanager"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

type Module struct {
	handler *core.Handler
	enabled bool
}

func New(cfg *config.Config) *Module {
	enabled := cfg != nil && cfg.CodexManager.Enabled
	if enabled {
		core.StartProjectionSyncOnceAsync(cfg)
	}
	return &Module{handler: core.NewHandler(cfg), enabled: enabled}
}

func (m *Module) RegisterManagementRoutes(group *gin.RouterGroup) {
	if m == nil || group == nil || m.handler == nil || !m.enabled {
		return
	}

	routes := group.Group("/codex-manager")
	{
		routes.GET("/accounts", m.handler.ListAccounts)
		routes.GET("/usage", m.handler.ListUsage)
		routes.GET("/accounts/:accountId", m.handler.GetAccount)
		routes.GET("/accounts/:accountId/usage", m.handler.GetAccountUsage)
		routes.DELETE("/accounts/:accountId", m.handler.DeleteAccount)
		routes.PATCH("/accounts/:accountId/relay-state", m.handler.PatchRelayState)
		routes.POST("/accounts/:accountId/usage/refresh", m.handler.RefreshAccountUsage)
		routes.POST("/usage/refresh-batch", m.handler.RefreshUsageBatch)

		routes.POST("/import", m.handler.ImportAccounts)

		routes.POST("/login/start", m.handler.StartLogin)
		routes.GET("/login/status/:loginId", m.handler.GetLoginStatus)
		routes.POST("/login/complete", m.handler.CompleteLogin)
	}
}
