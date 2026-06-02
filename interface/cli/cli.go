package cli

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	consoleui "github.com/cavos-io/rtp-agent/interface/cli/console"
	"github.com/cavos-io/rtp-agent/interface/worker"
	workeripc "github.com/cavos-io/rtp-agent/interface/worker/ipc"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/library/plugin"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/gordonklaus/portaudio"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

type CliArgs struct {
	LogLevel  string
	URL       string
	APIKey    string
	APISecret string
	DevMode   bool
	Reload    bool

	// ReloadCount tracks how many times the dev-mode worker has been reloaded.
	ReloadCount int
}

type ConnectArgs struct {
	RoomName            string
	ParticipantIdentity string
	LogLevel            string
	URL                 string
	APIKey              string
	APISecret           string
}

type liveKitRoomService interface {
	ListRooms(context.Context, *livekit.ListRoomsRequest) (*livekit.ListRoomsResponse, error)
	CreateRoom(context.Context, *livekit.CreateRoomRequest) (*livekit.Room, error)
}

type ConsoleMode string

const (
	ConsoleModeAudio ConsoleMode = "audio"
	ConsoleModeText  ConsoleMode = "text"
)

type ConsoleArgs struct {
	InputDevice  string
	OutputDevice string
	Mode         ConsoleMode
	Record       bool
	ListDevices  bool
	LogLevel     string
}

type EvalRunner func(context.Context) (string, error)

type consoleAudioDevice struct {
	Index             int
	Name              string
	MaxInputChannels  int
	MaxOutputChannels int
}

var printConsoleAudioDevices = func() {
	devices, defaultInput, defaultOutput, err := consoleAudioDevices()
	if err != nil {
		fmt.Printf("Failed to list audio devices: %v\n", err)
		return
	}
	fmt.Print(formatConsoleAudioDevices(devices, defaultInput, defaultOutput))
}

var newLiveKitRoomService = func(url, apiKey, apiSecret string) liveKitRoomService {
	return lksdk.NewRoomServiceClient(url, apiKey, apiSecret)
}

var consoleAudioDevices = func() ([]consoleAudioDevice, int, int, error) {
	if err := portaudio.Initialize(); err != nil {
		return nil, -1, -1, err
	}
	defer portaudio.Terminate()

	portaudioDevices, err := portaudio.Devices()
	if err != nil {
		return nil, -1, -1, err
	}

	defaultInput := -1
	if device, err := portaudio.DefaultInputDevice(); err == nil && device != nil {
		defaultInput = device.Index
	}
	defaultOutput := -1
	if device, err := portaudio.DefaultOutputDevice(); err == nil && device != nil {
		defaultOutput = device.Index
	}

	devices := make([]consoleAudioDevice, 0, len(portaudioDevices))
	for _, device := range portaudioDevices {
		if device == nil {
			continue
		}
		devices = append(devices, consoleAudioDevice{
			Index:             device.Index,
			Name:              device.Name,
			MaxInputChannels:  device.MaxInputChannels,
			MaxOutputChannels: device.MaxOutputChannels,
		})
	}

	return devices, defaultInput, defaultOutput, nil
}

func formatConsoleAudioDevices(devices []consoleAudioDevice, defaultInput, defaultOutput int) string {
	var b strings.Builder
	b.WriteString("ID\tType\tName\tDefault\n")
	for _, device := range devices {
		if device.MaxInputChannels > 0 {
			defaultMarker := ""
			if device.Index == defaultInput {
				defaultMarker = "yes"
			}
			fmt.Fprintf(&b, "%d\tInput\t%s\t%s\n", device.Index, device.Name, defaultMarker)
		}
		if device.MaxOutputChannels > 0 {
			defaultMarker := ""
			if device.Index == defaultOutput {
				defaultMarker = "yes"
			}
			fmt.Fprintf(&b, "%d\tOutput\t%s\t%s\n", device.Index, device.Name, defaultMarker)
		}
	}
	return b.String()
}

