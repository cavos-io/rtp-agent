package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/conversation-worker/interface/worker/ipc"
	"github.com/livekit/protocol/livekit"
)

func TestParseConnectArgsUsesProvidedIdentity(t *testing.T) {
	args, err := parseConnectArgs([]string{"worker", "connect", "room-a", "agent-custom"})
	if err != nil {
		t.Fatalf("parseConnectArgs() error = %v", err)
	}

	if args.RoomName != "room-a" {
		t.Fatalf("RoomName = %q, want room-a", args.RoomName)
	}
	if args.ParticipantIdentity != "agent-custom" {
		t.Fatalf("ParticipantIdentity = %q, want agent-custom", args.ParticipantIdentity)
	}
}

func TestParseConnectArgsDefaultsAgentIdentity(t *testing.T) {
	args, err := parseConnectArgs([]string{"worker", "connect", "room-a"})
	if err != nil {
		t.Fatalf("parseConnectArgs() error = %v", err)
	}

	if !strings.HasPrefix(args.ParticipantIdentity, "agent-") {
		t.Fatalf("ParticipantIdentity = %q, want agent- prefix", args.ParticipantIdentity)
	}
	if args.ParticipantIdentity == "agent-" {
		t.Fatal("ParticipantIdentity is only the prefix")
	}
}

func TestParseConnectArgsRequiresRoom(t *testing.T) {
	_, err := parseConnectArgs([]string{"worker", "connect"})
	if err == nil {
		t.Fatal("parseConnectArgs() error = nil, want missing room error")
	}
}

func TestConsoleLocalJobArgsMatchReference(t *testing.T) {
	roomName, participantIdentity := consoleLocalJobArgs()
	if roomName != "console-room" {
		t.Fatalf("roomName = %q, want console-room", roomName)
	}
	if participantIdentity != "console" {
		t.Fatalf("participantIdentity = %q, want console", participantIdentity)
	}
}

func TestParseConsoleArgsDefaultsToAudioMode(t *testing.T) {
	args, err := parseConsoleArgs([]string{"worker", "console"})
	if err != nil {
		t.Fatalf("parseConsoleArgs() error = %v", err)
	}
	if args.Mode != ConsoleModeAudio {
		t.Fatalf("Mode = %q, want %q", args.Mode, ConsoleModeAudio)
	}
	if args.Record {
		t.Fatal("Record = true, want false")
	}
}

func TestParseConsoleArgsSupportsTextModeAndDevices(t *testing.T) {
	args, err := parseConsoleArgs([]string{
		"worker", "console",
		"--text",
		"--record",
		"--input-device", "mic-a",
		"--output-device", "speaker-a",
	})
	if err != nil {
		t.Fatalf("parseConsoleArgs() error = %v", err)
	}
	if args.Mode != ConsoleModeText {
		t.Fatalf("Mode = %q, want %q", args.Mode, ConsoleModeText)
	}
	if !args.Record {
		t.Fatal("Record = false, want true")
	}
	if args.InputDevice != "mic-a" {
		t.Fatalf("InputDevice = %q, want mic-a", args.InputDevice)
	}
	if args.OutputDevice != "speaker-a" {
		t.Fatalf("OutputDevice = %q, want speaker-a", args.OutputDevice)
	}
}

func TestParseConsoleArgsSupportsListDevices(t *testing.T) {
	args, err := parseConsoleArgs([]string{"worker", "console", "--list-devices"})
	if err != nil {
		t.Fatalf("parseConsoleArgs() error = %v", err)
	}
	if !args.ListDevices {
		t.Fatal("ListDevices = false, want true")
	}
}

func TestRunConsoleListDevicesReturnsBeforeStartingConsole(t *testing.T) {
	oldPrint := printConsoleAudioDevices
	defer func() { printConsoleAudioDevices = oldPrint }()
	calls := 0
	printConsoleAudioDevices = func() {
		calls++
	}

	runConsole(nil, []string{"worker", "console", "--list-devices"})

	if calls != 1 {
		t.Fatalf("printConsoleAudioDevices calls = %d, want 1", calls)
	}
}

