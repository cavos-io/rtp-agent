package turndetector

import "errors"

const (
	PluginTitle   = "rtp-agent.plugins.turn_detector"
	PluginVersion = "1.5.15"
	PluginPackage = "rtp-agent.plugins.turn_detector"
)

type Plugin struct{}

func (Plugin) DownloadFiles() error {
	return errors.New("turn-detector Hugging Face ONNX model download is not implemented")
}