func RunApp(server *worker.AgentServer, evalRunners ...EvalRunner) {
	var evalRunner EvalRunner
	if len(evalRunners) > 0 {
		evalRunner = evalRunners[0]
	}
	DiscoverPlugins()
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}
	if err := applyDevModeEnv(os.Args); err != nil {
		logger.Logger.Errorw("Failed to set dev mode environment", err)
		os.Exit(1)
	}

	switch os.Args[1] {
	case "start":
		args, drainTimeout, err := parseWorkerArgs(os.Args, false)
		if err != nil {
			fmt.Println(err)
			printUsage()
			os.Exit(1)
		}
		if err := applyWorkerArgs(server, args, drainTimeout); err != nil {
			logger.Logger.Errorw("Failed to apply worker options", err)
			os.Exit(1)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if handled, err := runProcessJobFromEnv(ctx, server, currentEnvMap()); handled {
			if err != nil && !errors.Is(err, context.Canceled) {
				logger.Logger.Errorw("Process job error", err)
				os.Exit(1)
			}
			return
		}
		runWorker(server, false)
	case "dev":
		args, drainTimeout, err := parseWorkerArgs(os.Args, true)
		if err != nil {
			fmt.Println(err)
			printUsage()
			os.Exit(1)
		}
		if err := applyWorkerArgs(server, args, drainTimeout); err != nil {
			logger.Logger.Errorw("Failed to apply worker options", err)
			os.Exit(1)
		}
		applyDevReloadCompatibility(&args)
		if !args.Reload {
			runWorker(server, true)
			return
		}
		if err := RunWithDevMode(os.Args); err != nil {
			logger.Logger.Errorw("Dev mode error", err)
			os.Exit(1)
		}
	case "connect":
		runConnect(server)
	case "console":
		runConsole(server, os.Args)
	case "download-files":
		if err := runDownloadFiles(); err != nil {
			logger.Logger.Errorw("Download files failed", err)
			os.Exit(1)
		}
	case "eval":
		if err := runEval(evalRunner, os.Stdout); err != nil {
			logger.Logger.Errorw("Evaluation failed", err)
			os.Exit(1)
		}
	default:
		printUsage()
		os.Exit(1)
	}
}

func parseWorkerArgs(argv []string, devMode bool) (CliArgs, *int, error) {
	args := CliArgs{DevMode: devMode, Reload: devMode}
	var drainTimeout *int
	for i := 2; i < len(argv); i++ {
		switch argv[i] {
		case "--log-level":
			i++
			if i >= len(argv) {
				return CliArgs{}, nil, fmt.Errorf("missing value for --log-level")
			}
			logLevel := strings.ToUpper(argv[i])
			if !validConsoleLogLevel(logLevel) {
				return CliArgs{}, nil, fmt.Errorf("unknown log level %q", argv[i])
			}
			args.LogLevel = logLevel
		case "--url":
			i++
			if i >= len(argv) {
				return CliArgs{}, nil, fmt.Errorf("missing value for --url")
			}
			args.URL = argv[i]
		case "--api-key":
			i++
			if i >= len(argv) {
				return CliArgs{}, nil, fmt.Errorf("missing value for --api-key")
			}
			args.APIKey = argv[i]
		case "--api-secret":
			i++
			if i >= len(argv) {
				return CliArgs{}, nil, fmt.Errorf("missing value for --api-secret")
			}
			args.APISecret = argv[i]
		case "--drain-timeout":
			if devMode {
				return CliArgs{}, nil, fmt.Errorf("--drain-timeout is only supported by start")
			}
			i++
			if i >= len(argv) {
				return CliArgs{}, nil, fmt.Errorf("missing value for --drain-timeout")
			}
			value, err := strconv.Atoi(argv[i])
			if err != nil || value < 0 {
				return CliArgs{}, nil, fmt.Errorf("invalid --drain-timeout %q", argv[i])
			}
			drainTimeout = &value
		case "--reload":
			if !devMode {
				return CliArgs{}, nil, fmt.Errorf("--reload is only supported by dev")
			}
			args.Reload = true
		case "--no-reload":
			if !devMode {
				return CliArgs{}, nil, fmt.Errorf("--no-reload is only supported by dev")
			}
			args.Reload = false
		default:
			return CliArgs{}, nil, fmt.Errorf("unknown worker option %q", argv[i])
		}
	}
	return args, drainTimeout, nil
}

func applyWorkerArgs(server *worker.AgentServer, args CliArgs, drainTimeout *int) error {
	logLevel := args.LogLevel
	if logLevel == "" && args.DevMode && server.Options.LogLevel == "INFO" && os.Getenv("LIVEKIT_LOG_LEVEL") == "" {
		logLevel = "DEBUG"
	}
	opts := worker.WorkerOptions{
		LogLevel:  logLevel,
		WSURL:     args.URL,
		APIKey:    args.APIKey,
		APISecret: args.APISecret,
		DevMode:   args.DevMode,
	}
	if drainTimeout != nil {
		opts.DrainTimeoutSeconds = *drainTimeout
		opts.DrainTimeoutSecondsSet = true
	}
	return server.UpdateOptions(opts)
}