func TestFormatConsoleAudioDevicesListsInputsOutputsAndDefaults(t *testing.T) {
	got := formatConsoleAudioDevices([]consoleAudioDevice{
		{Index: 0, Name: "Built-in Mic", MaxInputChannels: 1},
		{Index: 1, Name: "Built-in Speakers", MaxOutputChannels: 2},
		{Index: 2, Name: "USB Headset", MaxInputChannels: 1, MaxOutputChannels: 2},
	}, 0, 1)

	for _, want := range []string{
		"ID\tType\tName\tDefault",
		"0\tInput\tBuilt-in Mic\tyes",
		"1\tOutput\tBuilt-in Speakers\tyes",
		"2\tInput\tUSB Headset\t",
		"2\tOutput\tUSB Headset\t",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatConsoleAudioDevices() missing %q in:\n%s", want, got)
		}
	}
}

func TestReadConsoleInputPreservesSpaces(t *testing.T) {
	got, err := readConsoleInput(strings.NewReader("hello world from console\n"))
	if err != nil {
		t.Fatalf("readConsoleInput() error = %v", err)
	}
	if got != "hello world from console" {
		t.Fatalf("readConsoleInput() = %q, want full line", got)
	}
}

func TestConsoleInputIsEmptyTreatsWhitespaceAsEmpty(t *testing.T) {
	for _, input := range []string{"", " ", "\t", "  \t  "} {
		if !consoleInputIsEmpty(input) {
			t.Fatalf("consoleInputIsEmpty(%q) = false, want true", input)
		}
	}
	if consoleInputIsEmpty(" hello ") {
		t.Fatal("consoleInputIsEmpty(\" hello \") = true, want false")
	}
}

func TestCliArgsCarriesReferenceReloadState(t *testing.T) {
	args := CliArgs{
		LogLevel:    "debug",
		URL:         "wss://livekit.example",
		APIKey:      "api-key",
		APISecret:   "api-secret",
		DevMode:     true,
		Reload:      true,
		ReloadCount: 2,
	}

	if !args.Reload {
		t.Fatal("Reload = false, want true")
	}
	if args.ReloadCount != 2 {
		t.Fatalf("ReloadCount = %d, want 2", args.ReloadCount)
	}
}

func TestWatcherReloadStateIgnoresOverlappingReloads(t *testing.T) {
	watcher := NewWatcher(nil, nil)

	if !watcher.beginReload() {
		t.Fatal("first beginReload() = false, want true")
	}
	if watcher.beginReload() {
		t.Fatal("second beginReload() = true, want false while reload is active")
	}

	watcher.Reloaded()
	if !watcher.beginReload() {
		t.Fatal("beginReload() after Reloaded() = false, want true")
	}
}

func TestWatcherBeginReloadIncrementsCliReloadCount(t *testing.T) {
	args := &CliArgs{ReloadCount: 2}
	watcher := NewWatcher(nil, nil, args)

	if !watcher.beginReload() {
		t.Fatal("beginReload() = false, want true")
	}
	if args.ReloadCount != 3 {
		t.Fatalf("ReloadCount after beginReload() = %d, want 3", args.ReloadCount)
	}

	if watcher.beginReload() {
		t.Fatal("overlapping beginReload() = true, want false")
	}
	if args.ReloadCount != 3 {
		t.Fatalf("ReloadCount after overlapping beginReload() = %d, want unchanged 3", args.ReloadCount)
	}
}

