//go:build ffmpeg

package worker

import "github.com/cavos-io/rtp-agent/core/agent"

var uploadSessionReport = func(string, string, string, string, *agent.SessionReport) error {
	return nil
}