func applyDevModeEnv(argv []string) error {
	if len(argv) < 2 {
		return nil
	}
	switch argv[1] {
	case "console", "dev":
		return os.Setenv("LIVEKIT_DEV_MODE", "1")
	default:
		return nil
	}
}

func applyDevReloadCompatibility(args *CliArgs) {
	if args == nil || !args.DevMode || !args.Reload {
		return
	}
	if os.Getenv("TERM_PROGRAM") == "iTerm.app" {
		args.Reload = false
	}
}

func printUsage() {
	fmt.Println("Usage: worker [subcommand]")
	fmt.Println("Subcommands:")
	fmt.Println("  start           Run the worker in production mode")
	fmt.Println("  dev             Run the worker in development mode (with auto-reload)")
	fmt.Println("  connect         Connect to a room and execute a local job")
	fmt.Println("  console         Run the worker in console mode for interactive testing")
	fmt.Println("  download-files  Download required files for all registered plugins")
	fmt.Println("  eval            Run configured evaluation judges against the current session context")
}

func runEval(runner EvalRunner, out io.Writer) error {
	if runner == nil {
		return fmt.Errorf("evaluation runner is not configured")
	}
	summary, err := runner(context.Background())
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(out, summary)
	return err
}

func runDownloadFiles() error {
	return runDownloadFilesForPlugins(plugin.RegisteredPlugins())
}

func runDownloadFilesForPlugins(plugins []plugin.Plugin) error {
	fmt.Printf("Downloading files for %d registered plugins...\n", len(plugins))
	for _, p := range plugins {
		fmt.Printf("Downloading for %s (%s)...\n", p.Title(), p.Package())
		if err := p.DownloadFiles(); err != nil {
			logger.Logger.Errorw("Failed to download files", err, "plugin", p.Title())
			return fmt.Errorf("download files for %s: %w", p.Package(), err)
		}
	}
	fmt.Println("Finished downloading files.")
	return nil
}

func runWorker(server *worker.AgentServer, devMode bool) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Logger.Infow("Starting worker", "devMode", devMode)
	if err := runWorkerLifecycle(ctx, server, devMode); err != nil {
		logger.Logger.Errorw("Worker error", err)
		os.Exit(1)
	}
}

type workerLifecycle interface {
	Run(context.Context) error
	Drain(context.Context) error
}

type processJobRunner interface {
	ExecuteRunningJob(context.Context, workeripc.RunningJobInfo) error
}

func runProcessJobFromEnv(ctx context.Context, runner processJobRunner, env map[string]string) (bool, error) {
	if env["LIVEKIT_AGENT_RUNNING_JOB_JSON"] == "" && env["LIVEKIT_AGENT_JOB_JSON"] == "" {
		return false, nil
	}
	info, err := workeripc.RunningJobInfoFromEnv(env)
	if err != nil {
		return true, err
	}
	return true, runner.ExecuteRunningJob(ctx, info)
}

func currentEnvMap() map[string]string {
	env := make(map[string]string)
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			env[key] = value
		}
	}
	return env
}

func runWorkerLifecycle(ctx context.Context, server workerLifecycle, devMode bool) error {
	err := server.Run(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	if devMode {
		return nil
	}
	if err := server.Drain(context.Background()); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			logger.Logger.Warnw("drain timed out, forcing shutdown", err)
			return nil
		}
		return err
	}
	return nil
}

