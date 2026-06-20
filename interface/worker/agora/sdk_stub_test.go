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

func TestNewSDKDataPublisherReportsBuildTagRequirement(t *testing.T) {
	if agoraSDKBuild {
		return
	}
	publisher, err := NewSDKDataPublisher(Options{})
	if err == nil {
		t.Fatal("NewSDKDataPublisher() error = nil, want build-tag requirement")
	}
	if publisher != nil {
		t.Fatalf("NewSDKDataPublisher() publisher = %#v, want nil", publisher)
	}
	if !strings.Contains(err.Error(), "agora_sdk") {
		t.Fatalf("NewSDKDataPublisher() error = %v, want agora_sdk build tag mention", err)
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

func TestSDKDataPublisherImplementationUsesBuildTag(t *testing.T) {
	source, err := os.ReadFile("sdk_rtm.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk_rtm.go) error = %v", err)
	}
	text := string(source)
	if !strings.Contains(text, "//go:build agora_sdk") {
		t.Fatal("sdk_rtm.go missing agora_sdk build tag")
	}
	for _, want := range []string{
		"Agora-Golang-Server-SDK",
		"NewRtmClient",
		"OnMessageEvent",
		"Login",
		"Subscribe",
		"Publish",
		"RtmChannelTypeMESSAGE",
		"RtmMessageTypeSTRING",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sdk_rtm.go missing %q", want)
		}
	}
}

func TestSDKDataPublisherCloseUsesLifecycleHelper(t *testing.T) {
	source, err := os.ReadFile("sdk_rtm.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk_rtm.go) error = %v", err)
	}
	if !strings.Contains(string(source), "closeRTMClient") {
		t.Fatal("sdk_rtm.go Close must use closeRTMClient so logout and release still run after unsubscribe failure")
	}
}

func TestSDKDataPublisherSubscribesWithMessages(t *testing.T) {
	source, err := os.ReadFile("sdk_rtm.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk_rtm.go) error = %v", err)
	}
	text := string(source)
	subscribeIndex := strings.Index(text, "func subscribeRTMMessages")
	if subscribeIndex < 0 {
		t.Fatal("sdk_rtm.go missing explicit RTM message subscription helper")
	}
	subscribeBody := text[subscribeIndex:]
	if nextFunc := strings.Index(subscribeBody[len("func "):], "\nfunc "); nextFunc >= 0 {
		subscribeBody = subscribeBody[:len("func ")+nextFunc]
	}
	for _, want := range []string{
		"opts := agorartm.NewSubscribeOptions()",
		"opts.WithMessage = true",
		"client.Subscribe(channel, opts)",
	} {
		if !strings.Contains(subscribeBody, want) {
			t.Fatalf("subscribeRTMMessages missing %q", want)
		}
	}
}

func TestSDKDataPublisherIgnoresInboundMessagesAfterClose(t *testing.T) {
	source, err := os.ReadFile("sdk_rtm.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk_rtm.go) error = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"closed := p.closed",
		"if handler == nil || closed",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sdk_rtm.go missing %q", want)
		}
	}
}

func TestSDKDataPublisherCloseClearsMessageHandler(t *testing.T) {
	source, err := os.ReadFile("sdk_rtm.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk_rtm.go) error = %v", err)
	}
	text := string(source)
	closeIndex := strings.Index(text, "func (p *sdkDataPublisher) Close")
	if closeIndex < 0 {
		t.Fatal("sdk_rtm.go missing sdkDataPublisher.Close")
	}
	closeBody := text[closeIndex:]
	if nextFunc := strings.Index(closeBody[len("func "):], "\nfunc "); nextFunc >= 0 {
		closeBody = closeBody[:len("func ")+nextFunc]
	}
	closedIndex := strings.Index(closeBody, "p.closed = true")
	clearIndex := strings.Index(closeBody, "p.handler = nil")
	cleanupIndex := strings.Index(closeBody, "closeRTMClient")
	if closedIndex < 0 || clearIndex < 0 || cleanupIndex < 0 {
		t.Fatal("Close must mark closed, clear message handler, then close native RTM client")
	}
	if !(closedIndex < clearIndex && clearIndex < cleanupIndex) {
		t.Fatal("Close must clear message handler before native RTM cleanup starts")
	}
}

