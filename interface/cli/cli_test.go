package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/interface/worker"
	"github.com/cavos-io/rtp-agent/interface/worker/ipc"
	"github.com/cavos-io/rtp-agent/library/plugin"
	"github.com/livekit/protocol/livekit"
)

type fakePlugin struct {
	title       string
	version     string
	packageName string
	err         error
	calls       int
}

func (p *fakePlugin) Title() string   { return p.title }
func (p *fakePlugin) Version() string { return p.version }
func (p *fakePlugin) Package() string { return p.packageName }
func (p *fakePlugin) DownloadFiles() error {
	p.calls++
	return p.err
}

type fakeLiveKitRoomService struct {
	listNames    []string
	listRooms    []*livekit.Room
	createdRooms []string
}

type fakeProcessJobRunner struct {
	calls int
	info  ipc.RunningJobInfo
	err   error
}

func (r *fakeProcessJobRunner) ExecuteRunningJob(_ context.Context, info ipc.RunningJobInfo) error {
	r.calls++
	r.info = info
	return r.err
}

func (s *fakeLiveKitRoomService) ListRooms(_ context.Context, req *livekit.ListRoomsRequest) (*livekit.ListRoomsResponse, error) {
	s.listNames = append([]string(nil), req.Names...)
	return &livekit.ListRoomsResponse{Rooms: s.listRooms}, nil
}

