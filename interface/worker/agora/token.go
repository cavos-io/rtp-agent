package agora

import (
	"strings"
	"time"

	rtctokenbuilder "github.com/AgoraIO/Tools/DynamicKey/AgoraDynamicKey/go/src/rtctokenbuilder2"
	"github.com/cavos-io/rtp-agent/interface/worker"
)

const defaultTokenTTL = time.Hour

func ResolveJoinOptions(opts worker.AgoraOptions) (worker.AgoraOptions, error) {
	if err := opts.Validate(); err != nil {
		return worker.AgoraOptions{}, err
	}
	if strings.TrimSpace(opts.UID) == "" {
		opts.UID = "0"
	}
	if strings.TrimSpace(opts.Token) != "" || strings.TrimSpace(opts.AppCertificate) == "" {
		return opts, nil
	}

	tokenTTL := uint32(defaultTokenTTL / time.Second)
	token, err := rtctokenbuilder.BuildTokenWithUserAccount(
		opts.AppID,
		opts.AppCertificate,
		opts.Channel,
		opts.UID,
		rtctokenbuilder.RolePublisher,
		tokenTTL,
		tokenTTL,
	)
	if err != nil {
		return worker.AgoraOptions{}, err
	}
	opts.Token = token
	return opts, nil
}
