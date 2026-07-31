package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/flowverse/flowverse-api/internal/auth"
	"github.com/flowverse/flowverse-api/internal/domain"
	"github.com/flowverse/flowverse-api/internal/engine"
	"github.com/flowverse/flowverse-api/internal/parser"
	"github.com/flowverse/flowverse-api/internal/runtime"
	"github.com/flowverse/flowverse-api/internal/store"
)

const (
	userContextKey = "flowverse.user"
	authSourceKey  = "flowverse.auth_source"
	maxBodyBytes   = 1 << 20
)

type Config struct {
	PublicOrigin  string
	SecureCookies bool
}

type Server struct {
	repository   store.Repository
	auth         *auth.Service
	parser       parser.FlowParser
	simulator    *engine.Simulator
	runs         *runtime.Manager
	config       Config
	limiter      *rateLimiter
	ratePolicies map[string]ratePolicy
}

func New(repository store.Repository, authService *auth.Service, flowParser parser.FlowParser, runManager *runtime.Manager, config Config) *Server {
	if flowParser == nil {
		flowParser = parser.NewMock()
	}
	if runManager == nil {
		runManager = runtime.NewManager(repository)
	}
	return &Server{
		repository: repository, auth: authService, parser: flowParser,
		simulator: engine.NewSimulator(), runs: runManager, config: config,
		limiter: newRateLimiter(10000, time.Minute, time.Now),
		ratePolicies: map[string]ratePolicy{
			"auth.register":    {Limit: 5, Window: 10 * time.Minute},
			"auth.login":       {Limit: 10, Window: time.Minute},
			"auth.refresh":     {Limit: 30, Window: time.Minute},
			"flows.parse_text": {Limit: 10, Window: time.Minute},
		},
	}
}

func (s *Server) Router() *gin.Engine {
	router := gin.New()
	_ = router.SetTrustedProxies(nil)
	router.Use(gin.Recovery(), s.securityHeaders(), s.requestID(), s.cors(), bodyLimit())
	router.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	router.GET("/readyz", s.readiness)
	router.GET("/health/live", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	router.GET("/health/ready", s.readiness)
	router.GET("/public/v1/shares/:token", s.publicShare)

	v1 := router.Group("/v1")
	v1.POST("/auth/register", s.rateLimit("auth.register"), s.register)
	v1.POST("/auth/login", s.rateLimit("auth.login"), s.login)
	v1.POST("/auth/refresh", s.rateLimit("auth.refresh"), s.refresh)
	v1.POST("/auth/logout", s.optionalAuth(), s.csrf(), s.logout)
	v1.GET("/public/shares/:token", s.publicShare)
	v1.GET("/runs/:runId/live", s.liveRun)

	secured := v1.Group("")
	secured.Use(s.requireAuth(), s.csrf())
	secured.GET("/auth/me", s.me)

	secured.POST("/projects", s.createProject)
	secured.GET("/projects", s.listProjects)
	secured.GET("/projects/:projectId", s.getProject)
	secured.PUT("/projects/:projectId", s.updateProject)
	secured.PATCH("/projects/:projectId", s.patchProject)
	secured.DELETE("/projects/:projectId", s.deleteProject)
	secured.GET("/projects/:projectId/members", s.listMembers)
	secured.POST("/projects/:projectId/members", s.addMember)
	secured.DELETE("/projects/:projectId/members/:userId", s.removeMember)
	secured.GET("/projects/:projectId/flows", s.listFlows)
	secured.POST("/projects/:projectId/flows", s.createFlow)

	secured.POST("/flows", s.createFlow)
	secured.GET("/flows/:flowId", s.getFlow)
	secured.PUT("/flows/:flowId", s.updateFlow)
	secured.PATCH("/flows/:flowId", s.patchFlow)
	secured.DELETE("/flows/:flowId", s.deleteFlow)
	secured.GET("/flows/:flowId/draft", s.getFlowDraft)
	secured.PUT("/flows/:flowId/draft", s.replaceFlowDraft)
	secured.POST("/flows/:flowId/versions", s.publishVersion)
	secured.POST("/flows/:flowId/publish", s.publishVersion)
	secured.GET("/flows/:flowId/versions", s.listVersions)
	secured.GET("/flows/:flowId/runs", s.listRuns)
	secured.POST("/flows/:flowId/runs", s.createDraftRun)
	secured.POST("/flows/:flowId/validate", s.validateDraft)
	secured.POST("/flows/:flowId/analyze", s.analyzeDraft)

	secured.GET("/flow-versions/:versionId", s.getVersion)
	secured.POST("/flow-versions/:versionId/validate", s.validateVersion)
	secured.POST("/flow-versions/:versionId/analyze", s.analyzeVersion)
	secured.GET("/flow-versions/:versionId/analysis", s.analyzeVersion)
	secured.POST("/flow-versions/:versionId/runs", s.createRun)

	secured.POST("/flows/import", s.importFlow)
	secured.POST("/flows/parse-text", s.rateLimit("flows.parse_text"), s.parseText)

	secured.GET("/runs/:runId", s.getRun)
	secured.GET("/runs/:runId/events", s.getRunEvents)
	secured.POST("/runs/:runId/pause", s.pauseRun)
	secured.POST("/runs/:runId/resume", s.resumeRun)
	secured.POST("/runs/:runId/step", s.stepRun)
	secured.POST("/runs/:runId/speed", s.speedRun)
	secured.PATCH("/runs/:runId/speed", s.speedRun)
	secured.POST("/runs/:runId/cancel", s.cancelRun)
	secured.POST("/runs/:runId/live-ticket", s.liveTicket)
	secured.POST("/runs/:runId/ws-ticket", s.liveTicket)

	secured.POST("/shares", s.createShare)
	secured.DELETE("/shares/:shareId", s.revokeShare)
	secured.GET("/flows/:flowId/share-links", s.listShareLinks)
	secured.POST("/flows/:flowId/share-links", s.createShare)
	secured.DELETE("/share-links/:shareId", s.revokeShare)
	return router
}

func (s *Server) readiness(c *gin.Context) {
	if dependency, ok := s.repository.(interface{ Ping(context.Context) error }); ok {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := dependency.Ping(ctx); err != nil {
			writeError(c, http.StatusServiceUnavailable, "dependency.unavailable", "A required dependency is unavailable", nil)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) requestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-ID")
		if id == "" || len(id) > 100 {
			id = uuid.NewString()
		}
		c.Header("X-Request-ID", id)
		c.Next()
	}
}

func (s *Server) securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		c.Next()
	}
}

