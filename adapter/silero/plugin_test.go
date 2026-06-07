package silero

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSileroPluginDownloadFilesUsesResourcesModels(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	var gotURL string
	var gotPath string
	oldDownload := downloadSileroModelFile
	downloadSileroModelFile = func(url, path string) error {
		gotURL = url
		gotPath = path
		return os.WriteFile(path, []byte("model"), 0o600)
	}
	t.Cleanup(func() { downloadSileroModelFile = oldDownload })

	p := Plugin{}
	if err := p.DownloadFiles(); err != nil {
		t.Fatalf("DownloadFiles() error = %v", err)
	}

	wantPath := filepath.Join(dir, "resources", "models", "silero_vad.onnx")
	if gotPath != wantPath {
		t.Fatalf("download path = %q, want %q", gotPath, wantPath)
	}
	if !strings.Contains(gotURL, "githubusercontent.com/snakers4/silero-vad/") {
		t.Fatalf("download URL = %q, want direct Silero upstream URL", gotURL)
	}
	if data, err := os.ReadFile(wantPath); err != nil {
		t.Fatalf("ReadFile(%q) error = %v", wantPath, err)
	} else if string(data) != "model" {
		t.Fatalf("downloaded model = %q, want model", data)
	}
}

func TestSileroPluginDownloadFilesSkipsExistingModel(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	modelPath := filepath.Join(dir, "resources", "models", "silero_vad.onnx")
	if err := os.MkdirAll(filepath.Dir(modelPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(modelPath, []byte("existing"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	oldDownload := downloadSileroModelFile
	downloadSileroModelFile = func(url, path string) error {
		t.Fatalf("download called for existing model: url=%q path=%q", url, path)
		return nil
	}
	t.Cleanup(func() { downloadSileroModelFile = oldDownload })

	p := Plugin{}
	if err := p.DownloadFiles(); err != nil {
		t.Fatalf("DownloadFiles() error = %v", err)
	}
}
