package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/flowverse/flowverse-api/internal/domain"
	"github.com/flowverse/flowverse-api/internal/engine"
)

func TestValidateSimulationRequestContractBounds(t *testing.T) {
	valid := simulationRequest{TriggerNodeID: "start", Input: map[string]any{}}
	if err := validateSimulationRequest(valid); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	maximumSteps, maximumVisits := engine.SimulationMaxSteps, engine.SimulationMaxVisitsPerNode
	valid.Limits = &simulationLimits{MaxSteps: &maximumSteps, MaxVisitsPerNode: &maximumVisits}
	if err := validateSimulationRequest(valid); err != nil {
		t.Fatalf("contract maxima rejected: %v", err)
	}

	tooManyProperties := map[string]any{}
	for index := 0; index <= engine.SimulationMaxInputProperties; index++ {
		tooManyProperties[fmt.Sprintf("property-%03d", index)] = index
	}
	tooManyOverrides := make([]simulationOverride, engine.SimulationMaxOverrides+1)
	for index := range tooManyOverrides {
		tooManyOverrides[index] = simulationOverride{Type: "force_edge", EdgeID: "edge"}
	}
	zero, excessiveSteps, excessiveVisits := 0, engine.SimulationMaxSteps+1, engine.SimulationMaxVisitsPerNode+1
	tests := []struct {
		name    string
		request simulationRequest
	}{
		{name: "missing trigger", request: simulationRequest{Input: map[string]any{}}},
		{name: "invalid trigger", request: simulationRequest{TriggerNodeID: "not valid", Input: map[string]any{}}},
		{name: "missing input", request: simulationRequest{TriggerNodeID: "start"}},
		{name: "too many input properties", request: simulationRequest{TriggerNodeID: "start", Input: tooManyProperties}},
		{name: "too many overrides", request: simulationRequest{TriggerNodeID: "start", Input: map[string]any{}, Overrides: tooManyOverrides}},
		{name: "zero steps", request: simulationRequest{TriggerNodeID: "start", Input: map[string]any{}, Limits: &simulationLimits{MaxSteps: &zero}}},
		{name: "excessive steps", request: simulationRequest{TriggerNodeID: "start", Input: map[string]any{}, Limits: &simulationLimits{MaxSteps: &excessiveSteps}}},
		{name: "excessive visits", request: simulationRequest{TriggerNodeID: "start", Input: map[string]any{}, Limits: &simulationLimits{MaxVisitsPerNode: &excessiveVisits}}},
		{name: "force edge missing id", request: simulationRequest{TriggerNodeID: "start", Input: map[string]any{}, Overrides: []simulationOverride{{Type: "force_edge"}}}},
		{name: "force edge extra field", request: simulationRequest{TriggerNodeID: "start", Input: map[string]any{}, Overrides: []simulationOverride{{Type: "force_edge", EdgeID: "edge", Code: "unexpected"}}}},
		{name: "failed node missing code", request: simulationRequest{TriggerNodeID: "start", Input: map[string]any{}, Overrides: []simulationOverride{{Type: "fail_node", NodeID: "node"}}}},
		{name: "failed node long message", request: simulationRequest{TriggerNodeID: "start", Input: map[string]any{}, Overrides: []simulationOverride{{Type: "fail_node", NodeID: "node", Code: "failed", Message: strings.Repeat("x", 501)}}}},
		{name: "unknown override", request: simulationRequest{TriggerNodeID: "start", Input: map[string]any{}, Overrides: []simulationOverride{{Type: "execute_code"}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateSimulationRequest(test.request); err == nil {
				t.Fatal("invalid simulation request was accepted")
			}
		})
	}
}

func TestRunEndpointsRejectContractLimitsBeforePersistence(t *testing.T) {
	client, repository := newTestServer()
	client.register(t, "simulation-limits@example.com")
	projectResponse := client.request(t, http.MethodPost, "/v1/projects", map[string]any{"name": "Limits"}, nil)
	var project domain.Project
	if err := json.Unmarshal(projectResponse.Body.Bytes(), &project); err != nil {
		t.Fatal(err)
	}
	flowResponse := client.request(t, http.MethodPost, "/v1/projects/"+project.ID+"/flows", map[string]any{"name": "Bounded"}, nil)
	var flow struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(flowResponse.Body.Bytes(), &flow); err != nil {
		t.Fatal(err)
	}
	publishResponse := client.request(t, http.MethodPost, "/v1/flows/"+flow.ID+"/versions", nil, nil)
	var version struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(publishResponse.Body.Bytes(), &version); err != nil {
		t.Fatal(err)
	}

	paths := []string{
		"/v1/flows/" + flow.ID + "/runs",
		"/v1/flow-versions/" + version.ID + "/runs",
	}
	for index, path := range paths {
		response := client.request(t, http.MethodPost, path, map[string]any{
			"triggerNodeId": "start", "input": map[string]any{},
			"limits": map[string]any{"maxSteps": 0},
		}, map[string]string{"Idempotency-Key": fmt.Sprintf("invalid-limit-%02d", index)})
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s status=%d: %s", path, response.Code, response.Body.String())
		}
		assertErrorCode(t, response, "run.invalid_request")
	}

	runs, err := repository.ListRuns(context.Background(), flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("invalid requests persisted %d runs", len(runs))
	}

	valid := client.request(t, http.MethodPost, paths[0], map[string]any{
		"triggerNodeId": "start", "input": map[string]any{},
		"limits": map[string]any{
			"maxSteps":         engine.SimulationMaxSteps,
			"maxVisitsPerNode": engine.SimulationMaxVisitsPerNode,
		},
	}, map[string]string{"Idempotency-Key": "maximum-limits-valid"})
	if valid.Code != http.StatusCreated {
		t.Fatalf("contract maxima status=%d: %s", valid.Code, valid.Body.String())
	}
}
