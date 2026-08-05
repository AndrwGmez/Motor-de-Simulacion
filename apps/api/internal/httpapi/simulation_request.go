package httpapi

import (
	"fmt"
	"regexp"
	"unicode/utf8"

	"github.com/flowverse/flowverse-api/internal/engine"
)

var simulationIdentifierPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

func validateSimulationRequest(request simulationRequest) error {
	if !simulationIdentifierPattern.MatchString(request.TriggerNodeID) {
		return fmt.Errorf("triggerNodeId must be a valid identifier")
	}
	if request.Input == nil {
		return fmt.Errorf("input must be an object")
	}
	if len(request.Input) > engine.SimulationMaxInputProperties {
		return fmt.Errorf("input must contain at most %d properties", engine.SimulationMaxInputProperties)
	}
	if len(request.Overrides) > engine.SimulationMaxOverrides {
		return fmt.Errorf("overrides must contain at most %d entries", engine.SimulationMaxOverrides)
	}
	for index, override := range request.Overrides {
		switch override.Type {
		case "force_edge":
			if !simulationIdentifierPattern.MatchString(override.EdgeID) {
				return fmt.Errorf("overrides[%d].edgeId must be a valid identifier", index)
			}
			if override.NodeID != "" || override.Code != "" || override.Message != "" {
				return fmt.Errorf("overrides[%d] contains fields not allowed for force_edge", index)
			}
		case "fail_node":
			if !simulationIdentifierPattern.MatchString(override.NodeID) {
				return fmt.Errorf("overrides[%d].nodeId must be a valid identifier", index)
			}
			codeLength := utf8.RuneCountInString(override.Code)
			if codeLength < 1 || codeLength > 100 {
				return fmt.Errorf("overrides[%d].code must contain between 1 and 100 characters", index)
			}
			if utf8.RuneCountInString(override.Message) > 500 {
				return fmt.Errorf("overrides[%d].message must contain at most 500 characters", index)
			}
			if override.EdgeID != "" {
				return fmt.Errorf("overrides[%d] contains fields not allowed for fail_node", index)
			}
		default:
			return fmt.Errorf("overrides[%d].type must be force_edge or fail_node", index)
		}
	}
	if request.Limits == nil {
		return nil
	}
	if request.Limits.MaxSteps != nil && (*request.Limits.MaxSteps < 1 || *request.Limits.MaxSteps > engine.SimulationMaxSteps) {
		return fmt.Errorf("limits.maxSteps must be between 1 and %d", engine.SimulationMaxSteps)
	}
	if request.Limits.MaxVisitsPerNode != nil && (*request.Limits.MaxVisitsPerNode < 1 || *request.Limits.MaxVisitsPerNode > engine.SimulationMaxVisitsPerNode) {
		return fmt.Errorf("limits.maxVisitsPerNode must be between 1 and %d", engine.SimulationMaxVisitsPerNode)
	}
	return nil
}

func (request simulationRequest) maxSteps() int {
	if request.Limits == nil || request.Limits.MaxSteps == nil {
		return 0
	}
	return *request.Limits.MaxSteps
}

func (request simulationRequest) maxVisitsPerNode() int {
	if request.Limits == nil || request.Limits.MaxVisitsPerNode == nil {
		return 0
	}
	return *request.Limits.MaxVisitsPerNode
}
