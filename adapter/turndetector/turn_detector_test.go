package turndetector

import (
	"strings"
	"testing"
	"time"
)

func TestTurnDetectorPluginMetadataMatchesReference(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.turn_detector" {
		t.Fatalf("PluginTitle = %q, want rtp-agent.plugins.turn_detector", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("PluginVersion = %q, want reference version", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.turn_detector" {
		t.Fatalf("PluginPackage = %q, want rtp-agent.plugins.turn_detector", PluginPackage)
	}
}

func TestTurnDetectorModelDefinitionsMatchReference(t *testing.T) {
	if HGModel != "livekit/turn-detector" {
		t.Fatalf("HGModel = %q, want livekit/turn-detector", HGModel)
	}
	if ONNXFilename != "model_q8.onnx" {
		t.Fatalf("ONNXFilename = %q, want model_q8.onnx", ONNXFilename)
	}
	if MaxHistoryTokens != 128 {
		t.Fatalf("MaxHistoryTokens = %d, want 128", MaxHistoryTokens)
	}
	if MaxHistoryTurns != 6 {
		t.Fatalf("MaxHistoryTurns = %d, want 6", MaxHistoryTurns)
	}

	english := NewEnglishModel()
	if english.Model() != ModelEnglish {
		t.Fatalf("english model = %q, want %q", english.Model(), ModelEnglish)
	}
	if english.Provider() != "livekit" {
		t.Fatalf("english provider = %q, want livekit", english.Provider())
	}
	if english.ModelRevision() != "v1.2.2-en" {
		t.Fatalf("english revision = %q, want v1.2.2-en", english.ModelRevision())
	}
	if english.InferenceMethod() != "lk_end_of_utterance_en" {
		t.Fatalf("english inference method = %q, want lk_end_of_utterance_en", english.InferenceMethod())
	}

	multilingual := NewMultilingualModel()
	if multilingual.Model() != ModelMultilingual {
		t.Fatalf("multilingual model = %q, want %q", multilingual.Model(), ModelMultilingual)
	}
	if multilingual.ModelRevision() != "v0.4.1-intl" {
		t.Fatalf("multilingual revision = %q, want v0.4.1-intl", multilingual.ModelRevision())
	}
	if multilingual.InferenceMethod() != "lk_end_of_utterance_multilingual" {
		t.Fatalf("multilingual inference method = %q, want lk_end_of_utterance_multilingual", multilingual.InferenceMethod())
	}
}

func TestTurnDetectorUnlikelyThresholdOverrideMatchesReference(t *testing.T) {
	threshold := 0.42
	model := NewEnglishModel(WithUnlikelyThreshold(threshold))

	got, ok := model.UnlikelyThreshold("en-US")
	if !ok {
		t.Fatal("UnlikelyThreshold(en-US) ok = false, want true")
	}
	if got != threshold {
		t.Fatalf("UnlikelyThreshold(en-US) = %v, want %v", got, threshold)
	}

	model = NewEnglishModel()
	if _, ok := model.UnlikelyThreshold("en-US"); ok {
		t.Fatal("UnlikelyThreshold(en-US) ok = true without language config or override, want false")
	}
}

func TestTurnDetectorRemoteInferenceURLMatchesReference(t *testing.T) {
	if got := RemoteInferenceURL(""); got != "" {
		t.Fatalf("RemoteInferenceURL(empty) = %q, want empty", got)
	}
	if got := RemoteInferenceURL("https://turn.example"); got != "https://turn.example/eot/multi" {
		t.Fatalf("RemoteInferenceURL() = %q, want reference suffix", got)
	}
	model := NewMultilingualModel(WithRemoteInferenceBaseURL("https://turn.example"))
	if got := model.RemoteInferenceURL(); got != "https://turn.example/eot/multi" {
		t.Fatalf("Model.RemoteInferenceURL() = %q, want reference suffix", got)
	}
	if RemoteInferenceTimeout != 2*time.Second {
		t.Fatalf("RemoteInferenceTimeout = %v, want 2s", RemoteInferenceTimeout)
	}
}

func TestTurnDetectorPluginDownloadFilesIsExplicitlyUnsupported(t *testing.T) {
	err := (Plugin{}).DownloadFiles()
	if err == nil {
		t.Fatal("DownloadFiles() error = nil, want explicit unsupported error")
	}
	if !strings.Contains(err.Error(), "Hugging Face") || !strings.Contains(err.Error(), "ONNX") {
		t.Fatalf("DownloadFiles() error = %q, want Hugging Face/ONNX unsupported detail", err.Error())
	}
}
