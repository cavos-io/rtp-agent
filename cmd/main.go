package main

import (
	"context"
	"fmt"

	"github.com/cavos-io/rtp-agent/app"
	"github.com/cavos-io/rtp-agent/interface/cli"
)

func main() {
	rtpApp, err := app.Init(app.DefaultConfigFromEnv())
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
