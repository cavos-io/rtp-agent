package agora

import (
	"context"
	"strings"
	"time"

	rtctokenbuilder "github.com/AgoraIO/Tools/DynamicKey/AgoraDynamicKey/go/src/rtctokenbuilder2"
)

type DataPublisher interface {
	PublishData(context.Context, []byte) error
	Close(context.Context) error
}

func ResolveDataOptions(opts Options) (Options, error) {
	if err := opts.Validate(); err != nil {
		return Options{}, err
	}
	opts.AppID = strings.TrimSpace(opts.AppID)
	opts.AppCertificate = strings.TrimSpace(opts.AppCertificate)
	opts.Channel = strings.TrimSpace(opts.Channel)
	opts.UID = strings.TrimSpace(opts.UID)
	opts.RemoteStreamID = strings.TrimSpace(opts.RemoteStreamID)
	opts.Token = strings.TrimSpace(opts.Token)
	opts.RTMUserID = strings.TrimSpace(opts.RTMUserID)
	opts.RTMToken = strings.TrimSpace(opts.RTMToken)
	rtcUID := opts.UID
	if opts.RTMUserID != "" {
		opts.UID = opts.RTMUserID
	}
	if opts.RTMToken != "" {
		opts.Token = opts.RTMToken
	} else if opts.RTMUserID != "" && opts.RTMUserID != rtcUID && opts.AppCertificate != "" {
		opts.Token = ""
	}
	if opts.UID == "" {
		opts.UID = "0"
	}
	if opts.Token == "" && opts.AppCertificate == "" {
		opts.Token = opts.AppID
	}
	if opts.Token == "" && opts.AppCertificate != "" {
		tokenTTL := uint32(defaultTokenTTL / time.Second)
		token, err := rtctokenbuilder.BuildTokenWithRtm(
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
	}
	return opts, nil
}
