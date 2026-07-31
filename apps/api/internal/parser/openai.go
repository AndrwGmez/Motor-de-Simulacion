package parser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/flowverse/flowverse-api/internal/contract"
	"github.com/flowverse/flowverse-api/internal/domain"
)

type OpenAI struct {
	apiKey string
	model  string
	url    string
	client *http.Client
}

func NewOpenAI(apiKey, model string, client *http.Client) *OpenAI {
	if model == "" {
		model = "gpt-4.1-mini"
	}
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	return &OpenAI{apiKey: apiKey, model: model, url: "https://api.openai.com/v1/responses", client: client}
}

func (o *OpenAI) Parse(ctx context.Context, text string) (Result, error) {
	if o.apiKey == "" {
		return Result{}, errors.New("OPENAI_API_KEY is not configured")
	}
	if strings.TrimSpace(text) == "" {
		return Result{}, errors.New("text is required")
	}
	body := map[string]any{
		"model": o.model,
		"store": false,
		"input": []map[string]any{
			{"role": "system", "content": "Convierte el proceso del usuario a un FlowVerse FlowDefinition 1.0. No inventes integraciones reales. Usa puertos explícitos, exactamente un camino default por decisión y JSON Pointer para variables."},
			{"role": "user", "content": text},
		},
		"text": map[string]any{"format": map[string]any{
			"type": "json_schema", "name": "flow_proposal", "strict": true, "schema": openAIFlowSchema(),
		}},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Result{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, o.url, bytes.NewReader(raw))
	if err != nil {
		return Result{}, err
	}
	request.Header.Set("Authorization", "Bearer "+o.apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := o.client.Do(request)
	if err != nil {
		return Result{}, fmt.Errorf("OpenAI request failed: %w", err)
	}
	defer response.Body.Close()
	responseRaw, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return Result{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Result{}, fmt.Errorf("OpenAI returned %d: %s", response.StatusCode, strings.TrimSpace(string(responseRaw)))
	}
	var envelope struct {
		Status            string `json:"status"`
		IncompleteDetails any    `json:"incomplete_details"`
		Output            []struct {
			Content []struct {
				Type    string `json:"type"`
				Text    string `json:"text"`
				Refusal string `json:"refusal"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(responseRaw, &envelope); err != nil {
		return Result{}, fmt.Errorf("invalid OpenAI response: %w", err)
	}
	if envelope.Status == "incomplete" {
		return Result{}, fmt.Errorf("OpenAI response incomplete: %v", envelope.IncompleteDetails)
	}
	var outputText string
	for _, output := range envelope.Output {
		for _, content := range output.Content {
			if content.Type == "refusal" || content.Refusal != "" {
				return Result{}, fmt.Errorf("OpenAI refused the request: %s", content.Refusal)
			}
			if content.Type == "output_text" && content.Text != "" {
				outputText += content.Text
			}
		}
	}
	if outputText == "" {
		return Result{}, errors.New("OpenAI response did not contain structured output")
	}
	var proposal domain.FlowDefinition
	if err := json.Unmarshal([]byte(outputText), &proposal); err != nil {
		return Result{}, fmt.Errorf("invalid structured output: %w", err)
	}
	cleanGeneratedConfiguration(&proposal)
	proposal = proposal.Normalize()
	validation := contract.ValidateFlow(proposal)
	if !validation.Valid {
		return Result{}, fmt.Errorf("generated proposal failed FlowVerse validation: %v", validation.Issues)
	}
	return Result{Proposal: proposal, Warnings: []string{}, Ambiguities: []Ambiguity{}, Provider: "openai"}, nil
}

// The Responses API receives a deliberately compact strict schema. Full
// FlowVerse semantic validation still runs locally before returning a preview.
func openAIFlowSchema() map[string]any {
	position := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"x", "y", "z"}, "properties": map[string]any{
		"x": map[string]any{"type": "number"}, "y": map[string]any{"type": "number"}, "z": map[string]any{"type": "number"},
	}}
	port := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id", "label"}, "properties": map[string]any{
		"id": map[string]any{"type": "string"}, "label": map[string]any{"type": "string"},
	}}
	operation := map[string]any{"type": "object", "additionalProperties": false,
		"required": []string{"op", "path", "from", "value"},
		"properties": map[string]any{
			"op":    map[string]any{"type": "string", "enum": []string{"set", "copy", "delete"}},
			"path":  map[string]any{"type": "string"},
			"from":  map[string]any{"anyOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "null"}}},
			"value": map[string]any{},
		},
	}
	configuration := map[string]any{"type": "object", "additionalProperties": false,
		"required": []string{"eventName", "operations", "strategy", "service", "latencyMs", "outcome", "response", "errorCode", "delayMs", "result", "output", "collapsed"},
		"properties": map[string]any{
			"eventName": nullable("string"), "operations": map[string]any{"type": "array", "items": operation},
			"strategy": nullableEnum("first_match", "all_matches"), "service": nullable("string"),
			"latencyMs": nullable("integer"), "outcome": nullableEnum("success", "failure"),
			"response": map[string]any{}, "errorCode": nullable("string"), "delayMs": nullable("integer"),
			"result": nullableEnum("success", "failure"), "output": map[string]any{}, "collapsed": nullable("boolean"),
		},
	}
	node := map[string]any{"type": "object", "additionalProperties": false,
		"required": []string{"id", "type", "label", "description", "inputs", "outputs", "activationMode", "durationMs", "configuration", "position", "locked"},
		"properties": map[string]any{
			"id": map[string]any{"type": "string"}, "type": map[string]any{"type": "string", "enum": []string{"trigger", "process", "decision", "data", "integration", "delay", "end", "group"}},
			"label": map[string]any{"type": "string"}, "description": map[string]any{"type": "string"},
			"inputs": map[string]any{"type": "array", "items": port}, "outputs": map[string]any{"type": "array", "items": port},
			"activationMode": map[string]any{"type": "string", "enum": []string{"each", "any", "all"}},
			"durationMs":     map[string]any{"type": "integer"}, "configuration": configuration,
			"position": position, "locked": map[string]any{"type": "boolean"},
		},
	}
	condition := map[string]any{"anyOf": []any{
		map[string]any{"type": "null"},
		map[string]any{"type": "object", "additionalProperties": false,
			"required": []string{"field", "operator", "value"},
			"properties": map[string]any{
				"field":    map[string]any{"type": "string"},
				"operator": map[string]any{"type": "string", "enum": []string{"equals", "not_equals", "greater_than", "greater_than_or_equal", "less_than", "less_than_or_equal", "contains", "not_contains", "exists", "not_exists"}},
				"value":    map[string]any{},
			}},
	}}
	edge := map[string]any{"type": "object", "additionalProperties": false,
		"required": []string{"id", "source", "target", "sourcePort", "targetPort", "label", "condition", "priority", "isDefault"},
		"properties": map[string]any{
			"id": map[string]any{"type": "string"}, "source": map[string]any{"type": "string"}, "target": map[string]any{"type": "string"},
			"sourcePort": map[string]any{"type": "string"}, "targetPort": map[string]any{"type": "string"}, "label": map[string]any{"type": "string"},
			"condition": condition, "priority": map[string]any{"type": "integer"}, "isDefault": map[string]any{"type": "boolean"},
		},
	}
	variable := map[string]any{"type": "object", "additionalProperties": false,
		"required": []string{"path", "type", "required", "description", "default"},
		"properties": map[string]any{
			"path": map[string]any{"type": "string"}, "type": map[string]any{"type": "string"}, "required": map[string]any{"type": "boolean"},
			"description": map[string]any{"type": "string"}, "default": map[string]any{},
		},
	}
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"schemaVersion", "name", "description", "variables", "layout", "nodes", "edges"},
		"properties": map[string]any{
			"schemaVersion": map[string]any{"type": "string", "const": domain.SchemaVersion},
			"name":          map[string]any{"type": "string"}, "description": map[string]any{"type": "string"},
			"variables": map[string]any{"type": "array", "items": variable},
			"layout":    map[string]any{"type": "object", "additionalProperties": false, "required": []string{"mode"}, "properties": map[string]any{"mode": map[string]any{"type": "string", "enum": []string{"force", "directional", "layers", "timeline", "clusters", "execution"}}}},
			"nodes":     map[string]any{"type": "array", "items": node},
			"edges":     map[string]any{"type": "array", "items": edge},
		},
	}
}

func nullable(kind string) map[string]any {
	return map[string]any{"anyOf": []any{map[string]any{"type": kind}, map[string]any{"type": "null"}}}
}

func nullableEnum(values ...string) map[string]any {
	return map[string]any{"anyOf": []any{map[string]any{"type": "string", "enum": values}, map[string]any{"type": "null"}}}
}

func cleanGeneratedConfiguration(flow *domain.FlowDefinition) {
	allowed := map[domain.NodeType]map[string]bool{
		domain.NodeTrigger:     {"eventName": true},
		domain.NodeProcess:     {"operations": true},
		domain.NodeDecision:    {"strategy": true},
		domain.NodeData:        {"operations": true},
		domain.NodeIntegration: {"service": true, "latencyMs": true, "outcome": true, "response": true, "errorCode": true},
		domain.NodeDelay:       {"delayMs": true},
		domain.NodeEnd:         {"result": true, "output": true},
		domain.NodeGroup:       {"collapsed": true},
	}
	for index := range flow.Nodes {
		node := &flow.Nodes[index]
		clean := map[string]any{}
		for key, value := range node.Configuration {
			if allowed[node.Type][key] && value != nil {
				clean[key] = value
			}
		}
		switch node.Type {
		case domain.NodeDecision:
			if _, ok := clean["strategy"]; !ok {
				clean["strategy"] = "first_match"
			}
		case domain.NodeIntegration:
			if _, ok := clean["latencyMs"]; !ok {
				clean["latencyMs"] = float64(0)
			}
			if _, ok := clean["outcome"]; !ok {
				clean["outcome"] = "success"
			}
		case domain.NodeDelay:
			if _, ok := clean["delayMs"]; !ok {
				clean["delayMs"] = float64(0)
			}
		case domain.NodeEnd:
			if _, ok := clean["result"]; !ok {
				clean["result"] = "success"
			}
		}
		if operations, ok := clean["operations"].([]any); ok {
			for _, raw := range operations {
				if operation, ok := raw.(map[string]any); ok {
					for key, value := range operation {
						if value == nil {
							delete(operation, key)
						}
					}
				}
			}
		}
		node.Configuration = clean
	}
	for index := range flow.Edges {
		condition := flow.Edges[index].Condition
		if condition != nil && (condition.Operator == "exists" || condition.Operator == "not_exists") {
			condition.Value = nil
		}
	}
}