func (s *fakeLiveKitRoomService) CreateRoom(_ context.Context, req *livekit.CreateRoomRequest) (*livekit.Room, error) {
	s.createdRooms = append(s.createdRooms, req.Name)
	return &livekit.Room{Sid: "RM_created", Name: req.Name}, nil
}

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
	if args.LogLevel != "DEBUG" {
		t.Fatalf("LogLevel = %q, want DEBUG", args.LogLevel)
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

func TestParseConnectArgsSupportsReferenceOptions(t *testing.T) {
	args, err := parseConnectArgs([]string{
		"worker", "connect",
		"--room", "room-a",
		"--participant-identity", "agent-custom",
		"--url", "wss://livekit.example",
		"--api-key", "api-key",
		"--api-secret", "api-secret",
		"--log-level", "trace",
	})
	if err != nil {
		t.Fatalf("parseConnectArgs() error = %v", err)
	}

	if args.RoomName != "room-a" {
		t.Fatalf("RoomName = %q, want room-a", args.RoomName)
	}
	if args.ParticipantIdentity != "agent-custom" {
		t.Fatalf("ParticipantIdentity = %q, want agent-custom", args.ParticipantIdentity)
	}
	if args.URL != "wss://livekit.example" {
		t.Fatalf("URL = %q, want wss://livekit.example", args.URL)
	}
	if args.APIKey != "api-key" {
		t.Fatalf("APIKey = %q, want api-key", args.APIKey)
	}
	if args.APISecret != "api-secret" {
		t.Fatalf("APISecret = %q, want api-secret", args.APISecret)
	}
	if args.LogLevel != "TRACE" {
		t.Fatalf("LogLevel = %q, want TRACE", args.LogLevel)
	}
}

func TestParseConnectArgsSupportsReferenceOptionsAfterPositionalRoom(t *testing.T) {
	args, err := parseConnectArgs([]string{
		"worker", "connect", "room-a",
		"--participant-identity", "agent-custom",
		"--url", "wss://livekit.example",
	})
	if err != nil {
		t.Fatalf("parseConnectArgs() error = %v", err)
	}

	if args.RoomName != "room-a" {
		t.Fatalf("RoomName = %q, want room-a", args.RoomName)
	}
	if args.ParticipantIdentity != "agent-custom" {
		t.Fatalf("ParticipantIdentity = %q, want agent-custom", args.ParticipantIdentity)
	}
	if args.URL != "wss://livekit.example" {
		t.Fatalf("URL = %q, want wss://livekit.example", args.URL)
	}
}

func TestParseConnectArgsRequiresRoom(t *testing.T) {
	_, err := parseConnectArgs([]string{"worker", "connect"})
	if err == nil {
		t.Fatal("parseConnectArgs() error = nil, want missing room error")
	}
}

func TestApplyConnectArgsUpdatesServerOptions(t *testing.T) {
	server := worker.NewAgentServer(worker.WorkerOptions{})
	args := ConnectArgs{
		URL:       "wss://connect.example",
		APIKey:    "connect-key",
		APISecret: "connect-secret",
		LogLevel:  "WARN",
	}

	if err := applyConnectArgs(server, args); err != nil {
		t.Fatalf("applyConnectArgs() error = %v", err)
	}

	if server.Options.WSURL != "wss://connect.example" || server.Options.WSRL != "wss://connect.example" {
		t.Fatalf("WSURL/WSRL = %q/%q, want connect URL", server.Options.WSURL, server.Options.WSRL)
	}
	if server.Options.APIKey != "connect-key" {
		t.Fatalf("APIKey = %q, want connect-key", server.Options.APIKey)
	}
	if server.Options.APISecret != "connect-secret" {
		t.Fatalf("APISecret = %q, want connect-secret", server.Options.APISecret)
	}
	if server.Options.LogLevel != "WARN" {
		t.Fatalf("LogLevel = %q, want WARN", server.Options.LogLevel)
	}
	if !server.Options.DevMode {
		t.Fatal("DevMode = false, want true for connect")
	}
}

func TestEnsureConnectRoomUsesExistingLiveKitRoom(t *testing.T) {
	service := &fakeLiveKitRoomService{
		listRooms: []*livekit.Room{{Sid: "RM_existing", Name: "room-a"}},
	}

	room, err := ensureConnectRoom(context.Background(), service, "room-a")
	if err != nil {
		t.Fatalf("ensureConnectRoom() error = %v", err)
	}

	if room.GetSid() != "RM_existing" {
		t.Fatalf("room SID = %q, want RM_existing", room.GetSid())
	}
	if strings.Join(service.listNames, ",") != "room-a" {
		t.Fatalf("ListRooms names = %#v, want room-a", service.listNames)
	}
	if len(service.createdRooms) != 0 {
		t.Fatalf("CreateRoom calls = %#v, want none", service.createdRooms)
	}
}

func TestEnsureConnectRoomCreatesMissingLiveKitRoom(t *testing.T) {
	service := &fakeLiveKitRoomService{}

	room, err := ensureConnectRoom(context.Background(), service, "room-a")
	if err != nil {
		t.Fatalf("ensureConnectRoom() error = %v", err)
	}

	if room.GetSid() != "RM_created" || room.GetName() != "room-a" {
		t.Fatalf("created room = %#v, want room-a with RM_created SID", room)
	}
	if strings.Join(service.createdRooms, ",") != "room-a" {
		t.Fatalf("CreateRoom names = %#v, want room-a", service.createdRooms)
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
	if args.LogLevel != "DEBUG" {
		t.Fatalf("LogLevel = %q, want DEBUG", args.LogLevel)
	}
}

func TestParseConsoleArgsSupportsAudioMode(t *testing.T) {
	args, err := parseConsoleArgs([]string{"worker", "console", "--audio"})
	if err != nil {
		t.Fatalf("parseConsoleArgs() error = %v", err)
	}
	if args.Mode != ConsoleModeAudio {
		t.Fatalf("Mode = %q, want %q", args.Mode, ConsoleModeAudio)
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

func TestConsoleLocalJobOptionsEnableRecordingWhenRequested(t *testing.T) {
	options := consoleLocalJobOptions(ConsoleArgs{Record: true})

	if !options.FakeJob {
		t.Fatal("FakeJob = false, want console local jobs to remain fake")
	}
	if options.RecordingOptions != (agent.RecordingOptions{Audio: true, Traces: true, Logs: true, Transcript: true}) {
		t.Fatalf("RecordingOptions = %#v, want all enabled", options.RecordingOptions)
	}
	if !strings.HasPrefix(options.SessionReportPath, "console-recordings/session-") {
		t.Fatalf("SessionReportPath = %q, want console recording session path", options.SessionReportPath)
	}
	if !strings.HasSuffix(options.SessionReportPath, "/session_report.json") {
		t.Fatalf("SessionReportPath = %q, want session_report.json", options.SessionReportPath)
	}
}

func TestConsoleSessionReportPathUsesReferenceDirectory(t *testing.T) {
	now := time.Date(2026, 6, 1, 9, 8, 7, 0, time.UTC)
	got := consoleSessionReportPath(now)
	want := "console-recordings/session-06-01-090807/session_report.json"

	if got != want {
		t.Fatalf("consoleSessionReportPath() = %q, want %q", got, want)
	}
}

func TestConsoleLocalJobOptionsDisableRecordingByDefault(t *testing.T) {
	options := consoleLocalJobOptions(ConsoleArgs{})

	if options.RecordingOptions != (agent.RecordingOptions{}) {
		t.Fatalf("RecordingOptions = %#v, want zero value", options.RecordingOptions)
	}
	if options.SessionReportPath != "" {
		t.Fatalf("SessionReportPath = %q, want empty when not recording", options.SessionReportPath)
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

func TestParseConsoleArgsSupportsLogLevel(t *testing.T) {
	args, err := parseConsoleArgs([]string{"worker", "console", "--log-level", "trace"})
	if err != nil {
		t.Fatalf("parseConsoleArgs() error = %v", err)
	}
	if args.LogLevel != "TRACE" {
		t.Fatalf("LogLevel = %q, want TRACE", args.LogLevel)
	}
}

func TestApplyDevModeEnvForReferenceSubcommands(t *testing.T) {
	t.Setenv("LIVEKIT_DEV_MODE", "")

	if err := applyDevModeEnv([]string{"worker", "console"}); err != nil {
		t.Fatalf("applyDevModeEnv(console) error = %v", err)
	}
	if got := os.Getenv("LIVEKIT_DEV_MODE"); got != "1" {
		t.Fatalf("LIVEKIT_DEV_MODE after console = %q, want 1", got)
	}

	t.Setenv("LIVEKIT_DEV_MODE", "")
	if err := applyDevModeEnv([]string{"worker", "dev"}); err != nil {
		t.Fatalf("applyDevModeEnv(dev) error = %v", err)
	}
	if got := os.Getenv("LIVEKIT_DEV_MODE"); got != "1" {
		t.Fatalf("LIVEKIT_DEV_MODE after dev = %q, want 1", got)
	}

	t.Setenv("LIVEKIT_DEV_MODE", "")
	if err := applyDevModeEnv([]string{"worker", "start"}); err != nil {
		t.Fatalf("applyDevModeEnv(start) error = %v", err)
	}
	if got := os.Getenv("LIVEKIT_DEV_MODE"); got != "" {
		t.Fatalf("LIVEKIT_DEV_MODE after start = %q, want unchanged empty", got)
	}
}

func TestApplyDevReloadCompatibilityDisablesReloadOnITerm(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	args := CliArgs{DevMode: true, Reload: true}

	applyDevReloadCompatibility(&args)

	if args.Reload {
		t.Fatal("Reload = true, want false on iTerm.app")
	}
}

func TestApplyDevReloadCompatibilityLeavesReloadOnOtherTerminals(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "xterm-256color")
	args := CliArgs{DevMode: true, Reload: true}

	applyDevReloadCompatibility(&args)

	if !args.Reload {
		t.Fatal("Reload = false, want true outside iTerm.app")
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

func TestStartConsoleAudioUISkipsAudioForTextMode(t *testing.T) {
	stop, err := startConsoleAudioUI(context.Background(), ConsoleArgs{Mode: ConsoleModeText})
	if err != nil {
		t.Fatalf("startConsoleAudioUI(text) error = %v, want nil", err)
	}
	if stop == nil {
		t.Fatal("startConsoleAudioUI(text) stop = nil, want no-op stop function")
	}

	stop()
}

func TestStartConsoleTranscriptPrinterWritesAgentOutput(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("help"), nil, agent.AgentSessionOptions{})
	var out bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if !startConsoleTranscriptPrinter(ctx, session, &out) {
		t.Fatal("startConsoleTranscriptPrinter() = false, want true for AgentSession")
	}
	session.EmitAgentOutputTranscribed(agent.AgentOutputTranscribedEvent{Transcript: "hello from agent"})

	deadline := time.After(time.Second)
	for !strings.Contains(out.String(), "hello from agent") {
		select {
		case <-deadline:
			t.Fatalf("console transcript output = %q, want agent transcript", out.String())
		default:
			time.Sleep(time.Millisecond)
		}
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

func TestRunEvalWritesRunnerSummary(t *testing.T) {
	var out bytes.Buffer
	called := false
	err := runEval(func(ctx context.Context) (string, error) {
		called = true
		return "score=1.00 all_passed=true\n", nil
	}, &out)
	if err != nil {
		t.Fatalf("runEval() error = %v", err)
	}
	if !called {
		t.Fatal("runner was not called")
	}
	if got := out.String(); got != "score=1.00 all_passed=true\n" {
		t.Fatalf("output = %q, want evaluation summary", got)
	}
}

func TestRunEvalRequiresConfiguredRunner(t *testing.T) {
	var out bytes.Buffer
	if err := runEval(nil, &out); err == nil {
		t.Fatal("runEval(nil) error = nil, want missing runner error")
	}
}

func TestRunDownloadFilesForPluginsReturnsPluginError(t *testing.T) {
	errBoom := errors.New("download failed")
	plugins := []plugin.Plugin{
		&fakePlugin{title: "ok", packageName: "plugin-ok"},
		&fakePlugin{title: "bad", packageName: "plugin-bad", err: errBoom},
	}

	err := runDownloadFilesForPlugins(plugins)
	if err == nil {
		t.Fatal("runDownloadFilesForPlugins() error = nil, want plugin error")
	}
	if !errors.Is(err, errBoom) {
		t.Fatalf("runDownloadFilesForPlugins() error = %v, want wrapped plugin error", err)
	}
	if plugins[0].(*fakePlugin).calls != 1 {
		t.Fatalf("first plugin calls = %d, want 1", plugins[0].(*fakePlugin).calls)
	}
	if plugins[1].(*fakePlugin).calls != 1 {
		t.Fatalf("second plugin calls = %d, want 1", plugins[1].(*fakePlugin).calls)
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

func TestParseWorkerArgsSupportsReferenceStartOptions(t *testing.T) {
	args, drainTimeout, err := parseWorkerArgs([]string{
		"worker", "start",
		"--log-level", "warn",
		"--url", "wss://livekit.example",
		"--api-key", "api-key",
		"--api-secret", "api-secret",
		"--drain-timeout", "42",
	}, false)
	if err != nil {
		t.Fatalf("parseWorkerArgs() error = %v", err)
	}

	if args.LogLevel != "WARN" {
		t.Fatalf("LogLevel = %q, want WARN", args.LogLevel)
	}
	if args.URL != "wss://livekit.example" {
		t.Fatalf("URL = %q, want wss://livekit.example", args.URL)
	}
	if args.APIKey != "api-key" {
		t.Fatalf("APIKey = %q, want api-key", args.APIKey)
	}
	if args.APISecret != "api-secret" {
		t.Fatalf("APISecret = %q, want api-secret", args.APISecret)
	}
	if args.DevMode {
		t.Fatal("DevMode = true, want false for start")
	}
	if args.Reload {
		t.Fatal("Reload = true, want false for start")
	}
	if drainTimeout == nil || *drainTimeout != 42 {
		t.Fatalf("drainTimeout = %v, want 42", drainTimeout)
	}
}

func TestParseWorkerArgsSupportsReferenceSimulationOption(t *testing.T) {
	args, drainTimeout, err := parseWorkerArgs([]string{
		"worker", "start",
		"--simulation",
	}, false)
	if err != nil {
		t.Fatalf("parseWorkerArgs() error = %v", err)
	}
	if drainTimeout != nil {
		t.Fatalf("drainTimeout = %v, want nil", drainTimeout)
	}

	if !args.Simulation {
		t.Fatal("Simulation = false, want true")
	}
}

func TestParseWorkerArgsSupportsReferenceDevOptions(t *testing.T) {
	args, drainTimeout, err := parseWorkerArgs([]string{
		"worker", "dev",
		"--log-level", "trace",
		"--url", "wss://dev.example",
		"--api-key", "dev-key",
		"--api-secret", "dev-secret",
		"--no-reload",
	}, true)
	if err != nil {
		t.Fatalf("parseWorkerArgs() error = %v", err)
	}

	if args.LogLevel != "TRACE" {
		t.Fatalf("LogLevel = %q, want TRACE", args.LogLevel)
	}
	if args.URL != "wss://dev.example" {
		t.Fatalf("URL = %q, want wss://dev.example", args.URL)
	}
	if args.APIKey != "dev-key" {
		t.Fatalf("APIKey = %q, want dev-key", args.APIKey)
	}
	if args.APISecret != "dev-secret" {
		t.Fatalf("APISecret = %q, want dev-secret", args.APISecret)
	}
	if !args.DevMode {
		t.Fatal("DevMode = false, want true for dev")
	}
	if args.Reload {
		t.Fatal("Reload = true, want false after --no-reload")
	}
	if drainTimeout != nil {
		t.Fatalf("drainTimeout = %v, want nil for dev", drainTimeout)
	}
}

func TestApplyWorkerArgsUpdatesServerOptions(t *testing.T) {
	server := worker.NewAgentServer(worker.WorkerOptions{})
	args := CliArgs{
		LogLevel:  "ERROR",
		URL:       "wss://livekit.example",
		APIKey:    "api-key",
		APISecret: "api-secret",
		DevMode:   true,
	}
	drainTimeout := 15

	if err := applyWorkerArgs(server, args, &drainTimeout); err != nil {
		t.Fatalf("applyWorkerArgs() error = %v", err)
	}

	if server.Options.LogLevel != "ERROR" {
		t.Fatalf("LogLevel = %q, want ERROR", server.Options.LogLevel)
	}
	if server.Options.WSURL != "wss://livekit.example" || server.Options.WSRL != "wss://livekit.example" {
		t.Fatalf("WSURL/WSRL = %q/%q, want URL applied", server.Options.WSURL, server.Options.WSRL)
	}
	if server.Options.APIKey != "api-key" {
		t.Fatalf("APIKey = %q, want api-key", server.Options.APIKey)
	}
	if server.Options.APISecret != "api-secret" {
		t.Fatalf("APISecret = %q, want api-secret", server.Options.APISecret)
	}
	if !server.Options.DevMode {
		t.Fatal("DevMode = false, want true")
	}
	if server.Options.DrainTimeoutSeconds != 15 {
		t.Fatalf("DrainTimeoutSeconds = %d, want 15", server.Options.DrainTimeoutSeconds)
	}
}

func TestApplyWorkerArgsPreservesExplicitZeroDrainTimeout(t *testing.T) {
	server := worker.NewAgentServer(worker.WorkerOptions{DrainTimeoutSeconds: 30})
	drainTimeout := 0

	if err := applyWorkerArgs(server, CliArgs{}, &drainTimeout); err != nil {
		t.Fatalf("applyWorkerArgs() error = %v", err)
	}

	if server.Options.DrainTimeoutSeconds != 0 {
		t.Fatalf("DrainTimeoutSeconds = %d, want explicit zero", server.Options.DrainTimeoutSeconds)
	}
}

func TestRunWorkerLifecycleDrainsAfterSignalCancellation(t *testing.T) {
	runner := &fakeWorkerLifecycle{runErr: context.Canceled}

	if err := runWorkerLifecycle(context.Background(), runner, false); err != nil {
		t.Fatalf("runWorkerLifecycle() error = %v", err)
	}
	if runner.runCalls != 1 {
		t.Fatalf("runCalls = %d, want 1", runner.runCalls)
	}
	if runner.drainCalls != 1 {
		t.Fatalf("drainCalls = %d, want 1 after cancellation in start mode", runner.drainCalls)
	}
}

func TestRunWorkerLifecycleSkipsDrainInDevMode(t *testing.T) {
	runner := &fakeWorkerLifecycle{runErr: context.Canceled}

	if err := runWorkerLifecycle(context.Background(), runner, true); err != nil {
		t.Fatalf("runWorkerLifecycle() error = %v", err)
	}
	if runner.drainCalls != 0 {
		t.Fatalf("drainCalls = %d, want 0 in dev mode", runner.drainCalls)
	}
}

func TestRunWorkerLifecyclePropagatesRunErrors(t *testing.T) {
	runErr := errors.New("run failed")
	runner := &fakeWorkerLifecycle{runErr: runErr}

	err := runWorkerLifecycle(context.Background(), runner, false)
	if !errors.Is(err, runErr) {
		t.Fatalf("runWorkerLifecycle() error = %v, want run error", err)
	}
	if runner.drainCalls != 0 {
		t.Fatalf("drainCalls = %d, want 0 after run failure", runner.drainCalls)
	}
}

func TestWriteWorkerErrorIncludesRunError(t *testing.T) {
	var out strings.Builder

	writeWorkerError(&out, errors.New("ws_url is required, or set LIVEKIT_URL environment variable"))

	got := out.String()
	want := "Worker error: ws_url is required, or set LIVEKIT_URL environment variable\n"
	if got != want {
		t.Fatalf("worker error output = %q, want %q", got, want)
	}
}

func TestRunWorkerLifecyclePropagatesDrainErrors(t *testing.T) {
	drainErr := errors.New("drain failed")
	runner := &fakeWorkerLifecycle{runErr: context.Canceled, drainErr: drainErr}

	err := runWorkerLifecycle(context.Background(), runner, false)
	if !errors.Is(err, drainErr) {
		t.Fatalf("runWorkerLifecycle() error = %v, want drain error", err)
	}
}

func TestRunWorkerLifecycleTreatsDrainTimeoutAsWarning(t *testing.T) {
	runner := &fakeWorkerLifecycle{runErr: context.Canceled, drainErr: context.DeadlineExceeded}

	if err := runWorkerLifecycle(context.Background(), runner, false); err != nil {
		t.Fatalf("runWorkerLifecycle() error = %v, want nil for drain timeout", err)
	}
	if runner.drainCalls != 1 {
		t.Fatalf("drainCalls = %d, want 1", runner.drainCalls)
	}
}

func TestRunProcessJobFromEnvExecutesEnvRunningJob(t *testing.T) {
	info := ipc.RunningJobInfo{
		AcceptArguments: ipc.JobAcceptArguments{Identity: "agent-process"},
		Job:             &livekit.Job{Id: "job-process", Room: &livekit.Room{Name: "room-a"}},
		URL:             "wss://livekit.example",
		Token:           "room-token",
		WorkerID:        "worker-a",
		FakeJob:         true,
	}
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("Marshal RunningJobInfo: %v", err)
	}
	runner := &fakeProcessJobRunner{}

	handled, err := runProcessJobFromEnv(context.Background(), runner, map[string]string{
		"LIVEKIT_AGENT_RUNNING_JOB_JSON": string(data),
	})
	if err != nil {
		t.Fatalf("runProcessJobFromEnv() error = %v", err)
	}
	if !handled {
		t.Fatal("runProcessJobFromEnv() handled = false, want true")
	}
	if runner.calls != 1 {
		t.Fatalf("ExecuteRunningJob calls = %d, want 1", runner.calls)
	}
	if runner.info.Job.GetId() != "job-process" {
		t.Fatalf("running job id = %q, want job-process", runner.info.Job.GetId())
	}
	if runner.info.AcceptArguments.Identity != "agent-process" {
		t.Fatalf("identity = %q, want agent-process", runner.info.AcceptArguments.Identity)
	}
	if runner.info.URL != "wss://livekit.example" || runner.info.Token != "room-token" || runner.info.WorkerID != "worker-a" || !runner.info.FakeJob {
		t.Fatalf("running job info = %#v, want preserved assignment", runner.info)
	}
}

func TestRunProcessJobFromEnvSkipsWhenNoProcessJobEnv(t *testing.T) {
	runner := &fakeProcessJobRunner{}

	handled, err := runProcessJobFromEnv(context.Background(), runner, map[string]string{})
	if err != nil {
		t.Fatalf("runProcessJobFromEnv() error = %v", err)
	}
	if handled {
		t.Fatal("runProcessJobFromEnv() handled = true, want false")
	}
	if runner.calls != 0 {
		t.Fatalf("ExecuteRunningJob calls = %d, want 0", runner.calls)
	}
}

func TestApplyWorkerArgsUsesDevDefaultLogLevel(t *testing.T) {
	server := worker.NewAgentServer(worker.WorkerOptions{})

	if err := applyWorkerArgs(server, CliArgs{DevMode: true}, nil); err != nil {
		t.Fatalf("applyWorkerArgs() error = %v", err)
	}

	if server.Options.LogLevel != "DEBUG" {
		t.Fatalf("LogLevel = %q, want reference dev default DEBUG", server.Options.LogLevel)
	}
}

func TestCLIProtocolLoggerConfigUsesReferenceProductionJSON(t *testing.T) {
	cfg := cliProtocolLoggerConfig("WARN", false)

	if cfg.Level != "warn" {
		t.Fatalf("Level = %q, want warn", cfg.Level)
	}
	if !cfg.JSON {
		t.Fatal("JSON = false, want true for production CLI logging")
	}
}

func TestCLIProtocolLoggerConfigUsesReferenceDevText(t *testing.T) {
	cfg := cliProtocolLoggerConfig("DEBUG", true)

	if cfg.Level != "debug" {
		t.Fatalf("Level = %q, want debug", cfg.Level)
	}
	if cfg.JSON {
		t.Fatal("JSON = true, want false for dev CLI logging")
	}
}

func TestStartArgsForDevReloadForwardsReferenceConnectionOptions(t *testing.T) {
	got := startArgsForDevReload(CliArgs{
		LogLevel:  "TRACE",
		URL:       "wss://livekit.example",
		APIKey:    "api-key",
		APISecret: "api-secret",
	})
	want := []string{
		"--log-level", "TRACE",
		"--url", "wss://livekit.example",
		"--api-key", "api-key",
		"--api-secret", "api-secret",
	}

	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("startArgsForDevReload() = %#v, want %#v", got, want)
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

func TestDevModeWatcherSharesCliReloadState(t *testing.T) {
	args := &CliArgs{ReloadCount: 4}
	calls := 0
	watcher := newDevModeWatcher(args, func() {
		calls++
	})
	watcher.activeJobsTimeout = time.Nanosecond

	if !watcher.triggerReload() {
		t.Fatal("triggerReload() = false, want true")
	}
	if calls != 1 {
		t.Fatalf("onChange calls = %d, want 1", calls)
	}
	if args.ReloadCount != 5 {
		t.Fatalf("ReloadCount = %d, want 5", args.ReloadCount)
	}
	if resp := watcher.reloadJobsResponse(); resp.ReloadCount != 5 {
		t.Fatalf("ReloadJobsResponse.ReloadCount = %d, want 5", resp.ReloadCount)
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

func TestWatcherTriggerReloadRequestsActiveJobsBeforeIncrementingReloadCount(t *testing.T) {
	args := &CliArgs{ReloadCount: 3}
	var output bytes.Buffer
	watcher := NewWatcher(nil, func() {
		if args.ReloadCount != 4 {
			t.Fatalf("ReloadCount during restart = %d, want 4", args.ReloadCount)
		}
	}, args)
	watcher.reloadIPC = writerFunc(func(p []byte) (int, error) {
		if args.ReloadCount != 3 {
			t.Fatalf("ReloadCount while requesting active jobs = %d, want 3", args.ReloadCount)
		}
		return output.Write(p)
	})
	watcher.activeJobsTimeout = time.Nanosecond

	if !watcher.triggerReload() {
		t.Fatal("triggerReload() = false, want true")
	}
	msg, err := ipc.ReadMessage(&output)
	if err != nil {
		t.Fatalf("ReadMessage ActiveJobsRequest: %v", err)
	}
	if msg.Type != ipc.MessageTypeActiveJobsRequest {
		t.Fatalf("request Type = %q, want %q", msg.Type, ipc.MessageTypeActiveJobsRequest)
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

	currentJobs := []ipc.RunningJobInfo{{
		Job:   &livekit.Job{Id: "job-current"},
		Token: "current-token",
		AcceptArguments: ipc.JobAcceptArguments{
			Attributes: map[string]string{"tier": "gold"},
		},
	}}
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

	resp.Jobs[0].AcceptArguments.Attributes["tier"] = "platinum"
	if got := watcher.reloadJobsResponse(); got.Jobs[0].AcceptArguments.Attributes["tier"] != "gold" {
		t.Fatalf("mutating ReloadJobsResponse attributes changed stored tier to %q", got.Jobs[0].AcceptArguments.Attributes["tier"])
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

	watcher.reloading = true
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
	watcher.reloading = true

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
	watcher.reloading = true

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

func TestWatcherStartReloadIPCServerRunsSessionForChildConnection(t *testing.T) {
	args := &CliArgs{ReloadCount: 11}
	watcher := NewWatcher(nil, nil, args)
	socketPath := tempUnixSocketPath(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverConn, childConn := net.Pipe()
	defer childConn.Close()
	listener := newStubReloadIPCListener(serverConn)
	origListen := reloadIPCListen
	reloadIPCListen = func(network, address string) (net.Listener, error) {
		if network != "unix" {
			t.Fatalf("reload IPC network = %q, want unix", network)
		}
		if address != socketPath {
			t.Fatalf("reload IPC address = %q, want %q", address, socketPath)
		}
		return listener, nil
	}
	t.Cleanup(func() {
		reloadIPCListen = origListen
	})

	closer, err := watcher.startReloadIPCServer(ctx, socketPath)
	if err != nil {
		t.Fatalf("startReloadIPCServer() error = %v", err)
	}
	defer closer.Close()

	msg := readIPCMessageWithin(t, childConn, time.Second, "initial reload jobs request")
	if msg.Type != ipc.MessageTypeReloadJobsRequest {
		t.Fatalf("initial request Type = %q, want %q", msg.Type, ipc.MessageTypeReloadJobsRequest)
	}
	if err := ipc.WriteMessage(childConn, mustIPCMessage(t, &ipc.Reloaded{})); err != nil {
		t.Fatalf("WriteMessage Reloaded: %v", err)
	}
}

type stubReloadIPCListener struct {
	once   sync.Once
	connC  chan net.Conn
	closeC chan struct{}
}

func newStubReloadIPCListener(conn net.Conn) *stubReloadIPCListener {
	l := &stubReloadIPCListener{
		connC:  make(chan net.Conn, 1),
		closeC: make(chan struct{}),
	}
	l.connC <- conn
	return l
}

func (l *stubReloadIPCListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.connC:
		return conn, nil
	case <-l.closeC:
		return nil, net.ErrClosed
	}
}

func (l *stubReloadIPCListener) Close() error {
	l.once.Do(func() {
		close(l.closeC)
	})
	return nil
}

func (l *stubReloadIPCListener) Addr() net.Addr {
	return stubNetAddr("reload-ipc")
}

type stubNetAddr string

func (a stubNetAddr) Network() string { return string(a) }
func (a stubNetAddr) String() string  { return string(a) }

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) {
	return f(p)
}

func tempUnixSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "rtp-agent-reload-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp /tmp: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return filepath.Join(dir, "reload.sock")
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

type fakeWorkerLifecycle struct {
	runErr     error
	drainErr   error
	runCalls   int
	drainCalls int
}

func (f *fakeWorkerLifecycle) Run(context.Context) error {
	f.runCalls++
	return f.runErr
}

func (f *fakeWorkerLifecycle) Drain(context.Context) error {
	f.drainCalls++
	return f.drainErr
}
