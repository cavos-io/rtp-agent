package app

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/cavos-io/rtp-agent/adapter/cavos"
	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/llm"
	logutil "github.com/cavos-io/rtp-agent/library/logger"
)

func TestAdaptiveInterruptionConfigOptIn(t *testing.T) {
	t.Setenv("RTP_AGENT_INTERRUPTION_PROVIDER", "cavos")
	t.Setenv("RTP_AGENT_BARGEIN_GRPC_ADDR", "127.0.0.1:9090")
	t.Setenv("RTP_AGENT_BARGEIN_DUMP_DIR", t.TempDir())
	cfg := DefaultConfigFromEnv()
	opts, err := agentSessionOptionsFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	d, ok := opts.InterruptionDetector.(*cavos.AdaptiveInterruptionDetector)
	if !ok || d == nil || opts.InterruptionMode != agent.InterruptionModeAdaptive {
		t.Fatalf("detector=%T mode=%s", opts.InterruptionDetector, opts.InterruptionMode)
	}
	expected := cavos.NewAdaptiveInterruptionDetector(cavos.WithAdaptiveInterruptionAddr("127.0.0.1:9090"), cavos.WithAdaptiveInterruptionDumpDir(cfg.BargeInDumpDir))
	if !reflect.DeepEqual(d, expected) {
		t.Fatal("env address/dump directory did not reach detector")
	}
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, opts)
	if session.Options.AECWarmupDuration != 0 || !session.Options.AECWarmupDurationSet || session.Options.HalfDuplexSTTWhileSpeaking || !session.Options.AllowInterruptions {
		t.Fatalf("invalid effective session options: %+v", session.Options)
	}
	// No connection is opened until the session actually starts its stream.
	if d.Model() != "bargein-v50" || d.Provider() != "cavos" {
		t.Fatal("missing model attribution")
	}
}

func TestAdaptiveInterruptionConfigDefaultAndInvalidProvider(t *testing.T) {
	t.Setenv("RTP_AGENT_INTERRUPTION_PROVIDER", "")
	opts, err := agentSessionOptionsFromConfig(DefaultConfigFromEnv())
	if err != nil {
		t.Fatal(err)
	}
	if opts.InterruptionDetector != nil || opts.InterruptionMode == agent.InterruptionModeAdaptive || opts.AECWarmupDurationSet {
		t.Fatal("default behavior changed")
	}
	t.Setenv("RTP_AGENT_INTERRUPTION_PROVIDER", "misspelled")
	if _, err = agentSessionOptionsFromConfig(DefaultConfigFromEnv()); err == nil {
		t.Fatal("unknown provider silently accepted")
	}
}

func TestAdaptivePlaybackLogsCoreOutcomeOnlyWhenEnabled(t *testing.T) {
	for _, provider := range []string{"", "cavos"} {
		t.Run(provider, func(t *testing.T) {
			previous := logutil.Logger
			defer logutil.SetLogger(previous)
			recorder := &appRecordingLogger{}
			application, err := NewApp(AppConfig{InterruptionProvider: provider, Logger: recorder})
			if err != nil {
				t.Fatal(err)
			}
			defer application.Close(context.Background())
			for _, interrupted := range []bool{false, true} {
				application.Session.EmitConversationItemAdded(&llm.ChatMessage{ID: fmt.Sprint(interrupted), Role: llm.ChatRoleAssistant, Interrupted: interrupted})
			}
			application.Session.EmitConversationItemAdded(&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser})
			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			outcomes := map[string]bool{}
			for _, entry := range recorder.entries {
				if entry.msg == "bargein.playback_result" {
					fields := map[string]any{}
					for i := 0; i < len(entry.kv); i += 2 {
						fields[entry.kv[i].(string)] = entry.kv[i+1]
					}
					outcomes[fields["message_id"].(string)] = fields["interrupted"].(bool)
				}
			}
			if provider == "" {
				if len(outcomes) != 0 {
					t.Fatal("default logging changed")
				}
				return
			}
			if len(outcomes) != 2 || outcomes["false"] || !outcomes["true"] {
				t.Fatalf("missing actual playback outcomes: %v", outcomes)
			}
		})
	}
}