func TestWatcherTriggerReloadKeepsReloadingUntilReloaded(t *testing.T) {
	args := &CliArgs{}
	calls := 0
	watcher := NewWatcher(nil, func() {
		calls++
	}, args)

	if !watcher.triggerReload() {
		t.Fatal("triggerReload() = false, want true")
	}
	if calls != 1 {
		t.Fatalf("onChange calls = %d, want 1", calls)
	}
	if args.ReloadCount != 1 {
		t.Fatalf("ReloadCount = %d, want 1", args.ReloadCount)
	}
	if watcher.triggerReload() {
		t.Fatal("overlapping triggerReload() = true, want false")
	}
	if calls != 1 {
		t.Fatalf("onChange calls after overlapping reload = %d, want 1", calls)
	}

	watcher.Reloaded()
	if !watcher.triggerReload() {
		t.Fatal("triggerReload() after Reloaded() = false, want true")
	}
	if calls != 2 {
		t.Fatalf("onChange calls after Reloaded() = %d, want 2", calls)
	}
}

func TestWatcherTriggerReloadRequestsActiveJobsBeforeRestart(t *testing.T) {
	args := &CliArgs{ReloadCount: 3}
	var output bytes.Buffer
	requestedBeforeRestart := false
	watcher := NewWatcher(nil, func() {
		msg, err := ipc.ReadMessage(&output)
		if err != nil {
			t.Fatalf("ReadMessage ActiveJobsRequest: %v", err)
		}
		requestedBeforeRestart = msg.Type == ipc.MessageTypeActiveJobsRequest
	}, args)
	watcher.reloadIPC = &output
	watcher.activeJobsTimeout = time.Nanosecond

	if !watcher.triggerReload() {
		t.Fatal("triggerReload() = false, want true")
	}
	if !requestedBeforeRestart {
		t.Fatal("triggerReload() did not request active jobs before restart")
	}
	if args.ReloadCount != 4 {
		t.Fatalf("ReloadCount = %d, want 4", args.ReloadCount)
	}
}

func TestWatcherTriggerReloadWaitsForActiveJobsBeforeRestart(t *testing.T) {
	args := &CliArgs{ReloadCount: 3}
	job := ipc.RunningJobInfo{Job: &livekit.Job{Id: "job-current"}, Token: "current-token"}
	restartStarted := make(chan struct{})
	var watcher *Watcher
	watcher = NewWatcher(nil, func() {
		close(restartStarted)
		resp := watcher.reloadJobsResponse()
		if len(resp.Jobs) != 1 || resp.Jobs[0].Job.GetId() != "job-current" {
			t.Fatalf("reloadJobsResponse().Jobs = %#v, want active job before restart", resp.Jobs)
		}
	}, args)

	peerReader, watcherWriter := io.Pipe()
	watcher.reloadIPC = watcherWriter
	requestRead := make(chan struct{})
	allowResponse := make(chan struct{})
	go func() {
		defer peerReader.Close()
		msg, err := ipc.ReadMessage(peerReader)
		if err != nil {
			t.Errorf("ReadMessage ActiveJobsRequest: %v", err)
			return
		}
		if msg.Type != ipc.MessageTypeActiveJobsRequest {
			t.Errorf("request Type = %q, want %q", msg.Type, ipc.MessageTypeActiveJobsRequest)
			return
		}
		close(requestRead)
		<-allowResponse
		watcher.recordActiveJobsResponse(ipc.ActiveJobsResponse{
			Jobs:        []ipc.RunningJobInfo{job},
			ReloadCount: 3,
		})
	}()

	done := make(chan bool, 1)
	go func() {
		done <- watcher.triggerReload()
	}()

	select {
	case <-requestRead:
	case <-time.After(time.Second):
		t.Fatal("triggerReload() did not request active jobs")
	}

	select {
	case <-restartStarted:
		t.Fatal("restart started before active jobs response")
	case <-time.After(25 * time.Millisecond):
	}

	close(allowResponse)
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("triggerReload() = false, want true")
		}
	case <-time.After(time.Second):
		t.Fatal("triggerReload() did not finish after active jobs response")
	}
	if args.ReloadCount != 4 {
		t.Fatalf("ReloadCount = %d, want 4", args.ReloadCount)
	}
}