func runConnect(server *worker.AgentServer) {
	args, err := parseConnectArgs(os.Args)
	if err != nil {
		fmt.Println("Usage: worker connect <room_name> [participant_identity]")
		os.Exit(1)
	}
	if err := applyConnectArgs(server, args); err != nil {
		logger.Logger.Errorw("Failed to apply connect options", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Logger.Infow("Starting connect mode", "room", args.RoomName, "participant", args.ParticipantIdentity)

	roomInfo, err := ensureConnectRoom(ctx, newLiveKitRoomService(server.Options.WSRL, server.Options.APIKey, server.Options.APISecret), args.RoomName)
	if err != nil {
		logger.Logger.Errorw("Connect room lookup error", err)
		os.Exit(1)
	}

	jobDone := make(chan error, 1)
	server.OnWorkerStarted(func() {
		go func() {
			jobDone <- server.ExecuteLocalJobWithOptions(
				ctx,
				args.RoomName,
				args.ParticipantIdentity,
				worker.LocalJobOptions{FakeJob: false, RoomInfo: roomInfo},
			)
			stop()
		}()
	})

	if err := server.RunUnregistered(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Logger.Errorw("Connect error", err)
		os.Exit(1)
	}
	if err := <-jobDone; err != nil && !errors.Is(err, context.Canceled) {
		logger.Logger.Errorw("Connect error", err)
		os.Exit(1)
	}
}

func ensureConnectRoom(ctx context.Context, roomService liveKitRoomService, roomName string) (*livekit.Room, error) {
	resp, err := roomService.ListRooms(ctx, &livekit.ListRoomsRequest{Names: []string{roomName}})
	if err != nil {
		return nil, err
	}
	if len(resp.Rooms) > 0 {
		return resp.Rooms[0], nil
	}
	return roomService.CreateRoom(ctx, &livekit.CreateRoomRequest{Name: roomName})
}

func parseConnectArgs(argv []string) (ConnectArgs, error) {
	args := ConnectArgs{
		LogLevel:            "DEBUG",
		ParticipantIdentity: defaultConnectParticipantIdentity(),
	}
	positional := 0
	for i := 2; i < len(argv); i++ {
		switch argv[i] {
		case "--room":
			i++
			if i >= len(argv) {
				return ConnectArgs{}, fmt.Errorf("missing value for --room")
			}
			args.RoomName = argv[i]
		case "--participant-identity":
			i++
			if i >= len(argv) {
				return ConnectArgs{}, fmt.Errorf("missing value for --participant-identity")
			}
			args.ParticipantIdentity = argv[i]
		case "--url":
			i++
			if i >= len(argv) {
				return ConnectArgs{}, fmt.Errorf("missing value for --url")
			}
			args.URL = argv[i]
		case "--api-key":
			i++
			if i >= len(argv) {
				return ConnectArgs{}, fmt.Errorf("missing value for --api-key")
			}
			args.APIKey = argv[i]
		case "--api-secret":
			i++
			if i >= len(argv) {
				return ConnectArgs{}, fmt.Errorf("missing value for --api-secret")
			}
			args.APISecret = argv[i]
		case "--log-level":
			i++
			if i >= len(argv) {
				return ConnectArgs{}, fmt.Errorf("missing value for --log-level")
			}
			logLevel := strings.ToUpper(argv[i])
			if !validConsoleLogLevel(logLevel) {
				return ConnectArgs{}, fmt.Errorf("unknown connect log level %q", argv[i])
			}
			args.LogLevel = logLevel
		default:
			if strings.HasPrefix(argv[i], "-") {
				return ConnectArgs{}, fmt.Errorf("unknown connect option %q", argv[i])
			}
			switch positional {
			case 0:
				if args.RoomName != "" {
					return ConnectArgs{}, fmt.Errorf("room specified more than once")
				}
				args.RoomName = argv[i]
			case 1:
				args.ParticipantIdentity = argv[i]
			default:
				return ConnectArgs{}, fmt.Errorf("unexpected connect argument %q", argv[i])
			}
			positional++
		}
	}
	if args.RoomName == "" {
		return ConnectArgs{}, fmt.Errorf("missing room name")
	}
	return args, nil
}

func applyConnectArgs(server *worker.AgentServer, args ConnectArgs) error {
	return server.UpdateOptions(worker.WorkerOptions{
		LogLevel:  args.LogLevel,
		WSURL:     args.URL,
		APIKey:    args.APIKey,
		APISecret: args.APISecret,
		DevMode:   true,
	})
}

func parseConsoleArgs(argv []string) (ConsoleArgs, error) {
	args := ConsoleArgs{Mode: ConsoleModeAudio, LogLevel: "DEBUG"}
	for i := 2; i < len(argv); i++ {
		switch argv[i] {
		case "--text":
			args.Mode = ConsoleModeText
		case "--record":
			args.Record = true
		case "--list-devices":
			args.ListDevices = true
		case "--input-device":
			i++
			if i >= len(argv) {
				return ConsoleArgs{}, fmt.Errorf("missing value for --input-device")
			}
			args.InputDevice = argv[i]
		case "--output-device":
			i++
			if i >= len(argv) {
				return ConsoleArgs{}, fmt.Errorf("missing value for --output-device")
			}
			args.OutputDevice = argv[i]
		case "--log-level":
			i++
			if i >= len(argv) {
				return ConsoleArgs{}, fmt.Errorf("missing value for --log-level")
			}
			logLevel := strings.ToUpper(argv[i])
			if !validConsoleLogLevel(logLevel) {
				return ConsoleArgs{}, fmt.Errorf("unknown console log level %q", argv[i])
			}
			args.LogLevel = logLevel
		default:
			return ConsoleArgs{}, fmt.Errorf("unknown console option %q", argv[i])
		}
	}
	return args, nil
}

func validConsoleLogLevel(logLevel string) bool {
	switch logLevel {
	case "TRACE", "DEBUG", "INFO", "WARN", "ERROR", "CRITICAL":
		return true
	default:
		return false
	}
}

func defaultConnectParticipantIdentity() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "agent-local"
	}
	return "agent-" + hex.EncodeToString(b[:])
}

