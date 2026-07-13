package workflows

import (
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
)

func TestWorkflowTasksPreserveReferenceVoiceAgentOptions(t *testing.T) {
	mode := agent.TurnDetectionModeManual
	allowInterruptions := false
	opts := AgentOptions{
		TurnDetection:      &mode,
		AllowInterruptions: &allowInterruptions,
	}

	tests := []struct {
		name string
		task func(t *testing.T) *agent.Agent
	}{
		{
			name: "phone",
			task: func(t *testing.T) *agent.Agent {
				return &NewGetPhoneNumberTask(GetPhoneNumberOptions{AgentOptions: opts}).Agent
			},
		},
		{
			name: "email",
			task: func(t *testing.T) *agent.Agent {
				return &NewGetEmailTask(GetEmailOptions{AgentOptions: opts}).Agent
			},
		},
		{
			name: "address",
			task: func(t *testing.T) *agent.Agent {
				return &NewGetAddressTask(GetAddressOptions{AgentOptions: opts}).Agent
			},
		},
		{
			name: "name",
			task: func(t *testing.T) *agent.Agent {
				return &NewGetNameTask(GetNameOptions{AgentOptions: opts, FirstName: true}).Agent
			},
		},
		{
			name: "dob",
			task: func(t *testing.T) *agent.Agent {
				return &NewGetDOBTask(GetDOBOptions{AgentOptions: opts}).Agent
			},
		},
		{
			name: "credit_card",
			task: func(t *testing.T) *agent.Agent {
				return &NewGetCreditCardTaskWithOptions(GetCreditCardOptions{AgentOptions: opts}).Agent
			},
		},
		{
			name: "warm_transfer",
			task: func(t *testing.T) *agent.Agent {
				task, err := NewWarmTransferTaskWithOptions(WarmTransferOptions{
					AgentOptions: opts,
					TargetPhone:  "+15551234567",
					TrunkID:      "trunk",
				})
				if err != nil {
					t.Fatalf("NewWarmTransferTaskWithOptions() error = %v", err)
				}
				return &task.Agent
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.task(t)
			if got.TurnDetection != agent.TurnDetectionModeManual {
				t.Fatalf("TurnDetection = %q, want %q", got.TurnDetection, agent.TurnDetectionModeManual)
			}
			if !got.AllowInterruptionsSet {
				t.Fatal("AllowInterruptionsSet = false, want explicit reference allow_interruptions option preserved")
			}
			if got.AllowInterruptions {
				t.Fatal("AllowInterruptions = true, want false")
			}
		})
	}
}
