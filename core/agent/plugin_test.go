package agent

import (
	"testing"
)

type mockPlugin struct {
	title string
}

func (m *mockPlugin) Title() string         { return m.title }
func (m *mockPlugin) Version() string       { return "1.0.0" }
func (m *mockPlugin) Package() string       { return "mock" }
func (m *mockPlugin) DownloadFiles() error { return nil }

func TestPluginRegistration(t *testing.T) {
	p := &mockPlugin{title: "Test Plugin"}
	RegisterPlugin(p)
	
	found := false
	for _, registered := range RegisteredPlugins() {
		if registered.Title() == "Test Plugin" {
			found = true
			break
		}
	}
	
	if !found {
		t.Error("Registered plugin not found in RegisteredPlugins()")
	}
}
