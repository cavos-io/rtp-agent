package clova

const (
	PluginTitle   = "rtp-agent.plugins.clova"
	PluginVersion = "1.5.15"
	PluginPackage = "rtp-agent.plugins.clova"
)

type Plugin struct{}

func (Plugin) DownloadFiles() error {
	return nil
}
