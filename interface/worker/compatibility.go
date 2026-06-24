package worker

import (
	"fmt"
	"strings"
)

const (
	defaultRuntimeName    = "rtp-agent"
	defaultRuntimeVersion = "0.1.0"

	CompatibilityProfileLiveKitConsole152Basic = "livekit-console-1.5.2-basic"
	CompatibilityProfileAgoraRTCBasic          = "agora-rtc-basic"

	CompatibilityFamilyLiveKitAgentsPython = "livekit-agents-python"
	CompatibilityFamilyAgoraRTCTransport   = "agora-rtc-transport"

	CompatibilityCapabilityWorkerRegistration     = "worker_registration"
	CompatibilityCapabilityJobLifecycle           = "job_lifecycle"
	CompatibilityCapabilityWorkerMetadata         = "worker_metadata"
	CompatibilityCapabilityRoomIOBasic            = "room_io_basic"
	CompatibilityCapabilitySessionMetricsBasic    = "session_metrics_basic"
	CompatibilityCapabilityAgoraChannelJoin       = "agora_channel_join"
	CompatibilityCapabilityAgoraPCMAudioIn        = "agora_pcm_audio_in"
	CompatibilityCapabilityAgoraPCMAudioOut       = "agora_pcm_audio_out"
	CompatibilityCapabilityAgoraRTMTextIn         = "agora_rtm_text_in"
	CompatibilityCapabilityAgoraTranscriptPublish = "agora_transcript_publish"
)

type CompatibilityInfo struct {
	RuntimeName       string
	RuntimeVersion    string
	Transport         WorkerTransport
	Profile           string
	AdvertisedFamily  string
	AdvertisedVersion string
	Capabilities      []string
}

type CompatibilityResolveOptions struct {
	Transport                 WorkerTransport
	RuntimeVersion            string
	Profile                   string
	AdvertisedVersionOverride string
}

type compatibilityProfileDefinition struct {
	transport         WorkerTransport
	profile           string
	advertisedFamily  string
	advertisedVersion string
	capabilities      []string
}

func ResolveCompatibility(opts CompatibilityResolveOptions) (CompatibilityInfo, error) {
	transport := NormalizeWorkerTransport(string(opts.Transport))
	if err := ValidateWorkerTransport(transport); err != nil {
		return CompatibilityInfo{}, err
	}
	profile := strings.TrimSpace(opts.Profile)
	if profile == "" {
		profile = defaultCompatibilityProfile(transport)
	}
	def, ok := compatibilityProfiles[profile]
	if !ok {
		return CompatibilityInfo{}, fmt.Errorf("unknown worker compatibility profile %q", profile)
	}
	if def.transport != transport {
		return CompatibilityInfo{}, fmt.Errorf("worker compatibility profile %q is for transport %q, not %q", profile, def.transport, transport)
	}
	runtimeVersion := strings.TrimSpace(opts.RuntimeVersion)
	if runtimeVersion == "" {
		runtimeVersion = defaultRuntimeVersion
	}
	advertisedVersion := def.advertisedVersion
	if override := strings.TrimSpace(opts.AdvertisedVersionOverride); override != "" {
		advertisedVersion = override
	}
	return CompatibilityInfo{
		RuntimeName:       defaultRuntimeName,
		RuntimeVersion:    runtimeVersion,
		Transport:         transport,
		Profile:           def.profile,
		AdvertisedFamily:  def.advertisedFamily,
		AdvertisedVersion: advertisedVersion,
		Capabilities:      append([]string(nil), def.capabilities...),
	}, nil
}

func defaultCompatibilityProfile(transport WorkerTransport) string {
	switch NormalizeWorkerTransport(string(transport)) {
	case WorkerTransportAgora:
		return CompatibilityProfileAgoraRTCBasic
	default:
		return CompatibilityProfileLiveKitConsole152Basic
	}
}

func hasCompatibilityCapability(info CompatibilityInfo, capability string) bool {
	for _, got := range info.Capabilities {
		if got == capability {
			return true
		}
	}
	return false
}

var compatibilityProfiles = map[string]compatibilityProfileDefinition{
	CompatibilityProfileLiveKitConsole152Basic: {
		transport:         WorkerTransportLiveKit,
		profile:           CompatibilityProfileLiveKitConsole152Basic,
		advertisedFamily:  CompatibilityFamilyLiveKitAgentsPython,
		advertisedVersion: "1.5.2",
		capabilities: []string{
			CompatibilityCapabilityWorkerRegistration,
			CompatibilityCapabilityJobLifecycle,
			CompatibilityCapabilityWorkerMetadata,
			CompatibilityCapabilityRoomIOBasic,
			CompatibilityCapabilitySessionMetricsBasic,
		},
	},
	CompatibilityProfileAgoraRTCBasic: {
		transport:         WorkerTransportAgora,
		profile:           CompatibilityProfileAgoraRTCBasic,
		advertisedFamily:  CompatibilityFamilyAgoraRTCTransport,
		advertisedVersion: defaultRuntimeVersion,
		capabilities: []string{
			CompatibilityCapabilityAgoraChannelJoin,
			CompatibilityCapabilityAgoraPCMAudioIn,
			CompatibilityCapabilityAgoraPCMAudioOut,
			CompatibilityCapabilityAgoraRTMTextIn,
			CompatibilityCapabilityAgoraTranscriptPublish,
		},
	},
}
