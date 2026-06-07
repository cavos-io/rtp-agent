package main

import (
	"context"
	"fmt"

	rawfunction "github.com/cavos-io/rtp-agent/examples/voice_agents/raw_function_description/rawfunction"
	"github.com/cavos-io/rtp-agent/interface/cli"
)

func main() {
	rtpApp, err := rawfunction.NewApp(rawfunction.ConfigFromEnv())
	if err != nil {
		panic(err)
	}
	defer rtpApp.Close(context.Background())

	cli.RunApp(rtpApp.Server, func(ctx context.Context) (string, error) {
		summary, err := rtpApp.EvaluateSession(ctx, nil)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(
			"score=%.2f all_passed=%t any_passed=%t majority_passed=%t none_failed=%t\n",
			summary.Score,
			summary.AllPassed,
			summary.AnyPassed,
			summary.MajorityPassed,
			summary.NoneFailed,
		), nil
	})
}
