package routes

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/bodylog"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBodyLogObservesEveryRegisteredGatewayRouteBeforeAdmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := bodylog.DefaultConfig()
	settings.Enabled = true
	logs, err := bodylog.NewManager(t.TempDir(), nil, settings)
	require.NoError(t, err)
	defer logs.Close()
	router := gin.New()
	registry := bodylog.NewGatewayRoutes()
	router.Use(logs.Gateway(registry, nil))
	observed := 0
	router.Use(func(c *gin.Context) {
		if bodylog.FromContext(c.Request.Context()) != nil {
			observed++
		}
		c.AbortWithStatus(http.StatusUnauthorized)
	})
	router.GET("/health", func(*gin.Context) {})
	before := router.Routes()
	RegisterGatewayRoutes(router, &handler.Handlers{
		Gateway: &handler.GatewayHandler{}, OpenAIGateway: &handler.OpenAIGatewayHandler{},
		AsyncImage: handler.NewAsyncImageHandler(nil, nil),
	}, middleware.APIKeyAuthMiddleware(func(c *gin.Context) { c.Next() }),
		nil, nil, nil, nil, nil, &config.Config{Gateway: config.GatewayConfig{MaxBodySize: 1 << 20, TextMaxBodySize: 1 << 20}})
	registry.Register(before, router.Routes())
	parameter := regexp.MustCompile(`[:*][^/]+`)
	for _, route := range router.Routes() {
		previous := observed
		path := parameter.ReplaceAllString(route.Path, "example")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(route.Method, path, strings.NewReader("unread body")))
		if path == "/health" {
			require.Equal(t, previous, observed)
		} else {
			require.Equal(t, previous+1, observed, "%s %s escaped logging", route.Method, route.Path)
		}
	}
	t.Logf("observed all %d gateway registrations before authentication/admission", observed)
}
