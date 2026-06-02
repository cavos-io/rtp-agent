package plugin

import (
	"sync"

	"github.com/cavos-io/rtp-agent/library/logger"
)

type Plugin interface {
	Title() string
	Version() string
	Package() string
	DownloadFiles() error
}

type metadataPlugin struct {
	title       string
	version     string
	packageName string
}

func (p metadataPlugin) Title() string   { return p.title }
func (p metadataPlugin) Version() string { return p.version }
func (p metadataPlugin) Package() string { return p.packageName }
func (p metadataPlugin) DownloadFiles() error {
	return nil
}

var (
	plugins   = make([]Plugin, 0)
	pluginsMu sync.RWMutex
)

func RegisterPluginMetadata(title, version, packageName string) {
	RegisterPlugin(metadataPlugin{
		title:       title,
		version:     version,
		packageName: packageName,
	})
}

func RegisterPlugin(p Plugin) {
	pluginsMu.Lock()
	defer pluginsMu.Unlock()
	plugins = append(plugins, p)
	logger.Logger.Infow("Plugin registered", "title", p.Title(), "version", p.Version())
}

func RegisteredPlugins() []Plugin {
	pluginsMu.RLock()
	defer pluginsMu.RUnlock()
	cp := make([]Plugin, len(plugins))
	copy(cp, plugins)
	return cp
}
