package agora

import (
	"strings"
	"time"

	rtctokenbuilder "github.com/AgoraIO/Tools/DynamicKey/AgoraDynamicKey/go/src/rtctokenbuilder2"
)

const defaultTokenTTL = 24 * time.Hour

func ResolveJoinOptions(opts Options) (Options, error) {
	if err := opts.Validate(); err != nil {
		return Options{}, err
	}
	opts.AppID = strings.TrimSpace(opts.AppID)
	opts.AppCertificate = strings.TrimSpace(opts.AppCertificate)
	opts.Channel = strings.TrimSpace(opts.Channel)
	opts.UID = strings.TrimSpace(opts.UID)
	opts.RemoteStreamID = strings.TrimSpace(opts.RemoteStreamID)
	opts.Token = strings.TrimSpace(opts.Token)
	if opts.PublishAudio == nil {
		enabled := true
		opts.PublishAudio = &enabled
	}
	if opts.SubscribeAudio == nil {
		enabled := true
		opts.SubscribeAudio = &enabled
	}
	if strings.TrimSpace(opts.UID) == "" {
		opts.UID = "0"
	}
	if opts.Token == "" && opts.AppCertificate == "" {
		opts.Token = opts.AppID
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

func PublishAudioEnabled(value *bool) bool {
	return value == nil || *value
}

func SubscribeAudioEnabled(value *bool) bool {
	return value == nil || *value
}

func acceptRemoteStream(remoteStreamID, userID string) bool {
	remoteStreamID = strings.TrimSpace(remoteStreamID)
	if remoteStreamID == "" {
		return true
	}
	return strings.TrimSpace(userID) == remoteStreamID
}

func acceptChannel(configuredChannel, eventChannel string) bool {
	eventChannel = strings.TrimSpace(eventChannel)
	if eventChannel == "" {
		return true
	}
	return eventChannel == strings.TrimSpace(configuredChannel)
}
