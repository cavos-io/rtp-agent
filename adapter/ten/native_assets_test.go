package ten

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeRuntimeAssetsAreProductionOwned(t *testing.T) {
	source, err := os.ReadFile("native_cgo.go")
	if err != nil {
		t.Fatalf("ReadFile(native_cgo.go) error = %v", err)
	}
	if strings.Contains(string(source), "refs/ten-vad") {
		t.Fatal("native_cgo.go references refs/ten-vad; production images do not include refs")
	}

	for _, path := range []string{
		filepath.Join("native", "linux_amd64", "include", "ten_vad.h"),
		filepath.Join("native", "linux_amd64", "lib", "libten_vad.so"),
		filepath.Join("native", "linux_amd64", "LICENSE"),
		filepath.Join("native", "linux_amd64", "NOTICES"),
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat(%q) error = %v", path, err)
		}
		if info.IsDir() {
			t.Fatalf("%q is a directory, want runtime asset file", path)
		}
	}
}
