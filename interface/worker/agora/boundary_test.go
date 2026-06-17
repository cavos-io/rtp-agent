package agora

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgoraProductionCodeDoesNotImportSharedWorkerPackage(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob agora files: %v", err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if strings.Contains(string(data), `"github.com/cavos-io/rtp-agent/interface/worker"`) {
			t.Fatalf("%s imports shared worker package; keep Agora independent from LiveKit-heavy worker internals", file)
		}
	}
}