func TestSDKDataPublisherDropsForeignChannelMessages(t *testing.T) {
	source, err := os.ReadFile("sdk_rtm.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk_rtm.go) error = %v", err)
	}
	text := string(source)
	handlerIndex := strings.Index(text, "func (p *sdkDataPublisher) handleMessageEvent")
	if handlerIndex < 0 {
		t.Fatal("sdk_rtm.go missing sdkDataPublisher.handleMessageEvent")
	}
	handlerBody := text[handlerIndex:]
	if nextFunc := strings.Index(handlerBody[len("func "):], "\nfunc "); nextFunc >= 0 {
		handlerBody = handlerBody[:len("func ")+nextFunc]
	}
	if !strings.Contains(handlerBody, "!acceptChannel(p.channel, event.ChannelName)") {
		t.Fatal("handleMessageEvent must drop SDK messages from channels other than the subscribed channel")
	}
}

func TestSDKDataPublisherUsesNormalizedChannelFilter(t *testing.T) {
	source, err := os.ReadFile("sdk_rtm.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk_rtm.go) error = %v", err)
	}
	text := string(source)
	handlerIndex := strings.Index(text, "func (p *sdkDataPublisher) handleMessageEvent")
	if handlerIndex < 0 {
		t.Fatal("sdk_rtm.go missing sdkDataPublisher.handleMessageEvent")
	}
	handlerBody := text[handlerIndex:]
	if nextFunc := strings.Index(handlerBody[len("func "):], "\nfunc "); nextFunc >= 0 {
		handlerBody = handlerBody[:len("func ")+nextFunc]
	}
	if !strings.Contains(handlerBody, "if !acceptChannel(p.channel, event.ChannelName)") {
		t.Fatal("handleMessageEvent must use normalized channel filtering for RTM callbacks")
	}
}

func TestSDKDataPublisherCloseReleasesLockBeforeNativeCleanup(t *testing.T) {
	source, err := os.ReadFile("sdk_rtm.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk_rtm.go) error = %v", err)
	}
	text := string(source)
	closeIndex := strings.Index(text, "func (p *sdkDataPublisher) Close")
	if closeIndex < 0 {
		t.Fatal("sdk_rtm.go missing sdkDataPublisher.Close")
	}
	closeBody := text[closeIndex:]
	if nextFunc := strings.Index(closeBody[len("func "):], "\nfunc "); nextFunc >= 0 {
		closeBody = closeBody[:len("func ")+nextFunc]
	}
	for _, want := range []string{
		"client := p.client",
		"channel := p.channel",
		"p.mu.Unlock()",
		"closeRTMClient(sdkRTMLifecycleClient{client: client}, channel)",
	} {
		if !strings.Contains(closeBody, want) {
			t.Fatalf("Close missing %q", want)
		}
	}
	unlockIndex := strings.Index(closeBody, "p.mu.Unlock()")
	cleanupIndex := strings.Index(closeBody, "closeRTMClient")
	if unlockIndex < 0 || cleanupIndex < 0 || unlockIndex > cleanupIndex {
		t.Fatal("Close must release publisher lock before native RTM cleanup")
	}
	if strings.Contains(closeBody, "defer p.mu.Unlock()") {
		t.Fatal("Close must not defer unlock across native RTM cleanup")
	}
}

func TestSDKDataPublisherCloseWaitsForAcceptedCallbacks(t *testing.T) {
	source, err := os.ReadFile("sdk_rtm.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk_rtm.go) error = %v", err)
	}
	text := string(source)
	if !strings.Contains(text, "callbacks sync.WaitGroup") {
		t.Fatal("sdkDataPublisher must track accepted RTM message callbacks")
	}
	handlerIndex := strings.Index(text, "func (p *sdkDataPublisher) handleMessageEvent")
	closeIndex := strings.Index(text, "func (p *sdkDataPublisher) Close")
	if handlerIndex < 0 || closeIndex < 0 {
		t.Fatal("sdk_rtm.go missing handleMessageEvent or Close")
	}
	handlerBody := text[handlerIndex:closeIndex]
	for _, want := range []string{
		"p.callbacks.Add(1)",
		"defer p.callbacks.Done()",
	} {
		if !strings.Contains(handlerBody, want) {
			t.Fatalf("handleMessageEvent missing %q", want)
		}
	}
	closeBody := text[closeIndex:]
	if nextFunc := strings.Index(closeBody[len("func "):], "\nfunc "); nextFunc >= 0 {
		closeBody = closeBody[:len("func ")+nextFunc]
	}
	waitIndex := strings.Index(closeBody, "p.callbacks.Wait()")
	cleanupIndex := strings.Index(closeBody, "closeRTMClient")
	if waitIndex < 0 || cleanupIndex < 0 || waitIndex > cleanupIndex {
		t.Fatal("Close must wait for accepted RTM callbacks before native cleanup returns")
	}
}

