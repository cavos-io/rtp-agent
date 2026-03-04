package main

import (
	"fmt"

	"github.com/cavos-io/conversation-worker/interface/cli"
	"github.com/cavos-io/conversation-worker/interface/worker"
)

func main() {
	opts := worker.WorkerOptions{
		AgentName:  "example-agent",
		WorkerType: worker.WorkerTypeRoom,
	}

	server := worker.NewAgentServer(opts)

	server.RTCSession(func(ctx *worker.JobContext) error {
		fmt.Println("Agent session started!")
		return nil
	}, nil, nil)

	cli.RunApp(server)
}
