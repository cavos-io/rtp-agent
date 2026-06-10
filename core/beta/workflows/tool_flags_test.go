package workflows

import (
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestCaptureDeclineToolsIgnoreOnEnter(t *testing.T) {
	tests := []struct {
		name string
		tool string
		list []llm.Tool
	}{
		{
			name: "address",
			tool: "decline_address_capture",
			list: NewGetAddressTask(GetAddressOptions{}).Agent.Tools,
		},
		{
			name: "email",
			tool: "decline_email_capture",
			list: NewGetEmailTask(GetEmailOptions{}).Agent.Tools,
		},
		{
			name: "name",
			tool: "decline_name_capture",
			list: NewGetNameTask(GetNameOptions{FirstName: true}).Agent.Tools,
		},
		{
			name: "phone_number",
			tool: "decline_phone_number_capture",
			list: NewGetPhoneNumberTask(GetPhoneNumberOptions{}).Agent.Tools,
		},
		{
			name: "dob",
			tool: "decline_dob_capture",
			list: NewGetDOBTask(GetDOBOptions{}).Agent.Tools,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := findWorkflowTool(t, tt.list, tt.tool)
			if !llm.ToolHasFlag(tool, llm.ToolFlagIgnoreOnEnter) {
				t.Fatalf("%s ToolFlags missing ToolFlagIgnoreOnEnter", tt.tool)
			}
		})
	}
}

func TestWorkflowControlToolsIgnoreOnEnter(t *testing.T) {
	tests := []struct {
		name string
		tool llm.Tool
	}{
		{name: "decline_card_capture", tool: &declineCardCaptureTool{}},
		{name: "restart_card_collection", tool: &restartCardCollectionTool{}},
		{name: "connect_to_caller", tool: &connectToCallerTool{}},
		{name: "decline_transfer", tool: &declineTransferTool{}},
		{name: "voicemail_detected", tool: &voicemailDetectedTool{}},
		{name: "out_of_scope", tool: &outOfScopeTool{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !llm.ToolHasFlag(tt.tool, llm.ToolFlagIgnoreOnEnter) {
				t.Fatalf("%s ToolFlags missing ToolFlagIgnoreOnEnter", tt.name)
			}
		})
	}
}

func findWorkflowTool(t *testing.T, tools []llm.Tool, name string) llm.Tool {
	t.Helper()

	for _, tool := range tools {
		if tool != nil && tool.Name() == name {
			return tool
		}
	}
	t.Fatalf("missing workflow tool %q in %#v", name, tools)
	return nil
}
