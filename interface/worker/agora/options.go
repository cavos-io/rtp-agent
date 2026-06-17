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
	Token          string
	PublishAudio   *bool
	SubscribeAudio *bool
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
