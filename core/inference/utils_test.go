package inference

import "testing"

func TestInferenceHeadersIncludeReferenceContextHeaders(t *testing.T) {
	restore := SetContextHeadersProvider(func() map[string]string {
		return map[string]string{
			"X-LiveKit-Room-ID": "RM_test",
			"X-LiveKit-Job-ID":  "job_test",
		}
	})
	defer restore()

	headers := InferenceHeaders()
	if got := headers.Get("User-Agent"); got == "" {
		t.Fatal("User-Agent = empty, want LiveKit Agents header")
	}
	if got := headers.Get("X-LiveKit-Room-ID"); got != "RM_test" {
		t.Fatalf("X-LiveKit-Room-ID = %q, want RM_test", got)
	}
	if got := headers.Get("X-LiveKit-Job-ID"); got != "job_test" {
		t.Fatalf("X-LiveKit-Job-ID = %q, want job_test", got)
	}
}

func TestNewInferenceClientsUseReferenceGatewaySelection(t *testing.T) {
	t.Run("custom inference URL", func(t *testing.T) {
		t.Setenv("LIVEKIT_INFERENCE_URL", "https://inference.example.test/v2")
		t.Setenv("LIVEKIT_URL", "wss://project.staging.livekit.cloud")

		ttsProvider := NewTTS("cartesia/sonic-3", "key", "secret")
		sttProvider := NewSTT("deepgram/nova-3", "key", "secret")

		if ttsProvider.baseURL != "wss://inference.example.test/v2" {
			t.Fatalf("TTS baseURL = %q, want custom websocket inference URL", ttsProvider.baseURL)
		}
		if sttProvider.baseURL != "wss://inference.example.test/v2" {
			t.Fatalf("STT baseURL = %q, want custom websocket inference URL", sttProvider.baseURL)
		}
	})

	t.Run("staging LiveKit URL", func(t *testing.T) {
		t.Setenv("LIVEKIT_INFERENCE_URL", "")
		t.Setenv("LIVEKIT_URL", "wss://project.staging.livekit.cloud")

		ttsProvider := NewTTS("cartesia/sonic-3", "key", "secret")
		sttProvider := NewSTT("deepgram/nova-3", "key", "secret")

		if ttsProvider.baseURL != "wss://agent-gateway.staging.livekit.cloud/v1" {
			t.Fatalf("TTS baseURL = %q, want staging websocket inference URL", ttsProvider.baseURL)
		}
		if sttProvider.baseURL != "wss://agent-gateway.staging.livekit.cloud/v1" {
			t.Fatalf("STT baseURL = %q, want staging websocket inference URL", sttProvider.baseURL)
		}
	})
}
