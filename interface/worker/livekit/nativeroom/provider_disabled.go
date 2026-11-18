//go:build !nativeinprocess

package nativeroom

import (
	"errors"
	"github.com/cavos-io/rtp-agent/interface/worker"
)

func NewProvider(Config, string, string) (worker.SessionTransportProvider, error) {
	return nil, errors.New("native transport requires building with -tags nativeinprocess")
}