func (s *Server) cors() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && origin == s.config.PublicOrigin {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-CSRF-Token, If-Match, Idempotency-Key, X-Request-ID")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			c.Header("Access-Control-Expose-Headers", "ETag, X-Draft-Revision, X-Request-ID, Retry-After")
		}
		if c.Request.Method == http.MethodOptions {
			if origin != "" && origin != s.config.PublicOrigin {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func bodyLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
		}
		c.Next()
	}
}

func (s *Server) requireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		user, source, err := s.authenticateRequest(c)
		if err != nil {
			writeError(c, http.StatusUnauthorized, "auth.required", "Authentication is required", nil)
			c.Abort()
			return
		}
		c.Set(userContextKey, user)
		c.Set(authSourceKey, source)
		c.Next()
	}
}

func (s *Server) optionalAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		user, source, err := s.authenticateRequest(c)
		if err == nil {
			c.Set(userContextKey, user)
			c.Set(authSourceKey, source)
		} else if _, accessErr := c.Cookie("flowverse_access"); accessErr == nil {
			c.Set(authSourceKey, "cookie")
		} else if _, refreshErr := c.Cookie("flowverse_refresh"); refreshErr == nil {
			c.Set(authSourceKey, "cookie")
		}
		c.Next()
	}
}

func (s *Server) authenticateRequest(c *gin.Context) (domain.User, string, error) {
	header := c.GetHeader("Authorization")
	if strings.HasPrefix(header, "Bearer ") {
		user, err := s.auth.Authenticate(c.Request.Context(), strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")))
		return user, "bearer", err
	}
	token, err := c.Cookie("flowverse_access")
	if err != nil {
		return domain.User{}, "", err
	}
	user, err := s.auth.Authenticate(c.Request.Context(), token)
	return user, "cookie", err
}

func (s *Server) csrf() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead || c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		source, _ := c.Get(authSourceKey)
		if source == "cookie" {
			cookie, err := c.Cookie("flowverse_csrf")
			header := c.GetHeader("X-CSRF-Token")
			if err != nil || cookie == "" || header == "" || cookie != header {
				writeError(c, http.StatusForbidden, "csrf.invalid", "A valid CSRF token is required", nil)
				c.Abort()
				return
			}
		}
		c.Next()
	}
}

func currentUser(c *gin.Context) domain.User {
	value, _ := c.Get(userContextKey)
	user, _ := value.(domain.User)
	return user
}

func (s *Server) allowProject(c *gin.Context, projectID string, required domain.Role) bool {
	role, err := s.repository.ProjectRole(c.Request.Context(), projectID, currentUser(c).ID)
	if err != nil || !role.Allows(required) {
		writeError(c, http.StatusNotFound, "resource.not_found", "Resource not found", nil)
		return false
	}
	return true
}

func writeError(c *gin.Context, status int, code, message string, details any) {
	requestID := c.Writer.Header().Get("X-Request-ID")
	c.JSON(status, gin.H{"code": code, "message": message, "requestId": requestID, "details": details})
}

func bindJSON(c *gin.Context, target any) bool {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(c, http.StatusRequestEntityTooLarge, "request.too_large", "Request body exceeds 1 MiB", nil)
			return false
		}
		writeError(c, http.StatusBadRequest, "request.invalid_json", "Request body is invalid", gin.H{"reason": err.Error()})
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(c, http.StatusBadRequest, "request.invalid_json", "Request body must contain exactly one JSON document", nil)
		return false
	}
	return true
}

func checksum(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

func parseAfterSequence(c *gin.Context) int64 {
	value, _ := strconv.ParseInt(c.Query("afterSequence"), 10, 64)
	if value < 0 {
		return 0
	}
	return value
}

func mapStoreError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(c, http.StatusNotFound, "resource.not_found", "Resource not found", nil)
	case errors.Is(err, store.ErrConflict):
		writeError(c, http.StatusConflict, "resource.conflict", "Resource already exists", nil)
	case errors.Is(err, store.ErrPrecondition):
		writeError(c, http.StatusPreconditionFailed, "draft.conflict", "Draft changed since it was loaded", nil)
	default:
		writeError(c, http.StatusInternalServerError, "internal.error", "Unexpected server error", nil)
	}
}

func now() time.Time { return time.Now().UTC() }

func validRole(role domain.Role) bool {
	return role == domain.RoleEditor || role == domain.RoleViewer
}

func parseDuration(value, fallback string) time.Duration {
	duration, err := time.ParseDuration(value)
	if err != nil {
		duration, _ = time.ParseDuration(fallback)
	}
	return duration
}

func etagMatches(value, expected string) bool {
	return value == expected || strings.Trim(value, `"`) == strings.Trim(expected, `"`)
}

func requiredString(value, name string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	return nil
}
