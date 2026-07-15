//go:build !ffmpeg

package worker

import "github.com/cavos-io/rtp-agent/core/agent"

var uploadSessionReport = agent.UploadSessionReport
