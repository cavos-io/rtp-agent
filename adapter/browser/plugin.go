package browser

const (
	PluginTitle   = "rtp-agent.plugins.browser"
	PluginVersion = "v0.1.0"
	PluginPackage = "rtp-agent.plugins.browser"
)

type Plugin struct{}

func (Plugin) DownloadFiles() error {
	return nil
}
