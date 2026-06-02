package main

import (
	"github.com/cavos-io/rtp-agent/app"
	"github.com/cavos-io/rtp-agent/interface/cli"
)

func main() {
	rtpApp, err := app.Init(app.DefaultConfigFromEnv())
	if err != nil {
		panic(err)
	}
	cli.RunApp(rtpApp.Server)
}