func TestWatcherStoresActiveJobsForCurrentReload(t *testing.T) {
	args := &CliArgs{ReloadCount: 2}
	watcher := NewWatcher(nil, nil, args)

	staleJobs := []ipc.RunningJobInfo{{Job: &livekit.Job{Id: "job-stale"}, Token: "stale-token"}}
	if watcher.recordActiveJobsResponse(ipc.ActiveJobsResponse{Jobs: staleJobs, ReloadCount: 1}) {
		t.Fatal("recordActiveJobsResponse() accepted stale reload count")
	}
	if got := watcher.reloadJobsResponse(); len(got.Jobs) != 0 {
		t.Fatalf("reloadJobsResponse() stale jobs len = %d, want 0", len(got.Jobs))
	}

	currentJobs := []ipc.RunningJobInfo{{Job: &livekit.Job{Id: "job-current"}, Token: "current-token"}}
	if !watcher.recordActiveJobsResponse(ipc.ActiveJobsResponse{Jobs: currentJobs, ReloadCount: 2}) {
		t.Fatal("recordActiveJobsResponse() rejected current reload count")
	}

	resp := watcher.reloadJobsResponse()
	if resp.ReloadCount != 2 {
		t.Fatalf("ReloadJobsResponse.ReloadCount = %d, want 2", resp.ReloadCount)
	}
	if len(resp.Jobs) != 1 {
		t.Fatalf("ReloadJobsResponse.Jobs len = %d, want 1", len(resp.Jobs))
	}
	if resp.Jobs[0].Job.GetId() != "job-current" {
		t.Fatalf("ReloadJobsResponse.Jobs[0].Job.Id = %q, want job-current", resp.Jobs[0].Job.GetId())
	}

	resp.Jobs[0].Token = "mutated"
	if got := watcher.reloadJobsResponse(); got.Jobs[0].Token != "current-token" {
		t.Fatalf("mutating ReloadJobsResponse changed stored token to %q", got.Jobs[0].Token)
	}
}

func TestWatcherHandleReloadMessageRespondsWithActiveJobs(t *testing.T) {
	args := &CliArgs{ReloadCount: 4}
	watcher := NewWatcher(nil, nil, args)

	storedJobs := []ipc.RunningJobInfo{{Job: &livekit.Job{Id: "job-current"}, Token: "current-token"}}
	resp, ok := watcher.handleReloadMessage(&ipc.ActiveJobsResponse{
		Jobs:        storedJobs,
		ReloadCount: 4,
	})
	if !ok {
		t.Fatal("handleReloadMessage(ActiveJobsResponse) = false, want true")
	}
	if resp != nil {
		t.Fatalf("handleReloadMessage(ActiveJobsResponse) response = %#v, want nil", resp)
	}

	resp, ok = watcher.handleReloadMessage(&ipc.ReloadJobsRequest{})
	if !ok {
		t.Fatal("handleReloadMessage(ReloadJobsRequest) = false, want true")
	}
	reloadResp, ok := resp.(*ipc.ReloadJobsResponse)
	if !ok {
		t.Fatalf("handleReloadMessage(ReloadJobsRequest) response = %T, want *ReloadJobsResponse", resp)
	}
	if reloadResp.ReloadCount != 4 {
		t.Fatalf("ReloadJobsResponse.ReloadCount = %d, want 4", reloadResp.ReloadCount)
	}
	if len(reloadResp.Jobs) != 1 || reloadResp.Jobs[0].Job.GetId() != "job-current" {
		t.Fatalf("ReloadJobsResponse.Jobs = %#v, want current job", reloadResp.Jobs)
	}

	watcher.beginReload()
	if !watcher.reloading {
		t.Fatal("watcher.reloading = false, want active reload before Reloaded")
	}
	resp, ok = watcher.handleReloadMessage(&ipc.Reloaded{})
	if !ok {
		t.Fatal("handleReloadMessage(Reloaded) = false, want true")
	}
	if resp != nil {
		t.Fatalf("handleReloadMessage(Reloaded) response = %#v, want nil", resp)
	}
	if watcher.reloading {
		t.Fatal("watcher.reloading = true, want false after Reloaded")
	}

	if resp, ok := watcher.handleReloadMessage(&ipc.PingRequest{}); ok || resp != nil {
		t.Fatalf("handleReloadMessage(PingRequest) = (%#v, %v), want (nil, false)", resp, ok)
	}
}

