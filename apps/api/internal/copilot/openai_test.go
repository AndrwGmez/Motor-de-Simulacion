package copilot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestOpenAIUsesStrictStatelessStructuredOutput(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("authorization = %q", request.Header.Get("Authorization"))
		}
		raw, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["store"] != false || payload["safety_identifier"] != "safe-user" {
			t.Fatalf("privacy controls missing: %#v", payload)
		}
		if payload["max_output_tokens"] != float64(4000) {
			t.Fatalf("output budget missing: %#v", payload["max_output_tokens"])
		}
		text := payload["text"].(map[string]any)
		format := text["format"].(map[string]any)
		if format["type"] != "json_schema" || format["strict"] != true {
			t.Fatalf("structured output config = %#v", format)
		}
		schema := format["schema"].(map[string]any)
		if schema["additionalProperties"] != false {
			t.Fatalf("root schema is not strict: %#v", schema)
		}
		body := `{"status":"completed","output":[{"content":[{"type":"output_text","text":"{\"summary\":\"ok\",\"suggestions\":[{\"title\":\"Inspect\",\"explanation\":\"Fact\",\"severity\":\"info\",\"confidence\":\"high\",\"evidenceIds\":[\"flow:summary\"],\"actions\":[{\"kind\":\"none\",\"targetId\":null,\"label\":\"Review\"}]}],\"limitations\":[]}"}]}]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	provider := NewOpenAI("test-key", "test-model", &http.Client{Transport: transport})
	draft, err := provider.Advise(context.Background(), Prompt{
		Question: "Review", SafetyIdentifier: "safe-user",
		Evidence: []EvidenceItem{{ID: "flow:summary", Kind: "flow", Summary: "summary", Facts: map[string]any{}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if draft.Summary != "ok" || len(draft.Suggestions) != 1 {
		t.Fatalf("draft = %#v", draft)
	}
}

func TestOpenAIHandlesIncompleteRefusalAndMissingOutput(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "incomplete", body: `{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}`, want: "incomplete"},
		{name: "refusal", body: `{"status":"completed","output":[{"content":[{"type":"refusal","refusal":"cannot comply"}]}]}`, want: "refused"},
		{name: "missing", body: `{"status":"completed","output":[]}`, want: "did not contain"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(test.body)), Header: make(http.Header)}, nil
			})
			_, err := NewOpenAI("key", "model", &http.Client{Transport: transport}).Advise(context.Background(), Prompt{Question: "Review"})
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
