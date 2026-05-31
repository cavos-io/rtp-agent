package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/cavos-io/conversation-worker/core/agent"
	"github.com/cavos-io/conversation-worker/interface/worker"
	"github.com/cavos-io/conversation-worker/library/logger"
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
}

func RunApp(server *worker.AgentServer) {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "start":
		runWorker(server, false)
	case "dev":
		if err := RunWithDevMode(os.Args); err != nil {
			logger.Logger.Errorw("Dev mode error", err)
			os.Exit(1)
		}
	case "connect":
		runConnect(server)
	case "console":
		runConsole(server)
	case "download-files":
		runDownloadFiles()
	default:
		printUsage()
		os.Exit(1)
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
}

func runDownloadFiles() {
	plugins := agent.RegisteredPlugins()
	fmt.Printf("Downloading files for %d registered plugins...\n", len(plugins))
	for _, p := range plugins {
		fmt.Printf("Downloading for %s (%s)...\n", p.Title(), p.Package())
		if err := p.DownloadFiles(); err != nil {
			logger.Logger.Errorw("Failed to download files", err, "plugin", p.Title())
		}
	}
	fmt.Println("Finished downloading files.")
}

func runWorker(server *worker.AgentServer, devMode bool) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Logger.Infow("Starting worker", "devMode", devMode)
	if err := server.Run(ctx); err != nil {
		logger.Logger.Errorw("Worker error", err)
		os.Exit(1)
	}
}

func runConnect(server *worker.AgentServer) {
	args, err := parseConnectArgs(os.Args)
	if err != nil {
		fmt.Println("Usage: worker connect <room_name> [participant_identity]")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Logger.Infow("Starting connect mode", "room", args.RoomName, "participant", args.ParticipantIdentity)

	if err := server.ExecuteLocalJob(ctx, args.RoomName, args.ParticipantIdentity); err != nil {
		logger.Logger.Errorw("Connect error", err)
		os.Exit(1)
	}
}

func parseConnectArgs(argv []string) (ConnectArgs, error) {
	if len(argv) < 3 {
		return ConnectArgs{}, fmt.Errorf("missing room name")
	}
	args := ConnectArgs{
		RoomName:            argv[2],
		ParticipantIdentity: defaultConnectParticipantIdentity(),
	}
	if len(argv) > 3 {
		args.ParticipantIdentity = argv[3]
	}
	return args, nil
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

func runConsole(server *worker.AgentServer) {
	fmt.Println("Starting console mode 🚀")
	fmt.Println("Type your message and press Enter to talk to the agent. Press Ctrl+C to exit.")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		roomName, participantIdentity := consoleLocalJobArgs()
		if err := server.ExecuteLocalJob(ctx, roomName, participantIdentity); err != nil {
			logger.Logger.Errorw("Console execution error", err)
			stop()
		}
	}()

	// Console read loop
	go func() {
		var input string
		for {
			fmt.Print("❯ ")
			_, err := fmt.Scanln(&input)
			if err != nil {
				break
			}
			if input != "" {
				logger.Logger.Infow("User input received", "input", input)
				if session := server.GetConsoleSession(); session != nil {
					// We use type assertion via a local interface to avoid tight coupling if preferred,
					// or we can just rely on the entrypoint to handle console input if we set a callback.
					// Since Go's type system requires knowing the type, we define an interface here.
					type ReplyGenerator interface {
						GenerateReply(ctx context.Context, userInput string) error
					}
					if rg, ok := session.(ReplyGenerator); ok {
						if err := rg.GenerateReply(context.Background(), input); err != nil {
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
