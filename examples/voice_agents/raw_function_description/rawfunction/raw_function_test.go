package rawfunction

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestNewAgentMatchesReferenceInstructionsAndRawSchema(t *testing.T) {
	agent := NewAgent()

	if agent.Instructions != "You are a helpful assistant" {
		t.Fatalf("Instructions = %q, want reference assistant instructions", agent.Instructions)
	}
	if len(agent.Tools) != 1 {
		t.Fatalf("len(Tools) = %d, want one raw function tool", len(agent.Tools))
	}

	tool := agent.Tools[0]
	if tool.Name() != "open_gate" {
		t.Fatalf("tool.Name() = %q, want open_gate", tool.Name())
	}
	if tool.Description() != "Opens a specified gate from a predefined set of access points." {
		t.Fatalf("Description() = %q, want reference raw schema description", tool.Description())
	}

	params := tool.Parameters()
	properties, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want map", params["properties"])
	}
	gateID, ok := properties["gate_id"].(map[string]any)
	if !ok {
		t.Fatalf("gate_id schema = %#v, want map", properties["gate_id"])
	}
	if gateID["type"] != "string" {
		t.Fatalf("gate_id.type = %#v, want string", gateID["type"])
	}
	if !strings.Contains(gateID["description"].(string), "Identifier of the gate to open") {
		t.Fatalf("gate_id.description = %q, want reference guidance", gateID["description"])
	}

	wantEnum := []any{"main_entrance", "north_parking", "loading_dock", "side_gate", "service_entry"}
	if got := gateID["enum"]; !reflect.DeepEqual(got, wantEnum) {
		t.Fatalf("gate_id.enum = %#v, want %#v", got, wantEnum)
	}
	if got := params["required"]; !reflect.DeepEqual(got, []any{"gate_id"}) {
		t.Fatalf("required = %#v, want gate_id required", got)
	}
}

func TestOpenGateToolMatchesReferenceContract(t *testing.T) {
	tool := openGateTool{}

	out, err := tool.Execute(context.Background(), `{"gate_id":"loading_dock"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "Gate loading_dock opened successfully" {
		t.Fatalf("Execute() = %q, want reference success message", out)
	}
}
