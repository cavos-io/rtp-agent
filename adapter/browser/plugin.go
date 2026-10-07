package browser

const (
	PluginTitle   = "rtp-agent.plugins.browser"
	PluginVersion = "v0.8.12"
	PluginPackage = "rtp-agent.plugins.browser"
)

type Plugin struct{}

func (Plugin) DownloadFiles() error {
	return nil
}
