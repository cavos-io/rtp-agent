package browser

const (
	PluginTitle   = "rtp-agent.plugins.browser"
	PluginVersion = "1.5.15"
	PluginPackage = "rtp-agent.plugins.browser"
)

type Plugin struct{}

func (Plugin) DownloadFiles() error {
	return nil
}
