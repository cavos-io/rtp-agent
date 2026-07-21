package nvidia

import (
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestRealtimeModelConstructorContract(t *testing.T) {
	var _ llm.RealtimeModel = (*RealtimeModel)(nil)
	t.Setenv(nvidiaPersonaplexURLEnv, "")
	model := NewRealtimeModel(WithNvidiaRealtimeVoice("voice"), WithNvidiaRealtimeSilenceThresholdMS(250))
	if model.voice != "voice" || model.silenceThresholdMS != 250 || model.baseURL != defaultNvidiaRealtimeBaseURL {
		t.Fatalf("NewRealtimeModel() did not apply defaults and options")
	}
}
