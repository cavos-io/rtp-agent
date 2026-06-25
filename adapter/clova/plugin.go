package clova

const (
	PluginTitle   = "rtp-agent.plugins.clova"
	PluginVersion = "v0.1.0"
	PluginPackage = "rtp-agent.plugins.clova"
)

type Plugin struct{}

func (Plugin) DownloadFiles() error {
	return nil
}