func TestSDKDataPublisherRechecksPublishContextAfterLock(t *testing.T) {
	source, err := os.ReadFile("sdk_rtm.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk_rtm.go) error = %v", err)
	}
	text := string(source)
	publishIndex := strings.Index(text, "func (p *sdkDataPublisher) PublishData")
	if publishIndex < 0 {
		t.Fatal("sdk_rtm.go missing sdkDataPublisher.PublishData")
	}
	publishBody := text[publishIndex:]
	if nextFunc := strings.Index(publishBody[len("func "):], "\nfunc "); nextFunc >= 0 {
		publishBody = publishBody[:len("func ")+nextFunc]
	}
	if strings.Count(publishBody, "case <-ctx.Done():") < 2 {
		t.Fatal("sdkDataPublisher.PublishData must recheck context after acquiring the publish lock")
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

func TestSDKClientImplementationGuardsInboundAudioByActiveConnection(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	text := string(source)
	if !strings.Contains(text, "func (c *sdkChannelClient) forwardActiveAudioFrame") {
		t.Fatal("sdk.go missing active-connection guarded inbound audio helper")
	}
	helperIndex := strings.Index(text, "func (c *sdkChannelClient) forwardActiveAudioFrame")
	helperBody := text[helperIndex:]
	if nextFunc := strings.Index(helperBody[len("func "):], "\nfunc "); nextFunc >= 0 {
		helperBody = helperBody[:len("func ")+nextFunc]
	}
	for _, want := range []string{
		"c.mu.Lock()",
		"defer c.mu.Unlock()",
		"if c.connection != connection",
		"audioHandler(audioFrame)",
	} {
		if !strings.Contains(helperBody, want) {
			t.Fatalf("forwardActiveAudioFrame missing %q", want)
		}
	}
	if !strings.Contains(text, "c.forwardActiveAudioFrame(connection, audioHandler, userID, frame)") {
		t.Fatal("SDK inbound audio callback must pass Agora userID through forwardActiveAudioFrame")
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

func TestSDKClientImplementationValidatesInboundPCMBufferShape(t *testing.T) {
	source, err := os.ReadFile("audio_frame.go")
	if err != nil {
		t.Fatalf("ReadFile(audio_frame.go) error = %v", err)
	}
	text := string(source)
	helperIndex := strings.Index(text, "func pcm16AudioFrameToModel")
	if helperIndex < 0 {
		t.Fatal("audio_frame.go missing pcm16AudioFrameToModel")
	}
	helperBody := text[helperIndex:]
	if nextFunc := strings.Index(helperBody[len("func "):], "\nfunc "); nextFunc >= 0 {
		helperBody = helperBody[:len("func ")+nextFunc]
	}
	for _, want := range []string{
		"len(frame.Data)%bytesPerInterleavedSample != 0",
		"samplesPerChannel < 0",
		"len(frame.Data) != samplesPerChannel*bytesPerInterleavedSample",
	} {
		if !strings.Contains(helperBody, want) {
			t.Fatalf("pcm16AudioFrameToModel missing %q", want)
		}
	}
	sdkSource, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	if !strings.Contains(string(sdkSource), "pcm16AudioFrameToModel(pcm16AudioFrame{") {
		t.Fatal("sdkAudioFrameToModel must delegate native SDK frame validation to pcm16AudioFrameToModel")
	}
	if !strings.Contains(string(sdkSource), "UserID:            userID") {
		t.Fatal("sdkAudioFrameToModel must pass SDK callback userID into generic PCM conversion")
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

func TestSDKClientImplementationGuardsEventsByActiveConnection(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	text := string(source)
	if !strings.Contains(text, "func (c *sdkChannelClient) emitActiveSDKEvent") {
		t.Fatal("sdk.go missing active-connection guarded event helper")
	}
	helperIndex := strings.Index(text, "func (c *sdkChannelClient) emitActiveSDKEvent")
	helperBody := text[helperIndex:]
	if nextFunc := strings.Index(helperBody[len("func "):], "\nfunc "); nextFunc >= 0 {
		helperBody = helperBody[:len("func ")+nextFunc]
	}
	for _, want := range []string{
		"c.mu.Lock()",
		"defer c.mu.Unlock()",
		"if c.connection != connection",
		"emitSDKEvent(handler, event)",
	} {
		if !strings.Contains(helperBody, want) {
			t.Fatalf("emitActiveSDKEvent missing %q", want)
		}
	}
	for _, want := range []string{
		"c.emitActiveSDKEvent(connection, handler, event)",
		"c.emitActiveSDKEvent(connection, handler, Event{Kind: EventUserJoined",
		"c.emitActiveSDKEvent(connection, handler, Event{Kind: EventUserLeft",
		"c.emitActiveSDKEvent(connection, handler, Event{Kind: EventError",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("SDK callbacks missing %q", want)
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

func TestSDKClientImplementationResolvesJoinOptions(t *testing.T) {
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
	resolveIndex := strings.Index(joinBody, "ResolveJoinOptions(opts)")
	connectIndex := strings.Index(joinBody, "Connect(opts.Token, opts.Channel, uid, \"\")")
	if resolveIndex < 0 {
		t.Fatal("SDK Join must resolve Agora join options before native Connect")
	}
	if connectIndex < 0 {
		t.Fatal("SDK Join missing native Connect call")
	}
	if resolveIndex > connectIndex {
		t.Fatal("SDK Join must resolve Agora join options before native Connect")
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

func TestSDKClientImplementationTrimsRuntimeEnv(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		`strings.TrimSpace(os.Getenv("AGORA_SDK_DATA_DIR"))`,
		`strings.TrimSpace(os.Getenv("AGORA_JOIN_TIMEOUT"))`,
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
		"connectedCh := make(chan Event, 1)",
		"joinErrCh := make(chan error, 1)",
		"case connectedCh <- event",
		"connectedEvent, err := c.waitConnected",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sdk.go missing %q", want)
		}
	}
}

func TestSDKClientImplementationPrioritizesStartupErrorOverConnected(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	text := string(source)
	waitIndex := strings.Index(text, "func (c *sdkChannelClient) waitConnected")
	if waitIndex < 0 {
		t.Fatal("sdk.go missing waitConnected")
	}
	waitBody := text[waitIndex:]
	if nextFunc := strings.Index(waitBody[len("func "):], "\nfunc "); nextFunc >= 0 {
		waitBody = waitBody[:len("func ")+nextFunc]
	}
	preferIndex := strings.Index(waitBody, "err, ok := pendingJoinError(joinErrCh)")
	connectedIndex := strings.Index(waitBody, "case event := <-connectedCh")
	if preferIndex < 0 {
		t.Fatal("waitConnected must check pending join errors before waiting on connected events")
	}
	if connectedIndex < 0 {
		t.Fatal("waitConnected missing connected event case")
	}
	if preferIndex > connectedIndex {
		t.Fatal("waitConnected must prefer pending join errors over connected events")
	}
	if !strings.Contains(text, "func pendingJoinError(joinErrCh <-chan error)") {
		t.Fatal("sdk.go missing pendingJoinError helper")
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

func TestSDKClientImplementationChecksContextBeforeConnect(t *testing.T) {
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
	setupIndex := strings.Index(joinBody, "if ret := connection.RegisterObserver")
	connectIndex := strings.Index(joinBody, "connection.Connect(")
	if setupIndex < 0 || connectIndex < 0 {
		t.Fatal("Join missing observer setup or Connect call")
	}
	setupToConnect := joinBody[setupIndex:connectIndex]
	contextIndex := strings.LastIndex(setupToConnect, "case <-ctx.Done():")
	if contextIndex < 0 {
		t.Fatal("Join must recheck context cancellation after SDK observer setup and before Connect")
	}
	contextBranch := setupToConnect[contextIndex:]
	for _, want := range []string{"connection.Release()", "releaseSDKService()", "return ctx.Err()"} {
		if !strings.Contains(contextBranch, want) {
			t.Fatalf("pre-Connect cancellation branch missing %q", want)
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

func TestSDKClientImplementationHonorsPublishAudioDisabled(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"publishCfg.IsPublishAudio = PublishAudioEnabled(opts.PublishAudio)",
		"if PublishAudioEnabled(opts.PublishAudio)",
		"c.publishActiveAudio(connection)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sdk.go missing %q", want)
		}
	}
}

func TestSDKClientImplementationHonorsSubscribeAudioDisabled(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"AutoSubscribeAudio:            SubscribeAudioEnabled(opts.SubscribeAudio)",
		"if audioHandler != nil && SubscribeAudioEnabled(opts.SubscribeAudio)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sdk.go missing %q", want)
		}
	}
}

func TestSDKClientImplementationFiltersRemoteStreamID(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"if !acceptRemoteStream(opts.RemoteStreamID, uid)",
		"if !acceptRemoteStream(opts.RemoteStreamID, userID)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sdk.go missing %q", want)
		}
	}
}

func TestSDKClientImplementationFiltersAudioByChannel(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	text := string(source)
	callbackIndex := strings.Index(text, "OnPlaybackAudioFrameBeforeMixing")
	if callbackIndex < 0 {
		t.Fatal("sdk.go missing OnPlaybackAudioFrameBeforeMixing callback")
	}
	callbackBody := text[callbackIndex:]
	if nextCallback := strings.Index(callbackBody[len("OnPlaybackAudioFrameBeforeMixing"):], "\n\t\t\t},"); nextCallback >= 0 {
		callbackBody = callbackBody[:len("OnPlaybackAudioFrameBeforeMixing")+nextCallback]
	}
	channelFilterIndex := strings.Index(callbackBody, "if !acceptChannel(opts.Channel, channelID)")
	remoteFilterIndex := strings.Index(callbackBody, "if !acceptRemoteStream(opts.RemoteStreamID, userID)")
	forwardIndex := strings.Index(callbackBody, "c.forwardActiveAudioFrame(connection, audioHandler, userID, frame)")
	if channelFilterIndex < 0 || remoteFilterIndex < 0 || forwardIndex < 0 {
		t.Fatal("audio callback must filter channel and remote stream before forwarding audio")
	}
	if !(channelFilterIndex < remoteFilterIndex && remoteFilterIndex < forwardIndex) {
		t.Fatal("audio callback must drop foreign channels before remote stream filtering and audio forwarding")
	}
}

func TestSDKClientImplementationEmitsConnectedAfterPublishAudio(t *testing.T) {
	source, err := os.ReadFile("sdk.go")
	if err != nil {
		t.Fatalf("ReadFile(sdk.go) error = %v", err)
	}
	text := string(source)
	onConnectedIndex := strings.Index(text, "OnConnected: func")
	if onConnectedIndex < 0 {
		t.Fatal("sdk.go missing OnConnected callback")
	}
	onConnectedBody := text[onConnectedIndex:]
	if nextCallback := strings.Index(onConnectedBody[len("OnConnected: func"):], "\n\t\t},"); nextCallback >= 0 {
		onConnectedBody = onConnectedBody[:len("OnConnected: func")+nextCallback]
	}
	if strings.Contains(onConnectedBody, "emitSDKEvent(handler, event)") {
		t.Fatal("OnConnected must not emit connected before startup PublishAudio succeeds")
	}
	joinIndex := strings.Index(text, "func (c *sdkChannelClient) Join")
	if joinIndex < 0 {
		t.Fatal("sdk.go missing sdkChannelClient.Join")
	}
	joinBody := text[joinIndex:]
	if nextFunc := strings.Index(joinBody[len("func "):], "\nfunc "); nextFunc >= 0 {
		joinBody = joinBody[:len("func ")+nextFunc]
	}
	waitIndex := strings.Index(joinBody, "c.waitConnected")
	publishIndex := strings.Index(joinBody, "c.publishActiveAudio(connection)")
	emitIndex := strings.Index(joinBody, "emitSDKEvent(handler, connectedEvent)")
	if waitIndex < 0 || publishIndex < 0 || emitIndex < 0 {
		t.Fatal("Join must wait, publish startup audio, then emit connected event")
	}
	if !(waitIndex < publishIndex && publishIndex < emitIndex) {
		t.Fatal("Join must emit connected only after waitConnected and PublishAudio succeed")
	}
}

func TestSDKClientImplementationChecksStartupErrorBeforeConnectedEmit(t *testing.T) {
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
	errorCheckIndex := strings.LastIndex(joinBody, "err, ok := pendingJoinError(joinErrCh)")
	emitIndex := strings.Index(joinBody, "emitSDKEvent(handler, connectedEvent)")
	if publishIndex < 0 || errorCheckIndex < 0 || emitIndex < 0 {
		t.Fatal("Join must publish audio, recheck pending startup errors, then emit connected")
	}
	if !(publishIndex < errorCheckIndex && errorCheckIndex < emitIndex) {
		t.Fatal("Join must recheck pending startup errors after PublishAudio and before connected emit")
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

func TestSDKClientImplementationRechecksPublishPCMContextAfterLock(t *testing.T) {
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
	if strings.Count(publishBody, "case <-ctx.Done():") < 2 {
		t.Fatal("PublishPCM must recheck context after acquiring the SDK publish lock")
	}
}
