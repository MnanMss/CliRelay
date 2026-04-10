package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	gin "github.com/gin-gonic/gin"
)

func TestAuthMiddleware_ManagerNilFailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(AuthMiddleware(nil))
	r.GET("/v1/models", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusInternalServerError, rr.Body.String())
	}
}
