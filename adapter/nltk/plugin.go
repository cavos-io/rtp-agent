package nltk

const (
	PluginTitle   = "rtp-agent.plugins.nltk"
	PluginVersion = "1.5.15"
	PluginPackage = "rtp-agent.plugins.nltk"
)

type Plugin struct{}

func (Plugin) DownloadFiles() error {
	return nil
}
