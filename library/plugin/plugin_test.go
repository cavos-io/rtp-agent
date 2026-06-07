package plugin

import (
	"errors"
	"testing"
)

func TestRegisterPluginDownloaderUsesProvidedDownloadFunction(t *testing.T) {
	pluginsMu.Lock()
	oldPlugins := plugins
	plugins = nil
	pluginsMu.Unlock()
	t.Cleanup(func() {
		pluginsMu.Lock()
		plugins = oldPlugins
		pluginsMu.Unlock()
	})

	wantErr := errors.New("download failed")
	var called bool
	RegisterPluginDownloader("title", "version", "package", func() error {
		called = true
		return wantErr
	})

	registered := RegisteredPlugins()
	if len(registered) != 1 {
		t.Fatalf("RegisteredPlugins length = %d, want 1", len(registered))
	}
	if registered[0].Title() != "title" || registered[0].Version() != "version" || registered[0].Package() != "package" {
		t.Fatalf("registered metadata = %q/%q/%q, want title/version/package", registered[0].Title(), registered[0].Version(), registered[0].Package())
	}
	if err := registered[0].DownloadFiles(); !errors.Is(err, wantErr) {
		t.Fatalf("DownloadFiles() error = %v, want %v", err, wantErr)
	}
	if !called {
		t.Fatal("download function was not called")
	}
}
