package clova

const (
	PluginTitle   = "rtp-agent.plugins.clova"
	PluginVersion = "v0.5.1"
	PluginPackage = "rtp-agent.plugins.clova"
)

type Plugin struct{}

func (Plugin) DownloadFiles() error {
	return nil
}
