package parser

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/flowverse/flowverse-api/internal/engine"
)

func TestMockParser(t *testing.T) {
	result, err := NewMock().Parse(context.Background(), "Si el pago fue aprobado, preparar el pedido; si no, devolver el dinero.")
	if err != nil {
		t.Fatal(err)
	}
	if result.Provider != "mock" || len(result.Ambiguities) == 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if validation := engine.Validate(result.Proposal); !validation.Valid {
		t.Fatalf("mock generated invalid flow: %+v", validation.Issues)
	}
}

func TestOpenAIUsesResponsesStructuredOutputs(t *testing.T) {
	mockResult, err := NewMock().Parse(context.Background(), "Validar una solicitud y terminar.")
	if err != nil {
		t.Fatal(err)
	}
	proposalRaw, _ := json.Marshal(mockResult.Proposal)
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v1/responses" || request.Method != http.MethodPost {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatal("missing bearer key")
		}
		raw, _ := io.ReadAll(request.Body)
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["store"] != false {
			t.Fatalf("store must be false: %#v", payload)
		}
		text := payload["text"].(map[string]any)
		format := text["format"].(map[string]any)
		if format["type"] != "json_schema" || format["strict"] != true {
			t.Fatalf("Structured Outputs not enabled: %#v", format)
		}
		responseRaw, _ := json.Marshal(map[string]any{
			"status": "completed",
			"output": []any{map[string]any{"content": []any{map[string]any{
				"type": "output_text", "text": string(proposalRaw),
			}}}},
		})
		return jsonResponse(string(responseRaw)), nil
	})}
	client := NewOpenAI("test-key", "test-model", httpClient)
	result, err := client.Parse(context.Background(), "Validar una solicitud y terminar.")
	if err != nil {
		t.Fatal(err)
	}
	if result.Provider != "openai" || result.Proposal.Name == "" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestOpenAIIncompleteAndRefusalAreRecoverable(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"incomplete", `{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]}`, "incomplete"},
		{"refusal", `{"status":"completed","output":[{"content":[{"type":"refusal","refusal":"not allowed"}]}]}`, "refused"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return jsonResponse(test.body), nil
			})}
			client := NewOpenAI("test-key", "test-model", httpClient)
			_, err := client.Parse(context.Background(), "Un proceso suficientemente detallado.")
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(body)),
	}
}
