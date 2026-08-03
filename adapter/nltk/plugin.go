package nltk

const (
	PluginTitle   = "rtp-agent.plugins.nltk"
	PluginVersion = "v0.4.3"
	PluginPackage = "rtp-agent.plugins.nltk"
)

type Plugin struct{}

func (Plugin) DownloadFiles() error {
	return nil
}