func TestWatcherHandleReloadIPCMessageWritesResponse(t *testing.T) {
	args := &CliArgs{ReloadCount: 5}
	watcher := NewWatcher(nil, nil, args)
	watcher.recordActiveJobsResponse(ipc.ActiveJobsResponse{
		Jobs:        []ipc.RunningJobInfo{{Job: &livekit.Job{Id: "job-current"}, Token: "current-token"}},
		ReloadCount: 5,
	})

	req, err := ipc.NewMessage(&ipc.ReloadJobsRequest{})
	if err != nil {
		t.Fatalf("NewMessage(ReloadJobsRequest): %v", err)
	}
	var input bytes.Buffer
	if err := ipc.WriteMessage(&input, req); err != nil {
		t.Fatalf("WriteMessage request: %v", err)
	}
	var output bytes.Buffer

	handled, err := watcher.handleReloadIPCMessage(&input, &output)
	if err != nil {
		t.Fatalf("handleReloadIPCMessage() error = %v", err)
	}
	if !handled {
		t.Fatal("handleReloadIPCMessage() handled = false, want true")
	}

	msg, err := ipc.ReadMessage(&output)
	if err != nil {
		t.Fatalf("ReadMessage response: %v", err)
	}
	if msg.Type != ipc.MessageTypeReloadJobsResponse {
		t.Fatalf("response Type = %q, want %q", msg.Type, ipc.MessageTypeReloadJobsResponse)
	}
	payload, err := ipc.DecodePayload(msg)
	if err != nil {
		t.Fatalf("DecodePayload response: %v", err)
	}
	resp, ok := payload.(*ipc.ReloadJobsResponse)
	if !ok {
		t.Fatalf("response payload = %T, want *ReloadJobsResponse", payload)
	}
	if resp.ReloadCount != 5 {
		t.Fatalf("ReloadCount = %d, want 5", resp.ReloadCount)
	}
	if len(resp.Jobs) != 1 || resp.Jobs[0].Job.GetId() != "job-current" {
		t.Fatalf("Jobs = %#v, want current job", resp.Jobs)
	}
}

func TestWatcherRequestReloadJobsWritesRequestFrame(t *testing.T) {
	watcher := NewWatcher(nil, nil)
	var output bytes.Buffer

	if err := watcher.requestReloadJobs(&output); err != nil {
		t.Fatalf("requestReloadJobs() error = %v", err)
	}

	msg, err := ipc.ReadMessage(&output)
	if err != nil {
		t.Fatalf("ReadMessage request: %v", err)
	}
	if msg.Type != ipc.MessageTypeReloadJobsRequest {
		t.Fatalf("request Type = %q, want %q", msg.Type, ipc.MessageTypeReloadJobsRequest)
	}
	payload, err := ipc.DecodePayload(msg)
	if err != nil {
		t.Fatalf("DecodePayload request: %v", err)
	}
	if _, ok := payload.(*ipc.ReloadJobsRequest); !ok {
		t.Fatalf("request payload = %T, want *ReloadJobsRequest", payload)
	}
}

