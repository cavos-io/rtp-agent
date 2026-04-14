package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cavos-io/conversation-worker/core/agent"
	"github.com/cavos-io/conversation-worker/interface/cli/console"
	"github.com/cavos-io/conversation-worker/interface/worker"
	"github.com/cavos-io/conversation-worker/library/logger"
)

type CliArgs struct {
	LogLevel  string
	URL       string
	APIKey    string
	APISecret string
	DevMode   bool
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
	if len(os.Args) < 3 {
		fmt.Println("Usage: worker connect <room_name> [participant_identity]")
		os.Exit(1)
	}
	roomName := os.Args[2]
	participantIdentity := "user"
	if len(os.Args) > 3 {
		participantIdentity = os.Args[3]
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Logger.Infow("Starting connect mode", "room", roomName, "participant", participantIdentity)

	if err := server.ExecuteLocalJob(ctx, roomName, participantIdentity); err != nil {
		logger.Logger.Errorw("Connect error", err)
		os.Exit(1)
	}
}

func runConsole(server *worker.AgentServer) {
	fmt.Println("Starting console mode 🚀")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	audioIO := console.NewAudioIO()
	if err := audioIO.Start(ctx); err != nil {
		logger.Logger.Errorw("Failed to start local audio", err)
		return
	}
	defer audioIO.Stop()

	go func() {
		// Wait for session to be registered
		for i := 0; i < 50; i++ {
			if s := server.GetConsoleSession(); s != nil {
				if agentSession, ok := s.(*agent.AgentSession); ok {
					agentSession.Input.Audio = audioIO
					agentSession.Output.Audio = audioIO
					break
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()

	go func() {
		if err := server.ExecuteLocalJob(ctx, "console-room", "console-user"); err != nil {
			logger.Logger.Errorw("Console execution error", err)
			stop()
		}
	}()

	m := console.NewConsoleModel(ctx, audioIO)
	p := tea.NewProgram(m, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Printf("Alas, there's been an error: %v", err)
		os.Exit(1)
	}
}