func consoleLocalJobArgs() (roomName string, participantIdentity string) {
	return "console-room", "console"
}

func consoleSessionReportPath(now time.Time) string {
	return filepath.Join(
		"console-recordings",
		"session-"+now.Format("01-02-150405"),
		"session_report.json",
	)
}

func consoleLocalJobOptions(args ConsoleArgs) worker.LocalJobOptions {
	options := worker.LocalJobOptions{FakeJob: true}
	if args.Record {
		options.RecordingOptions = agent.RecordingOptions{
			Audio:      true,
			Traces:     true,
			Logs:       true,
			Transcript: true,
		}
		options.SessionReportPath = consoleSessionReportPath(time.Now())
	}
	return options
}

func readConsoleInput(r io.Reader) (string, error) {
	reader := bufio.NewReader(r)
	line, err := reader.ReadString('\n')
	if err != nil && len(line) == 0 {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func consoleInputIsEmpty(input string) bool {
	return strings.TrimSpace(input) == ""
}

func startConsoleAudioUI(ctx context.Context, args ConsoleArgs) (func(), error) {
	if args.Mode == ConsoleModeText {
		return func() {}, nil
	}

	audioIO := consoleui.NewAudioIO()
	audioIO.SetDevices(args.InputDevice, args.OutputDevice)
	if err := audioIO.Start(ctx); err != nil {
		return nil, err
	}

	go func() {
		if _, err := tea.NewProgram(consoleui.NewConsoleModel(ctx, audioIO)).Run(); err != nil {
			logger.Logger.Errorw("Console UI error", err)
		}
	}()

	return func() {
		_ = audioIO.Stop()
	}, nil
}

func runConsole(server *worker.AgentServer, argv []string) {
	args, err := parseConsoleArgs(argv)
	if err != nil {
		fmt.Println("Usage: worker console [--text] [--record] [--input-device <device>] [--output-device <device>]")
		os.Exit(1)
	}

	if args.ListDevices {
		printConsoleAudioDevices()
		return
	}
	localJobOptions := consoleLocalJobOptions(args)

	fmt.Println("Starting console mode 🚀")
	if localJobOptions.SessionReportPath != "" {
		fmt.Printf("Session recording will be saved to %s\n", filepath.Dir(localJobOptions.SessionReportPath))
	}
	fmt.Println("Type your message and press Enter to talk to the agent. Press Ctrl+C to exit.")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	stopAudio, err := startConsoleAudioUI(ctx, args)
	if err != nil {
		logger.Logger.Errorw("Console audio startup error", err)
		stop()
		return
	}
	defer stopAudio()

	logger.Logger.Infow(
		"Starting console local job",
		"mode", args.Mode,
		"record", args.Record,
		"inputDevice", args.InputDevice,
		"outputDevice", args.OutputDevice,
		"logLevel", args.LogLevel,
	)

	go func() {
		roomName, participantIdentity := consoleLocalJobArgs()
		if err := server.ExecuteLocalJobWithOptions(ctx, roomName, participantIdentity, localJobOptions); err != nil {
			logger.Logger.Errorw("Console execution error", err)
			stop()
		}
	}()

	// Console read loop
	go func() {
		reader := bufio.NewReader(os.Stdin)
		for {
			fmt.Print("❯ ")
			input, err := readConsoleInput(reader)
			if err != nil {
				break
			}
			if !consoleInputIsEmpty(input) {
				logger.Logger.Infow("User input received", "input", input)
				if session := server.GetConsoleSession(); session != nil {
					// We use type assertion via a local interface to avoid tight coupling if preferred,
					// or we can just rely on the entrypoint to handle console input if we set a callback.
					// Since Go's type system requires knowing the type, we define an interface here.
					type ReplyGenerator interface {
						GenerateReply(ctx context.Context, userInput string) (*agent.SpeechHandle, error)
					}
					if rg, ok := session.(ReplyGenerator); ok {
						if _, err := rg.GenerateReply(context.Background(), input); err != nil {
							logger.Logger.Errorw("Failed to generate reply", err)
						}
					} else {
						logger.Logger.Warnw("Active session does not support text input", nil)
					}
				}
			}
		}
	}()

	<-ctx.Done()
}
