package parser

import (
	"context"
	"errors"
	"strings"

	"github.com/flowverse/flowverse-api/internal/domain"
)

type Mock struct{}

func NewMock() *Mock { return &Mock{} }

func (m *Mock) Parse(_ context.Context, text string) (Result, error) {
	if strings.TrimSpace(text) == "" {
		return Result{}, errors.New("text is required")
	}
	input := domain.Port{ID: "input", Label: "Entrada"}
	output := domain.Port{ID: "output", Label: "Salida"}
	yes := domain.Port{ID: "approved", Label: "Sí"}
	no := domain.Port{ID: "rejected", Label: "No"}
	flow := domain.FlowDefinition{
		SchemaVersion: domain.SchemaVersion,
		Name:          "Flujo generado",
		Description:   "Previsualización determinista generada por el proveedor mock.",
		Variables:     []domain.VariableDefinition{},
		Layout:        domain.Layout{Mode: "directional"},
		Nodes: []domain.Node{
			{ID: "start", Type: domain.NodeTrigger, Label: "Inicio", Inputs: []domain.Port{}, Outputs: []domain.Port{output}, ActivationMode: domain.ActivationEach, Configuration: map[string]any{}, Position: domain.Position{X: -240}},
			{ID: "process", Type: domain.NodeProcess, Label: firstSentence(text), Inputs: []domain.Port{input}, Outputs: []domain.Port{output}, ActivationMode: domain.ActivationEach, Configuration: map[string]any{}, Position: domain.Position{}},
			{ID: "end", Type: domain.NodeEnd, Label: "Completado", Inputs: []domain.Port{input}, Outputs: []domain.Port{}, ActivationMode: domain.ActivationEach, Configuration: map[string]any{"result": "success"}, Position: domain.Position{X: 240}},
		},
		Edges: []domain.Edge{
			{ID: "start_process", Source: "start", Target: "process", SourcePort: "output", TargetPort: "input", Priority: 0, Default: false},
			{ID: "process_end", Source: "process", Target: "end", SourcePort: "output", TargetPort: "input", Priority: 0, Default: false},
		},
	}
	result := Result{Proposal: flow, Warnings: []string{"Proveedor mock: revisa la propuesta antes de guardarla."}, Ambiguities: []Ambiguity{}, Provider: "mock"}
	lower := strings.ToLower(text)
	if strings.Contains(lower, "pago") && (strings.Contains(lower, "si ") || strings.Contains(lower, "sí ")) {
		flow.Name = "Procesamiento de pedido"
		flow.Variables = []domain.VariableDefinition{{Path: "/payment/status", Type: "string", Required: true}}
		flow.Nodes = []domain.Node{
			{ID: "start", Type: domain.NodeTrigger, Label: "Pedido recibido", Inputs: []domain.Port{}, Outputs: []domain.Port{output}, ActivationMode: domain.ActivationEach, Configuration: map[string]any{}, Position: domain.Position{X: -360}},
			{ID: "validate_payment", Type: domain.NodeProcess, Label: "Validar pago", Inputs: []domain.Port{input}, Outputs: []domain.Port{output}, ActivationMode: domain.ActivationEach, Configuration: map[string]any{}, Position: domain.Position{X: -180}},
			{ID: "payment_approved", Type: domain.NodeDecision, Label: "¿Pago aprobado?", Inputs: []domain.Port{input}, Outputs: []domain.Port{yes, no}, ActivationMode: domain.ActivationEach, Configuration: map[string]any{"strategy": "first_match"}, Position: domain.Position{}},
			{ID: "prepare_order", Type: domain.NodeProcess, Label: "Preparar pedido", Inputs: []domain.Port{input}, Outputs: []domain.Port{output}, ActivationMode: domain.ActivationEach, Configuration: map[string]any{}, Position: domain.Position{X: 180, Y: 80}},
			{ID: "refund", Type: domain.NodeProcess, Label: "Devolver dinero", Inputs: []domain.Port{input}, Outputs: []domain.Port{output}, ActivationMode: domain.ActivationEach, Configuration: map[string]any{}, Position: domain.Position{X: 180, Y: -80}},
			{ID: "end", Type: domain.NodeEnd, Label: "Fin", Inputs: []domain.Port{input}, Outputs: []domain.Port{}, ActivationMode: domain.ActivationEach, Configuration: map[string]any{"result": "success"}, Position: domain.Position{X: 360}},
		}
		flow.Edges = []domain.Edge{
			{ID: "start_payment", Source: "start", Target: "validate_payment", SourcePort: "output", TargetPort: "input"},
			{ID: "payment_decision", Source: "validate_payment", Target: "payment_approved", SourcePort: "output", TargetPort: "input"},
			{ID: "payment_yes", Source: "payment_approved", Target: "prepare_order", SourcePort: "approved", TargetPort: "input", Priority: 1, Condition: &domain.Condition{Field: "/payment/status", Operator: "equals", Value: "approved"}},
			{ID: "payment_no", Source: "payment_approved", Target: "refund", SourcePort: "rejected", TargetPort: "input", Priority: 2, Default: true},
			{ID: "prepare_end", Source: "prepare_order", Target: "end", SourcePort: "output", TargetPort: "input"},
			{ID: "refund_end", Source: "refund", Target: "end", SourcePort: "output", TargetPort: "input"},
		}
		result.Proposal = flow
		result.Ambiguities = append(result.Ambiguities, Ambiguity{
			Code: "payment_status_source", Question: "¿Qué campo contiene el resultado del pago?",
			SuggestedResolution: "Usar /payment/status con el valor approved.",
		})
	}
	return result, nil
}

func firstSentence(text string) string {
	text = strings.TrimSpace(text)
	for _, separator := range []string{"\n", "."} {
		if index := strings.Index(text, separator); index > 0 {
			text = text[:index]
		}
	}
	runes := []rune(text)
	if len(runes) > 100 {
		runes = runes[:100]
	}
	return string(runes)
}
