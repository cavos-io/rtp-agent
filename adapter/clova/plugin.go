package clova

const (
	PluginTitle   = "rtp-agent.plugins.clova"
	PluginVersion = "v0.1.8"
	PluginPackage = "rtp-agent.plugins.clova"
)

type Plugin struct{}

func (Plugin) DownloadFiles() error {
	return nil
}
