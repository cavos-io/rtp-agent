package agora

import (
	"os"
	"strings"
	"testing"
)

func TestNewSDKChannelClientReportsBuildTagRequirement(t *testing.T) {
	client, err := NewSDKChannelClient()
	if agoraSDKBuild {
		if err != nil {
			t.Fatalf("NewSDKChannelClient() error = %v, want nil with agora_sdk tag", err)
		}
		if client == nil {
			t.Fatal("NewSDKChannelClient() client = nil, want SDK client with agora_sdk tag")
		}
		return
	}
	if err == nil {
		t.Fatal("NewSDKChannelClient() error = nil, want build-tag requirement")
	}
	if client != nil {
		t.Fatalf("NewSDKChannelClient() client = %#v, want nil", client)
	}
	if !strings.Contains(err.Error(), "agora_sdk") {
		t.Fatalf("NewSDKChannelClient() error = %v, want agora_sdk build tag mention", err)
	}
}

func TestSDKClientImplementationUsesBuildTag(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	if !strings.Contains(string(source), "//go:build agora_sdk") {
		t.Fatal("sdk.go missing agora_sdk build tag")
	}
	if !strings.Contains(string(source), "Agora-Golang-Server-SDK") {
		t.Fatal("sdk.go does not reference the Agora Golang Server SDK")
	}
}

func TestSDKClientImplementationRegistersInboundAudioObserver(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"SetPlaybackAudioFrameBeforeMixingParameters(1, 16000)",
		"RegisterAudioFrameObserver",
		"OnPlaybackAudioFrameBeforeMixing",
		"audioHandler(audioFrame)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sdk.go missing %q", want)
		}
	}
}

func TestSDKClientImplementationUsesCurrentConnectSignature(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	if !strings.Contains(string(source), `Connect(opts.Token, opts.Channel, uid, "")`) {
		t.Fatal("sdk.go must call RtcConnection.Connect with token, channel, uid, and info arguments")
	}
}

func TestSDKClientImplementationUsesVoidReleaseSignature(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	if strings.Contains(string(source), "ret := connection.Release()") {
		t.Fatal("sdk.go must not treat RtcConnection.Release as returning a status code")
	}
}

func TestSDKClientImplementationConfiguresRuntimeDirectories(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"AGORA_SDK_DATA_DIR",
		"cfg.LogPath",
		"cfg.ConfigDir",
		"cfg.DataDir",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sdk.go missing %q", want)
		}
	}
}

func TestSDKClientImplementationWaitsForConnectedEvent(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"connectedCh := make(chan struct{}, 1)",
		"joinErrCh := make(chan error, 1)",
		"case connectedCh <- struct{}{}",
		"return c.waitConnected",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sdk.go missing %q", want)
		}
	}
}

func TestSDKClientImplementationHasJoinTimeout(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"AGORA_JOIN_TIMEOUT",
		"defaultSDKJoinTimeout",
		"time.NewTimer",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sdk.go missing %q", want)
		}
	}
}

func TestSDKClientImplementationReleasesServiceOnLeave(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"agoraservice.Initialize(cfg)",
		"releaseSDKService()",
		"agoraservice.Release()",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sdk.go missing %q", want)
		}
	}
}