func TestWatcherProcessReloadIPCMessagesUntilEOF(t *testing.T) {
	args := &CliArgs{ReloadCount: 6}
	watcher := NewWatcher(nil, nil, args)

	var input bytes.Buffer
	activeResp, err := ipc.NewMessage(&ipc.ActiveJobsResponse{
		Jobs:        []ipc.RunningJobInfo{{Job: &livekit.Job{Id: "job-current"}, Token: "current-token"}},
		ReloadCount: 6,
	})
	if err != nil {
		t.Fatalf("NewMessage(ActiveJobsResponse): %v", err)
	}
	if err := ipc.WriteMessage(&input, activeResp); err != nil {
		t.Fatalf("WriteMessage active response: %v", err)
	}
	reloadReq, err := ipc.NewMessage(&ipc.ReloadJobsRequest{})
	if err != nil {
		t.Fatalf("NewMessage(ReloadJobsRequest): %v", err)
	}
	if err := ipc.WriteMessage(&input, reloadReq); err != nil {
		t.Fatalf("WriteMessage reload request: %v", err)
	}
	var output bytes.Buffer

	if err := watcher.processReloadIPCMessages(&input, &output); err != nil {
		t.Fatalf("processReloadIPCMessages() error = %v", err)
	}

	msg, err := ipc.ReadMessage(&output)
	if err != nil {
		t.Fatalf("ReadMessage response: %v", err)
	}
	if msg.Type != ipc.MessageTypeReloadJobsResponse {
		t.Fatalf("response Type = %q, want %q", msg.Type, ipc.MessageTypeReloadJobsResponse)
	}
	payload, err := ipc.DecodePayload(msg)
	if err != nil {
		t.Fatalf("DecodePayload response: %v", err)
	}
	resp, ok := payload.(*ipc.ReloadJobsResponse)
	if !ok {
		t.Fatalf("response payload = %T, want *ReloadJobsResponse", payload)
	}
	if resp.ReloadCount != 6 {
		t.Fatalf("ReloadCount = %d, want 6", resp.ReloadCount)
	}
	if len(resp.Jobs) != 1 || resp.Jobs[0].Job.GetId() != "job-current" {
		t.Fatalf("Jobs = %#v, want current job", resp.Jobs)
	}
	if _, err := ipc.ReadMessage(&output); err == nil {
		t.Fatal("second ReadMessage response error = nil, want EOF")
	}
}

