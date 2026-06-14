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

func TestSDKClientImplementationRequiresPCM16InboundAudio(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"frame.Type != agoraservice.AudioFrameTypePCM16",
		"frame.BytesPerSample != 2",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sdk.go missing %q", want)
		}
	}
}

func TestSDKClientImplementationRegistersLocalUserObserver(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"RegisterLocalUserObserver",
		"OnUserAudioTrackSubscribed",
		"OnUserAudioTrackStateChanged",
		"agora SDK register local user observer failed",
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
		"os.MkdirAll(runtimeDir",
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
		"if err := c.waitConnected",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sdk.go missing %q", want)
		}
	}
}

func TestSDKClientImplementationChecksConnectionObserverRegistration(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"if ret := connection.RegisterObserver",
		"agora SDK register connection observer failed",
		"connection.Release()",
		"releaseSDKService()",
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

func TestSDKClientImplementationReleasesWaitConnectionOnlyWhenOwned(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	text := string(source)
	if !strings.Contains(text, "func (c *sdkChannelClient) releaseActiveConnection") {
		t.Fatal("sdk.go missing releaseActiveConnection ownership helper")
	}
	waitIndex := strings.Index(text, "func (c *sdkChannelClient) waitConnected")
	if waitIndex < 0 {
		t.Fatal("sdk.go missing waitConnected")
	}
	waitBody := text[waitIndex:]
	if nextFunc := strings.Index(waitBody[len("func "):], "\nfunc "); nextFunc >= 0 {
		waitBody = waitBody[:len("func ")+nextFunc]
	}
	if strings.Contains(waitBody, "connection.Release()") {
		t.Fatal("waitConnected must not release the SDK connection without ownership")
	}
	if !strings.Contains(waitBody, "c.releaseActiveConnection(connection)") {
		t.Fatal("waitConnected must release through releaseActiveConnection")
	}
}

func TestSDKClientImplementationDoesNotClearJoiningForStaleWaitCleanup(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	text := string(source)
	helperIndex := strings.Index(text, "func (c *sdkChannelClient) releaseActiveConnection")
	if helperIndex < 0 {
		t.Fatal("sdk.go missing releaseActiveConnection")
	}
	helperBody := text[helperIndex:]
	if nextFunc := strings.Index(helperBody[len("func "):], "\nfunc "); nextFunc >= 0 {
		helperBody = helperBody[:len("func ")+nextFunc]
	}
	staleIndex := strings.Index(helperBody, "if c.connection != connection")
	ownerIndex := strings.LastIndex(helperBody, "c.joining = false")
	if staleIndex < 0 {
		t.Fatal("releaseActiveConnection missing stale connection branch")
	}
	if ownerIndex < 0 {
		t.Fatal("releaseActiveConnection missing owner joining reset")
	}
	if ownerIndex < staleIndex {
		t.Fatal("releaseActiveConnection must reset joining only after ownership is confirmed")
	}
	staleBranch := helperBody[staleIndex:ownerIndex]
	if strings.Contains(staleBranch, "c.joining = false") {
		t.Fatal("stale wait cleanup must not clear another in-progress join")
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

func TestSDKClientImplementationLeavesBeforeCheckingContext(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	text := string(source)
	leaveIndex := strings.Index(text, "func (c *sdkChannelClient) Leave")
	if leaveIndex < 0 {
		t.Fatal("sdk.go missing sdkChannelClient.Leave")
	}
	leaveBody := text[leaveIndex:]
	connectionIndex := strings.Index(leaveBody, "connection := c.connection")
	contextIndex := strings.Index(leaveBody, "case <-ctx.Done()")
	if connectionIndex < 0 {
		t.Fatal("sdk.go Leave missing connection cleanup")
	}
	if contextIndex >= 0 && contextIndex < connectionIndex {
		t.Fatal("sdk.go Leave must release the active connection before returning context cancellation")
	}
}

func TestSDKClientImplementationNormalizesNilContexts(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	text := string(source)
	for _, method := range []string{"Join", "Leave", "PublishPCM"} {
		funcIndex := strings.Index(text, "func (c *sdkChannelClient) "+method)
		if funcIndex < 0 {
			t.Fatalf("sdk.go missing sdkChannelClient.%s", method)
		}
		body := text[funcIndex:]
		if nextFunc := strings.Index(body[len("func "):], "\nfunc "); nextFunc >= 0 {
			body = body[:len("func ")+nextFunc]
		}
		if !strings.Contains(body, "ctx = normalizeContext(ctx)") {
			t.Fatalf("sdk.go %s must normalize nil contexts before using ctx.Done", method)
		}
	}
}

func TestSDKClientImplementationRejectsDuplicateJoin(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"if c.connection != nil",
		"agora SDK channel is already joined",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sdk.go missing %q", want)
		}
	}
}

func TestSDKClientImplementationRejectsConcurrentJoin(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"joining    bool",
		"if c.connection != nil || c.joining",
		"c.joining = true",
		"c.joining = false",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sdk.go missing %q", want)
		}
	}
}

