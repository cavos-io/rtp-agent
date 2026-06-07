package workflows

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/beta"
)

func TestGetAddressTaskRecordsAddressWithoutConfirmation(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"123 Main St","unit_number":"","locality":"Springfield IL 62701","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "Address captured and task completed." {
		t.Fatalf("Execute() output = %q, want completion message", out)
	}

	select {
	case result := <-task.Result:
		if result.Address != "123 Main St Springfield IL 62701 United States" {
			t.Fatalf("Address = %q, want normalized joined address", result.Address)
		}
	default:
		t.Fatal("task did not complete after address update")
	}
}

func TestGetAddressTaskSkipsWhitespaceOnlyUnitNumber(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	_, err := tool.Execute(context.Background(), `{"street_address":"123 Main St","unit_number":"   ","locality":"Springfield IL 62701","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	select {
	case result := <-task.Result:
		if result.Address != "123 Main St Springfield IL 62701 United States" {
			t.Fatalf("Address = %q, want whitespace-only unit omitted", result.Address)
		}
	default:
		t.Fatal("task did not complete after address update")
	}
}

func TestGetAddressTaskInjectsConfirmToolAfterUpdate(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{})
	if len(task.Agent.Tools) != 2 {
		t.Fatalf("initial tools = %d, want update/decline before address is captured", len(task.Agent.Tools))
	}

	update := &updateAddressTool{task: task}
	out, err := update.Execute(context.Background(), `{"street_address":"123 Main St","unit_number":"Apt 4","locality":"Springfield IL 62701","country":"United States"}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if out == "" {
		t.Fatal("update Execute() output is empty, want confirmation guidance")
	}
	if !strings.Contains(out, `Repeat the address field by field: ["123 Main St" "Apt 4" "Springfield IL 62701" "United States"] if needed`) {
		t.Fatalf("update Execute() output = %q, want field-by-field address guidance", out)
	}
	if len(task.Agent.Tools) != 3 || task.Agent.Tools[2].Name() != "confirm_address" {
		t.Fatalf("tools = %#v, want confirm_address appended", task.Agent.Tools)
	}

	confirm := &confirmAddressTool{task: task, address: "123 Main St Apt 4 Springfield IL 62701 United States"}
	if _, err := confirm.Execute(context.Background(), `{}`); err != nil {
		t.Fatalf("confirm Execute() error = %v", err)
	}

	select {
	case result := <-task.Result:
		if result.Address != "123 Main St Apt 4 Springfield IL 62701 United States" {
			t.Fatalf("Address = %q, want captured address", result.Address)
		}
	default:
		t.Fatal("task did not complete after confirmation")
	}
}

func TestGetAddressTaskCanDisableDefaultConfirmation(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmation: false, RequireConfirmationSet: true})
	update := &updateAddressTool{task: task}

	out, err := update.Execute(context.Background(), `{"street_address":"123 Main St","unit_number":"","locality":"Springfield IL 62701","country":"United States"}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if out != "Address captured and task completed." {
		t.Fatalf("update Execute() output = %q, want completion message", out)
	}
}

func TestGetAddressTaskInstructionsOmitConfirmationWhenDisabled(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmation: false, RequireConfirmationSet: true})

	if strings.Contains(task.Instructions, "confirm_address") {
		t.Fatalf("Instructions = %q, want no confirm_address guidance when confirmation disabled", task.Instructions)
	}
}

func TestGetAddressTaskInstructionsUseReferenceToolGuidance(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{})

	for _, want := range []string{
		"Call `update_address` at the first opportunity whenever you form a new hypothesis about the address. (before asking any questions or providing any answers.)",
		"Always explicitly invoke a tool when applicable. Do not simulate tool usage, no real action is taken unless the tool is explicitly called.",
	} {
		if !strings.Contains(task.Instructions, want) {
			t.Fatalf("Instructions = %q, want reference guidance %q", task.Instructions, want)
		}
	}
}

func TestGetAddressTaskInstructionPartsCustomizePersonaAndExtra(t *testing.T) {
	customPersona := "You only collect shipping addresses for hardware orders."
	task := NewGetAddressTask(GetAddressOptions{
		Instructions: &beta.InstructionParts{
			Persona: &customPersona,
			Extra:   "Ask whether the destination is residential or commercial.",
		},
	})

	if !strings.Contains(task.Instructions, customPersona) {
		t.Fatalf("Instructions = %q, want custom persona", task.Instructions)
	}
	if strings.Contains(task.Instructions, "responsible solely for capturing an address") {
		t.Fatalf("Instructions = %q, want default persona replaced", task.Instructions)
	}
	if !strings.Contains(task.Instructions, "Ask whether the destination is residential or commercial.") {
		t.Fatalf("Instructions = %q, want extra instructions appended", task.Instructions)
	}
}

func TestGetAddressTaskInstructionPartsCanRemovePersona(t *testing.T) {
	emptyPersona := ""
	task := NewGetAddressTask(GetAddressOptions{
		Instructions: &beta.InstructionParts{Persona: &emptyPersona},
	})

	if strings.Contains(task.Instructions, "responsible solely for capturing an address") {
		t.Fatalf("Instructions = %q, want default persona removed", task.Instructions)
	}
	if !strings.Contains(task.Instructions, "Call `update_address` at the first opportunity") {
		t.Fatalf("Instructions = %q, want workflow guidance preserved", task.Instructions)
	}
}

func TestUpdateAddressToolParametersUseReferenceDescriptions(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{})
	tool := &updateAddressTool{task: task}

	properties, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want map", tool.Parameters()["properties"])
	}

	for field, want := range map[string]string{
		"street_address": "Dependent on country, may include fields like house number, street name, block, or district",
		"unit_number":    "The unit number, for example Floor 1 or Apartment 12. If there is no unit number, return ''",
		"locality":       "Dependent on country, may include fields like city, zip code, or province",
		"country":        "The country the user lives in spelled out fully",
	} {
		schema, ok := properties[field].(map[string]any)
		if !ok {
			t.Fatalf("properties[%s] = %#v, want map", field, properties[field])
		}
		if got := schema["description"]; got != want {
			t.Fatalf("properties[%s].description = %#v, want %q", field, got, want)
		}
	}
}

func TestGetAddressTaskOnEnterUsesReferencePrompt(t *testing.T) {
	want := "Ask the user to provide their address."
	if got := addressOnEnterPrompt(); got != want {
		t.Fatalf("addressOnEnterPrompt() = %q, want %q", got, want)
	}
}

func TestGetAddressTaskStaleConfirmationPromptsForUpdatedAddress(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{})
	session := agent.NewAgentSession(task, nil, agent.AgentSessionOptions{})
	session.Assistant = &fakeDtmfSessionAssistant{}
	speechEvents := session.SpeechCreatedEvents()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer session.Stop(context.Background())

	select {
	case <-speechEvents:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for address on-enter prompt")
	}

	update := &updateAddressTool{task: task}

	if _, err := update.Execute(context.Background(), `{"street_address":"123 Main St","unit_number":"","locality":"Springfield IL 62701","country":"United States"}`); err != nil {
		t.Fatalf("first update Execute() error = %v", err)
	}
	staleConfirm := &confirmAddressTool{task: task, address: "123 Main St Springfield IL 62701 United States"}

	if _, err := update.Execute(context.Background(), `{"street_address":"456 Oak Ave","unit_number":"","locality":"Springfield IL 62701","country":"United States"}`); err != nil {
		t.Fatalf("second update Execute() error = %v", err)
	}
	if _, err := staleConfirm.Execute(context.Background(), `{}`); err != nil {
		t.Fatalf("stale confirm Execute() error = %v, want nil after prompting for updated confirmation", err)
	}

	select {
	case ev := <-speechEvents:
		if ev.Source != "generate_reply" {
			t.Fatalf("SpeechCreated Source = %q, want generate_reply", ev.Source)
		}
		if ev.SpeechHandle == nil {
			t.Fatal("SpeechCreated SpeechHandle = nil, want stale confirmation reply handle")
		}
		want := addressStaleConfirmationPrompt()
		if ev.SpeechHandle.Generation.Instructions == nil {
			t.Fatal("stale confirmation instructions = nil, want changed-address prompt")
		}
		if got := ev.SpeechHandle.Generation.Instructions.AsModality("text").String(); got != want {
			t.Fatalf("stale confirmation instructions = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stale confirmation prompt")
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for stale confirmation", result)
	default:
	}
}

func TestDeclineAddressCaptureToolFailsWithReason(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &declineAddressCaptureTool{task: task}

	if _, err := tool.Execute(context.Background(), `{"reason":"user refused"}`); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	_, err := task.WaitAny(context.Background())
	if err == nil || err.Error() != "couldn't get the address: user refused" {
		t.Fatalf("WaitAny() error = %v, want decline reason", err)
	}
}
