package pipecat

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPipecatPluginDownloadFilesDownloadsSmartTurnCPUModel(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	var gotURL string
	downloadPipecatModelFile = func(url string, path string) error {
		gotURL = url
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte("onnx"), 0o600)
	}
	t.Cleanup(func() { downloadPipecatModelFile = downloadFile })

	err := (Plugin{}).DownloadFiles()

	if err != nil {
		t.Fatalf("DownloadFiles error = %v", err)
	}
	if gotURL != smartTurnCPUModelURL {
		t.Fatalf("download URL = %q, want %q", gotURL, smartTurnCPUModelURL)
	}
	path, err := smartTurnCPUModelPath()
	if err != nil {
		t.Fatalf("smartTurnCPUModelPath error = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("downloaded model stat error = %v", err)
	}
}

func TestPipecatPluginDownloadFilesSkipsExistingModel(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	path, err := smartTurnCPUModelPath()
	if err != nil {
		t.Fatalf("smartTurnCPUModelPath error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	downloadPipecatModelFile = func(string, string) error {
		t.Fatal("download called for existing model")
		return nil
	}
	t.Cleanup(func() { downloadPipecatModelFile = downloadFile })

	if err := (Plugin{}).DownloadFiles(); err != nil {
		t.Fatalf("DownloadFiles error = %v", err)
	}
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir error = %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("restore Chdir error = %v", err)
		}
	})
}
