package httpapi

import (
	"net/http/httptest"
	"testing"
)

func TestShouldTraceRequestExcludesCapabilityTokens(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "/v1/flows/flow-id", want: true},
		{path: "/health/ready", want: false},
		{path: "/readyz", want: false},
		{path: "/public/v1/shares/reusable-secret-token", want: false},
		{path: "/v1/public/shares/reusable-secret-token", want: false},
	}
	for _, test := range tests {
		request := httptest.NewRequest("GET", test.path, nil)
		if got := shouldTraceRequest(request); got != test.want {
			t.Fatalf("shouldTraceRequest(%q) = %v, want %v", test.path, got, test.want)
		}
	}
}