func TestWatcherRunReloadIPCSessionRequestsThenProcessesMessages(t *testing.T) {
	args := &CliArgs{ReloadCount: 7}
	watcher := NewWatcher(nil, nil, args)
	if !watcher.beginReload() {
		t.Fatal("beginReload() = false, want active reload")
	}

	peerReader, watcherWriter := io.Pipe()
	watcherReader, peerWriter := io.Pipe()
	sessionErr := make(chan error, 1)
	go func() {
		sessionErr <- watcher.runReloadIPCSession(struct {
			io.Reader
			io.Writer
		}{
			Reader: watcherReader,
			Writer: watcherWriter,
		})
	}()

	msg, err := ipc.ReadMessage(peerReader)
	if err != nil {
		t.Fatalf("ReadMessage initial request: %v", err)
	}
	if msg.Type != ipc.MessageTypeReloadJobsRequest {
		t.Fatalf("initial request Type = %q, want %q", msg.Type, ipc.MessageTypeReloadJobsRequest)
	}
	if err := ipc.WriteMessage(peerWriter, mustIPCMessage(t, &ipc.Reloaded{})); err != nil {
		t.Fatalf("WriteMessage Reloaded: %v", err)
	}
	if err := peerWriter.Close(); err != nil {
		t.Fatalf("close peer writer: %v", err)
	}

	select {
	case err := <-sessionErr:
		if err != nil {
			t.Fatalf("runReloadIPCSession() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runReloadIPCSession() did not return after peer close")
	}
	if watcher.reloading {
		t.Fatal("watcher.reloading = true, want false after Reloaded")
	}
}

func TestWatcherRunReloadIPCSessionHandlesTriggerReloadRequests(t *testing.T) {
	args := &CliArgs{ReloadCount: 8}
	restarted := make(chan struct{})
	watcher := NewWatcher(nil, func() {
		close(restarted)
	}, args)
	watcher.activeJobsTimeout = time.Nanosecond

	peerReader, watcherWriter := io.Pipe()
	watcherReader, peerWriter := io.Pipe()
	sessionErr := make(chan error, 1)
	go func() {
		sessionErr <- watcher.runReloadIPCSession(struct {
			io.Reader
			io.Writer
		}{
			Reader: watcherReader,
			Writer: watcherWriter,
		})
	}()

	msg, err := ipc.ReadMessage(peerReader)
	if err != nil {
		t.Fatalf("ReadMessage initial request: %v", err)
	}
	if msg.Type != ipc.MessageTypeReloadJobsRequest {
		t.Fatalf("initial request Type = %q, want %q", msg.Type, ipc.MessageTypeReloadJobsRequest)
	}

	triggered := make(chan bool, 1)
	go func() {
		triggered <- watcher.triggerReload()
	}()

	msg = readIPCMessageWithin(t, peerReader, time.Second, "active jobs request")
	if msg.Type != ipc.MessageTypeActiveJobsRequest {
		t.Fatalf("trigger request Type = %q, want %q", msg.Type, ipc.MessageTypeActiveJobsRequest)
	}

	select {
	case ok := <-triggered:
		if !ok {
			t.Fatal("triggerReload() = false, want true")
		}
	case <-time.After(time.Second):
		t.Fatal("triggerReload() did not finish")
	}

	select {
	case <-restarted:
	case <-time.After(time.Second):
		t.Fatal("restart callback was not called")
	}

	if err := peerWriter.Close(); err != nil {
		t.Fatalf("close peer writer: %v", err)
	}
	select {
	case err := <-sessionErr:
		if err != nil {
			t.Fatalf("runReloadIPCSession() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runReloadIPCSession() did not return after peer close")
	}
}

func TestWatcherRunReloadIPCSessionClearsReloadingOnClose(t *testing.T) {
	watcher := NewWatcher(nil, nil)
	if !watcher.beginReload() {
		t.Fatal("beginReload() = false, want active reload")
	}

	peerReader, watcherWriter := io.Pipe()
	watcherReader, peerWriter := io.Pipe()
	sessionErr := make(chan error, 1)
	go func() {
		sessionErr <- watcher.runReloadIPCSession(struct {
			io.Reader
			io.Writer
		}{
			Reader: watcherReader,
			Writer: watcherWriter,
		})
	}()

	msg, err := ipc.ReadMessage(peerReader)
	if err != nil {
		t.Fatalf("ReadMessage initial request: %v", err)
	}
	if msg.Type != ipc.MessageTypeReloadJobsRequest {
		t.Fatalf("initial request Type = %q, want %q", msg.Type, ipc.MessageTypeReloadJobsRequest)
	}
	if err := peerWriter.Close(); err != nil {
		t.Fatalf("close peer writer: %v", err)
	}

	select {
	case err := <-sessionErr:
		if err != nil {
			t.Fatalf("runReloadIPCSession() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runReloadIPCSession() did not return after peer close")
	}
	if watcher.reloading {
		t.Fatal("watcher.reloading = true, want false after reload IPC close")
	}
}

func mustIPCMessage(t *testing.T, payload any) ipc.Message {
	t.Helper()
	msg, err := ipc.NewMessage(payload)
	if err != nil {
		t.Fatalf("NewMessage(%T): %v", payload, err)
	}
	return msg
}

func readIPCMessageWithin(t *testing.T, r io.Reader, timeout time.Duration, name string) ipc.Message {
	t.Helper()
	type result struct {
		msg ipc.Message
		err error
	}
	done := make(chan result, 1)
	go func() {
		msg, err := ipc.ReadMessage(r)
		done <- result{msg: msg, err: err}
	}()
	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("ReadMessage %s: %v", name, res.err)
		}
		return res.msg
	case <-time.After(timeout):
		t.Fatalf("ReadMessage %s timed out", name)
		return ipc.Message{}
	}
}
