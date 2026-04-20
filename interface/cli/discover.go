package cli

import (
	"github.com/cavos-io/rtp-agent/library/logger"
)

// In Python, this dynamically imports plugins.
// In Go, since it is a compiled language, plugins are imported anonymously in main.go
// (e.g., _ "github.com/cavos-io/rtp-agent/adapter/openai").
// This function exists for structural parity.
func DiscoverPlugins() {
	logger.Logger.Debugw("Discovering plugins (compile-time in Go)")
	// Implement plugin registry checking here if a dynamic plugin system is added later.
}

