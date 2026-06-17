package agora

import (
	"fmt"
	"strings"
)

type Options struct {
	AppID          string
	AppCertificate string
	Channel        string
	UID            string
	RemoteStreamID string
	Token          string
	RTMUserID      string
	RTMToken       string
	PublishAudio   *bool
	SubscribeAudio *bool
	PublishData    *bool
}

func (opts Options) Validate() error {
	if strings.TrimSpace(opts.AppID) == "" {
		return fmt.Errorf("AGORA_APP_ID is required for agora worker transport")
	}
	if strings.TrimSpace(opts.Channel) == "" {
		return fmt.Errorf("AGORA_CHANNEL is required for agora worker transport")
	}
	return nil
}

func PublishDataEnabled(value *bool) bool {
	return value != nil && *value
}
