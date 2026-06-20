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
	RTMEnabled     *bool
}

func (opts Options) Validate() error {
	if strings.TrimSpace(opts.AppID) == "" {
		return fmt.Errorf("AGORA_APP_ID is required for agora worker transport")
	}
	channel := strings.TrimSpace(opts.Channel)
	if channel == "" {
		return fmt.Errorf("AGORA_CHANNEL is required for agora worker transport")
	}
	if err := validateChannelName(channel); err != nil {
		return fmt.Errorf("AGORA_CHANNEL is invalid for agora worker transport: %w", err)
	}
	return nil
}

func validateChannelName(channel string) error {
	if len(channel) > 100 {
		return fmt.Errorf("channel name too long")
	}
	if strings.Contains(channel, "..") ||
		strings.Contains(channel, "/") ||
		strings.Contains(channel, "\\") ||
		strings.Contains(channel, "\x00") {
		return fmt.Errorf("channel name contains invalid characters")
	}
	if strings.HasPrefix(channel, ".") {
		return fmt.Errorf("channel name cannot start with dot")
	}
	return nil
}

func PublishDataEnabled(value *bool) bool {
	return value != nil && *value
}

func DataEnabled(opts Options) bool {
	if PublishDataEnabled(opts.PublishData) || PublishDataEnabled(opts.RTMEnabled) {
		return true
	}
	if opts.RTMEnabled != nil {
		return false
	}
	if opts.PublishData != nil {
		return false
	}
	return true
}
