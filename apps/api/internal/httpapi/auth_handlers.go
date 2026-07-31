package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/flowverse/flowverse-api/internal/auth"
	"github.com/flowverse/flowverse-api/internal/store"
)

type credentialsRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) register(c *gin.Context) {
	var request struct {
		Email       string `json:"email"`
		Password    string `json:"password"`
		DisplayName string `json:"displayName"`
	}
	if !bindJSON(c, &request) {
		return
	}
	user, pair, err := s.auth.Register(c.Request.Context(), request.Email, request.Password, request.DisplayName)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(c, http.StatusConflict, "auth.email_exists", "An account already exists for this email", nil)
		} else {
			writeError(c, http.StatusUnprocessableEntity, "auth.invalid_registration", err.Error(), nil)
		}
		return
	}
	s.setSessionCookies(c, pair)
	c.JSON(http.StatusCreated, gin.H{"user": user, "csrfToken": pair.CSRFToken, "accessExpiresAt": pair.AccessExpiry})
}

func (s *Server) login(c *gin.Context) {
	var request credentialsRequest
	if !bindJSON(c, &request) {
		return
	}
	user, pair, err := s.auth.Login(c.Request.Context(), request.Email, request.Password)
	if err != nil {
		writeError(c, http.StatusUnauthorized, "auth.invalid_credentials", "Email or password is incorrect", nil)
		return
	}
	s.setSessionCookies(c, pair)
	c.JSON(http.StatusOK, gin.H{"user": user, "csrfToken": pair.CSRFToken, "accessExpiresAt": pair.AccessExpiry})
}

func (s *Server) refresh(c *gin.Context) {
	refresh, err := c.Cookie("flowverse_refresh")
	if err != nil {
		writeError(c, http.StatusUnauthorized, "auth.invalid_refresh", "Refresh session is invalid", nil)
		return
	}
	csrfCookie, csrfErr := c.Cookie("flowverse_csrf")
	if csrfErr != nil || csrfCookie == "" || csrfCookie != c.GetHeader("X-CSRF-Token") {
		writeError(c, http.StatusForbidden, "csrf.invalid", "A valid CSRF token is required", nil)
		return
	}
	pair, err := s.auth.Refresh(c.Request.Context(), refresh)
	if err != nil {
		s.clearSessionCookies(c)
		writeError(c, http.StatusUnauthorized, "auth.invalid_refresh", "Refresh session is invalid", nil)
		return
	}
	s.setSessionCookies(c, pair)
	user, authErr := s.auth.Authenticate(c.Request.Context(), pair.AccessToken)
	if authErr != nil {
		s.clearSessionCookies(c)
		writeError(c, http.StatusInternalServerError, "auth.refresh_failed", "Session could not be refreshed", nil)
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": user, "csrfToken": pair.CSRFToken, "accessExpiresAt": pair.AccessExpiry})
}

func (s *Server) logout(c *gin.Context) {
	access, _ := c.Cookie("flowverse_access")
	refresh, _ := c.Cookie("flowverse_refresh")
	s.auth.Logout(c.Request.Context(), access, refresh)
	s.clearSessionCookies(c)
	c.Status(http.StatusNoContent)
}

func (s *Server) me(c *gin.Context) {
	c.JSON(http.StatusOK, currentUser(c))
}

func (s *Server) setSessionCookies(c *gin.Context, pair auth.TokenPair) {
	c.SetSameSite(http.SameSiteLaxMode)
	accessAge := maxAge(pair.AccessExpiry.Sub(time.Now()))
	c.SetCookie("flowverse_access", pair.AccessToken, accessAge, "/", "", s.config.SecureCookies, true)
	c.SetCookie("flowverse_refresh", pair.RefreshToken, int((30 * 24 * time.Hour).Seconds()), "/v1/auth", "", s.config.SecureCookies, true)
	c.SetCookie("flowverse_csrf", pair.CSRFToken, int((30 * 24 * time.Hour).Seconds()), "/", "", s.config.SecureCookies, false)
}

func (s *Server) clearSessionCookies(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("flowverse_access", "", -1, "/", "", s.config.SecureCookies, true)
	c.SetCookie("flowverse_refresh", "", -1, "/v1/auth", "", s.config.SecureCookies, true)
	c.SetCookie("flowverse_csrf", "", -1, "/", "", s.config.SecureCookies, false)
}

func maxAge(duration time.Duration) int {
	if duration <= 0 {
		return 0
	}
	return int(duration.Seconds())
}
