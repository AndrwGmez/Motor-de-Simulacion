package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type admissionGate struct {
	slots chan struct{}
}

func newAdmissionGate(maxConcurrent int) *admissionGate {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &admissionGate{slots: make(chan struct{}, maxConcurrent)}
}

func (gate *admissionGate) acquire() bool {
	select {
	case gate.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (gate *admissionGate) release() {
	<-gate.slots
}

func (s *Server) admit(name string) gin.HandlerFunc {
	return func(c *gin.Context) {
		gate := s.admissions[name]
		if gate == nil {
			c.Next()
			return
		}
		if !gate.acquire() {
			c.Header("Retry-After", "1")
			writeError(c, http.StatusServiceUnavailable, "admission.overloaded", "Server is busy; retry later", nil)
			c.Abort()
			return
		}
		defer gate.release()
		c.Next()
	}
}
