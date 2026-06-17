package agora

import (
	"strings"
	"time"

	rtctokenbuilder "github.com/AgoraIO/Tools/DynamicKey/AgoraDynamicKey/go/src/rtctokenbuilder2"
)

const defaultTokenTTL = time.Hour

func ResolveJoinOptions(opts Options) (Options, error) {
	if err := opts.Validate(); err != nil {
		return Options{}, err
	}
	opts.AppID = strings.TrimSpace(opts.AppID)
	opts.AppCertificate = strings.TrimSpace(opts.AppCertificate)
	opts.Channel = strings.TrimSpace(opts.Channel)
	opts.UID = strings.TrimSpace(opts.UID)
	opts.Token = strings.TrimSpace(opts.Token)
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
		return Options{}, err
	}
	opts.Token = token
	return opts, nil
}
