package ultravox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUltravoxAdapterHasNoProductionSurfaceWithoutRealtimeModel(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir adapter/ultravox: %v", err)
	}

	var productionFiles []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) == ".go" && !strings.HasSuffix(name, "_test.go") {
			productionFiles = append(productionFiles, name)
		}
	}
	if len(productionFiles) != 0 {
		t.Fatalf("Ultravox production files = %v, want none until a real realtime adapter exists", productionFiles)
	}
}
