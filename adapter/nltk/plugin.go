package nltk

const (
	PluginTitle   = "rtp-agent.plugins.nltk"
	PluginVersion = "v0.8.5"
	PluginPackage = "rtp-agent.plugins.nltk"
)

type Plugin struct{}

func (Plugin) DownloadFiles() error {
	return nil
}
