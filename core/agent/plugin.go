package agent

import (
	"sync"

	"github.com/cavos-io/conversation-worker/library/logger"
)

type Plugin interface {
	Title() string
	Version() string
	Package() string
	DownloadFiles() error
}

var (
	plugins   = make([]Plugin, 0)
	pluginsMu sync.RWMutex
)

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