func TestSDKClientImplementationPublishesAfterConnected(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	text := string(source)
	waitIndex := strings.Index(text, "c.waitConnected")
	publishIndex := strings.Index(text, "c.publishActiveAudio(connection)")
	if waitIndex < 0 {
		t.Fatal("sdk.go missing waitConnected call")
	}
	if publishIndex < 0 {
		t.Fatal("sdk.go missing publishActiveAudio call")
	}
	if waitIndex > publishIndex {
		t.Fatal("sdk.go must wait for Agora connected event before publishing audio")
	}
}

func TestSDKClientImplementationReleasesPublishFailureOnlyWhenOwned(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	text := string(source)
	joinIndex := strings.Index(text, "func (c *sdkChannelClient) Join")
	if joinIndex < 0 {
		t.Fatal("sdk.go missing sdkChannelClient.Join")
	}
	joinBody := text[joinIndex:]
	if nextFunc := strings.Index(joinBody[len("func "):], "\nfunc "); nextFunc >= 0 {
		joinBody = joinBody[:len("func ")+nextFunc]
	}
	publishIndex := strings.Index(joinBody, "c.publishActiveAudio(connection)")
	if publishIndex < 0 {
		t.Fatal("Join missing publishActiveAudio call")
	}
	publishFailureBody := joinBody[publishIndex:]
	publishFailureIndex := strings.Index(publishFailureBody, "agora SDK publish audio failed")
	if publishFailureIndex < 0 {
		t.Fatal("Join missing publish-audio failure branch")
	}
	publishFailureBody = publishFailureBody[:publishFailureIndex]
	if strings.Contains(publishFailureBody, "connection.Release()") {
		t.Fatal("publish-audio failure must not release the SDK connection without ownership")
	}
	if !strings.Contains(publishFailureBody, "c.releaseActiveConnection(connection)") {
		t.Fatal("publish-audio failure must release through releaseActiveConnection")
	}
}

func TestSDKClientImplementationSerializesStartupPublishWithLeave(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	text := string(source)
	if !strings.Contains(text, "func (c *sdkChannelClient) publishActiveAudio") {
		t.Fatal("sdk.go missing publishActiveAudio helper")
	}
	joinIndex := strings.Index(text, "func (c *sdkChannelClient) Join")
	if joinIndex < 0 {
		t.Fatal("sdk.go missing sdkChannelClient.Join")
	}
	joinBody := text[joinIndex:]
	if nextFunc := strings.Index(joinBody[len("func "):], "\nfunc "); nextFunc >= 0 {
		joinBody = joinBody[:len("func ")+nextFunc]
	}
	if !strings.Contains(joinBody, "c.publishActiveAudio(connection)") {
		t.Fatal("Join must publish startup audio through publishActiveAudio")
	}
	helperIndex := strings.Index(text, "func (c *sdkChannelClient) publishActiveAudio")
	helperBody := text[helperIndex:]
	if nextFunc := strings.Index(helperBody[len("func "):], "\nfunc "); nextFunc >= 0 {
		helperBody = helperBody[:len("func ")+nextFunc]
	}
	lockIndex := strings.Index(helperBody, "c.mu.Lock()")
	publishIndex := strings.Index(helperBody, "connection.PublishAudio()")
	deferUnlockIndex := strings.Index(helperBody, "defer c.mu.Unlock()")
	if lockIndex < 0 || publishIndex < 0 {
		t.Fatal("publishActiveAudio must lock before PublishAudio")
	}
	if lockIndex > publishIndex {
		t.Fatal("publishActiveAudio must lock before PublishAudio")
	}
	if deferUnlockIndex < 0 {
		t.Fatal("publishActiveAudio must hold the lock through PublishAudio")
	}
	if !strings.Contains(helperBody, "if c.connection != connection") {
		t.Fatal("publishActiveAudio must verify active connection ownership")
	}
}

func TestSDKClientImplementationSerializesPublishPCMWithLeave(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	text := string(source)
	publishIndex := strings.Index(text, "func (c *sdkChannelClient) PublishPCM")
	if publishIndex < 0 {
		t.Fatal("sdk.go missing sdkChannelClient.PublishPCM")
	}
	publishBody := text[publishIndex:]
	if nextFunc := strings.Index(publishBody[len("func "):], "\nfunc "); nextFunc >= 0 {
		publishBody = publishBody[:len("func ")+nextFunc]
	}
	lockIndex := strings.Index(publishBody, "c.mu.Lock()")
	pushIndex := strings.Index(publishBody, "PushAudioPcmData")
	unlockIndex := strings.Index(publishBody, "c.mu.Unlock()")
	deferUnlockIndex := strings.Index(publishBody, "defer c.mu.Unlock()")
	if lockIndex < 0 || pushIndex < 0 {
		t.Fatal("PublishPCM must lock before pushing PCM to the SDK connection")
	}
	if unlockIndex >= 0 && unlockIndex < pushIndex && !strings.Contains(publishBody[unlockIndex-6:unlockIndex], "defer ") {
		t.Fatal("PublishPCM must not unlock before PushAudioPcmData returns")
	}
	if deferUnlockIndex < 0 {
		t.Fatal("PublishPCM must defer unlock while using the SDK connection")
	}
}
