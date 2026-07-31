package contract_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/flowverse/flowverse-api/internal/domain"
)

// sharedSchemaPath points at the single source of truth shared with the web
// client. The Go validator and the browser validator must agree on the wire
// format, so this suite asserts the marshalled payload against that schema
// instead of against a second copy of the rules.
const sharedSchemaPath = "../../../../packages/contracts/schemas/flow-definition.schema.json"

type jsonSchema struct {
	Required []string              `json:"required"`
	Defs     map[string]jsonSchema `json:"$defs"`
}

// Los límites solo se leen en la raíz: dentro de `$defs` hay subesquemas
// booleanos que no encajan en esta forma.
type schemaLimits struct {
	Properties map[string]struct {
		MaxItems int `json:"maxItems"`
	} `json:"properties"`
}

func loadSchemaLimits(t *testing.T) schemaLimits {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(sharedSchemaPath))
	if err != nil {
		t.Fatalf("read shared contract: %v", err)
	}
	var limits schemaLimits
	if err := json.Unmarshal(raw, &limits); err != nil {
		t.Fatalf("decode shared contract limits: %v", err)
	}
	return limits
}

func loadSharedSchema(t *testing.T) jsonSchema {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(sharedSchemaPath))
	if err != nil {
		t.Fatalf("read shared contract: %v", err)
	}
	var schema jsonSchema
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("decode shared contract: %v", err)
	}
	return schema
}

// defaultValuedFlow exercises the zero values the editor produces most often:
// a single edge with priority 0 and nodes without optional metadata.
func defaultValuedFlow() domain.FlowDefinition {
	return domain.FlowDefinition{
		Name:   "Pedidos",
		Layout: domain.Layout{Mode: "directional"},
		Nodes: []domain.Node{
			{
				ID: "start", Type: domain.NodeTrigger, Label: "Inicio",
				Outputs:  []domain.Port{{ID: "output", Label: "Salida"}},
				Position: domain.Position{X: -120},
			},
			{
				ID: "end", Type: domain.NodeEnd, Label: "Fin",
				Inputs:        []domain.Port{{ID: "input", Label: "Entrada"}},
				Configuration: map[string]any{"result": "success"},
				Position:      domain.Position{X: 120},
			},
		},
		Edges: []domain.Edge{{
			ID: "start_end", Source: "start", Target: "end",
			SourcePort: "output", TargetPort: "input",
			Priority: 0, Default: false,
		}},
	}.Normalize()
}

func marshalToMap(t *testing.T, value any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return decoded
}

func assertRequiredPresent(t *testing.T, where string, required []string, payload map[string]any) {
	t.Helper()
	for _, property := range required {
		if _, present := payload[property]; !present {
			t.Errorf("%s omits required property %q from the wire format", where, property)
		}
	}
}

func collection(t *testing.T, payload map[string]any, key string) []map[string]any {
	t.Helper()
	values, ok := payload[key].([]any)
	if !ok {
		t.Fatalf("payload %q is not an array", key)
	}
	items := make([]map[string]any, 0, len(values))
	for _, value := range values {
		item, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("entry of %q is not an object", key)
		}
		items = append(items, item)
	}
	return items
}

func TestMarshalledFlowKeepsEveryRequiredProperty(t *testing.T) {
	schema := loadSharedSchema(t)
	payload := marshalToMap(t, defaultValuedFlow())

	assertRequiredPresent(t, "flow", schema.Required, payload)

	for index, node := range collection(t, payload, "nodes") {
		assertRequiredPresent(t, "node "+string(rune('0'+index)), schema.Defs["node"].Required, node)
		position, ok := node["position"].(map[string]any)
		if !ok {
			t.Fatalf("node position is not an object")
		}
		assertRequiredPresent(t, "node position", schema.Defs["position"].Required, position)
	}

	for index, edge := range collection(t, payload, "edges") {
		assertRequiredPresent(t, "edge "+string(rune('0'+index)), schema.Defs["edge"].Required, edge)
	}
}

// El validador de Go y el del navegador deben coincidir en el tamaño máximo del
// grafo. Si solo se sube uno de los dos, la API acepta flujos que el editor
// rechaza —o al revés— y el usuario se queda sin poder abrir lo que guardó.
func TestGraphLimitsMatchTheSharedSchema(t *testing.T) {
	limits := loadSchemaLimits(t)

	if limits.Properties["nodes"].MaxItems != domain.MaxNodes {
		t.Errorf("nodes: el esquema permite %d y el dominio %d",
			limits.Properties["nodes"].MaxItems, domain.MaxNodes)
	}
	if limits.Properties["edges"].MaxItems != domain.MaxEdges {
		t.Errorf("edges: el esquema permite %d y el dominio %d",
			limits.Properties["edges"].MaxItems, domain.MaxEdges)
	}
}
