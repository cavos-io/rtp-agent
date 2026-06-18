package agora

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgoraProductionCodeDoesNotImportSharedWorkerOrLiveKitPackages(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob agora files: %v", err)
	}
	forbidden := []string{
		`"github.com/cavos-io/rtp-agent/interface/worker"`,
		`"github.com/cavos-io/rtp-agent/interface/worker/livekit"`,
		`"github.com/livekit/`,
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, forbiddenImport := range forbidden {
			if strings.Contains(string(data), forbiddenImport) {
				t.Fatalf("%s imports %s; keep Agora independent from LiveKit-heavy worker internals", file, forbiddenImport)
			}
		}
	}
}
