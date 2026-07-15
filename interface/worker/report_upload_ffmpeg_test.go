//go:build ffmpeg

package worker

import "testing"

func TestFFmpegBuildDisablesLiveKitReportUpload(t *testing.T) {
	if err := uploadSessionReport("://invalid", "key", "secret", "agent", nil); err != nil {
		t.Fatalf("disabled uploadSessionReport() error = %v, want nil", err)
	}
}
