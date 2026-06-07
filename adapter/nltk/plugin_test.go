package nltk

import "testing"

func TestNltkPluginDownloadFilesIsGoNativeNoop(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.nltk" {
		t.Fatalf("plugin title = %q, want rtp-agent.plugins.nltk", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("plugin version = %q, want reference version", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.nltk" {
		t.Fatalf("plugin package = %q, want rtp-agent.plugins.nltk", PluginPackage)
	}
	if err := (Plugin{}).DownloadFiles(); err != nil {
		t.Fatalf("DownloadFiles() error = %v, want nil for Go-native tokenizer", err)
	}
}
