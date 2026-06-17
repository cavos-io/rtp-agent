package agora

import (
	"context"
	"strings"
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
	if opts.RTMUserID != "" {
		opts.UID = opts.RTMUserID
	}
	if opts.RTMToken != "" {
		opts.Token = opts.RTMToken
	}
	if opts.UID == "" {
		opts.UID = "0"
	}
	return opts, nil
}
