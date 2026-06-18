package livekit_test

import (
	"context"
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lkprotocol "github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

func TestConnectInfoUsesAcceptedParticipantFields(t *testing.T) {
	info := workerlivekit.ConnectInfo(workerlivekit.ConnectInfoOptions{
		APIKey:              "key",
		APISecret:           "secret",
		RoomName:            "room-a",
		ParticipantName:     "Agent Name",
		ParticipantIdentity: "custom-agent",
		ParticipantMetadata: "custom-metadata",
		ParticipantAttributes: map[string]string{
			"tier": "gold",
		},
	})

	if info.APIKey != "key" {
		t.Fatalf("ConnectInfo.APIKey = %q, want key", info.APIKey)
	}
	if info.APISecret != "secret" {
		t.Fatalf("ConnectInfo.APISecret = %q, want secret", info.APISecret)
	}
	if info.RoomName != "room-a" {
		t.Fatalf("ConnectInfo.RoomName = %q, want room-a", info.RoomName)
	}
	if info.ParticipantIdentity != "custom-agent" {
		t.Fatalf("ConnectInfo.ParticipantIdentity = %q, want custom-agent", info.ParticipantIdentity)
	}
	if info.ParticipantName != "Agent Name" {
		t.Fatalf("ConnectInfo.ParticipantName = %q, want Agent Name", info.ParticipantName)
	}
	if info.ParticipantMetadata != "custom-metadata" {
		t.Fatalf("ConnectInfo.ParticipantMetadata = %q, want custom-metadata", info.ParticipantMetadata)
	}
	if info.ParticipantAttributes["tier"] != "gold" {
		t.Fatalf("ConnectInfo.ParticipantAttributes[tier] = %q, want gold", info.ParticipantAttributes["tier"])
	}
	if info.ParticipantKind != lksdk.ParticipantAgent {
		t.Fatalf("ConnectInfo.ParticipantKind = %v, want ParticipantAgent", info.ParticipantKind)
	}
}

func TestJobConnectInfoUsesJobRoomName(t *testing.T) {
	info := workerlivekit.JobConnectInfo(&lkprotocol.Job{
		Room: &lkprotocol.Room{Name: "room-a"},
	}, workerlivekit.ConnectInfoOptions{
		APIKey:              "key",
		APISecret:           "secret",
		ParticipantName:     "Agent Name",
		ParticipantIdentity: "custom-agent",
	})

	if info.RoomName != "room-a" {
		t.Fatalf("JobConnectInfo.RoomName = %q, want room-a", info.RoomName)
	}
	if info.ParticipantIdentity != "custom-agent" {
		t.Fatalf("JobConnectInfo.ParticipantIdentity = %q, want custom-agent", info.ParticipantIdentity)
	}
	if info.ParticipantKind != lksdk.ParticipantAgent {
		t.Fatalf("JobConnectInfo.ParticipantKind = %v, want ParticipantAgent", info.ParticipantKind)
	}
}

func TestConnectOptionsForAutoSubscribeBuildsSDKOptions(t *testing.T) {
	options := workerlivekit.ConnectOptionsForAutoSubscribe("audio_only")

	if len(options) != 1 {
		t.Fatalf("ConnectOptionsForAutoSubscribe() len = %d, want 1", len(options))
	}
	if options[0] == nil {
		t.Fatal("ConnectOptionsForAutoSubscribe()[0] = nil, want SDK option")
	}
}

func TestConnectOptionsOwnsAutoSubscribeMode(t *testing.T) {
	opts := workerlivekit.ConnectOptions{AutoSubscribe: workerlivekit.AutoSubscribeAudioOnly}

	if opts.AutoSubscribe != workerlivekit.AutoSubscribeAudioOnly {
		t.Fatalf("AutoSubscribe = %q, want %q", opts.AutoSubscribe, workerlivekit.AutoSubscribeAudioOnly)
	}
}

func TestConnectRoomUsesTokenConnectorWhenTokenPresent(t *testing.T) {
	wantRoom := lksdk.NewRoom(nil)
	calledWithToken := false
	room, err := workerlivekit.ConnectRoom(context.Background(), workerlivekit.RoomConnectOptions{
		URL:           "wss://livekit.example",
		Token:         "room-token",
		AutoSubscribe: "audio_only",
		Connector: workerlivekit.RoomConnector{
			ConnectWithToken: func(url string, token string, _ *lksdk.RoomCallback, options ...lksdk.ConnectOption) (*lksdk.Room, error) {
				calledWithToken = true
				if url != "wss://livekit.example" {
					t.Fatalf("ConnectWithToken url = %q, want wss://livekit.example", url)
				}
				if token != "room-token" {
					t.Fatalf("ConnectWithToken token = %q, want room-token", token)
				}
				if len(options) != 1 {
					t.Fatalf("ConnectWithToken options = %d, want 1", len(options))
				}
				return wantRoom, nil
			},
			Connect: func(string, lksdk.ConnectInfo, *lksdk.RoomCallback, ...lksdk.ConnectOption) (*lksdk.Room, error) {
				t.Fatal("Connect was called, want token connector")
				return nil, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("ConnectRoom() error = %v", err)
	}
	if !calledWithToken {
		t.Fatal("ConnectWithToken was not called")
	}
	if room != wantRoom {
		t.Fatal("ConnectRoom() room did not match connector room")
	}
}

func TestConnectRoomUsesJobConnectInfoWithoutToken(t *testing.T) {
	wantRoom := lksdk.NewRoom(nil)
	room, err := workerlivekit.ConnectRoom(context.Background(), workerlivekit.RoomConnectOptions{
		URL:           "wss://livekit.example",
		APIKey:        "key",
		APISecret:     "secret",
		Job:           &lkprotocol.Job{Room: &lkprotocol.Room{Name: "room-a"}},
		AutoSubscribe: "video_only",
		Accept: workerlivekit.ConnectInfoOptions{
			ParticipantName:     "Agent Name",
			ParticipantIdentity: "agent-a",
		},
		Connector: workerlivekit.RoomConnector{
			ConnectWithToken: func(string, string, *lksdk.RoomCallback, ...lksdk.ConnectOption) (*lksdk.Room, error) {
				t.Fatal("ConnectWithToken was called, want API key connector")
				return nil, nil
			},
			Connect: func(url string, info lksdk.ConnectInfo, _ *lksdk.RoomCallback, options ...lksdk.ConnectOption) (*lksdk.Room, error) {
				if url != "wss://livekit.example" {
					t.Fatalf("Connect url = %q, want wss://livekit.example", url)
				}
				if info.APIKey != "key" {
					t.Fatalf("ConnectInfo.APIKey = %q, want key", info.APIKey)
				}
				if info.APISecret != "secret" {
					t.Fatalf("ConnectInfo.APISecret = %q, want secret", info.APISecret)
				}
				if info.RoomName != "room-a" {
					t.Fatalf("ConnectInfo.RoomName = %q, want room-a", info.RoomName)
				}
				if info.ParticipantName != "Agent Name" {
					t.Fatalf("ConnectInfo.ParticipantName = %q, want Agent Name", info.ParticipantName)
				}
				if info.ParticipantIdentity != "agent-a" {
					t.Fatalf("ConnectInfo.ParticipantIdentity = %q, want agent-a", info.ParticipantIdentity)
				}
				if len(options) != 1 {
					t.Fatalf("Connect options = %d, want 1", len(options))
				}
				return wantRoom, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("ConnectRoom() error = %v", err)
	}
	if room != wantRoom {
		t.Fatal("ConnectRoom() room did not match connector room")
	}
}

func TestJoinPreparedRoomUsesExistingRoomWithJobConnectInfo(t *testing.T) {
	prepared := lksdk.NewRoom(nil)
	joined := false

	err := workerlivekit.JoinPreparedRoom(context.Background(), workerlivekit.PreparedRoomConnectOptions{
		Room:          prepared,
		URL:           "wss://livekit.example",
		APIKey:        "key",
		APISecret:     "secret",
		Job:           &lkprotocol.Job{Room: &lkprotocol.Room{Name: "room-a"}},
		AutoSubscribe: "audio_only",
		Accept: workerlivekit.ConnectInfoOptions{
			ParticipantName:     "Agent Name",
			ParticipantIdentity: "agent-a",
		},
		Connector: workerlivekit.RoomConnector{
			JoinWithToken: func(context.Context, *lksdk.Room, string, string, ...lksdk.ConnectOption) error {
				t.Fatal("JoinWithToken was called, want API key join")
				return nil
			},
			Join: func(joinCtx context.Context, room *lksdk.Room, url string, info lksdk.ConnectInfo, options ...lksdk.ConnectOption) error {
				if joinCtx == nil {
					t.Fatal("join context = nil")
				}
				if room != prepared {
					t.Fatal("joined room did not match prepared room")
				}
				if url != "wss://livekit.example" {
					t.Fatalf("join url = %q, want wss://livekit.example", url)
				}
				if info.RoomName != "room-a" {
					t.Fatalf("ConnectInfo.RoomName = %q, want room-a", info.RoomName)
				}
				if info.ParticipantName != "Agent Name" {
					t.Fatalf("ConnectInfo.ParticipantName = %q, want Agent Name", info.ParticipantName)
				}
				if info.ParticipantIdentity != "agent-a" {
					t.Fatalf("ConnectInfo.ParticipantIdentity = %q, want agent-a", info.ParticipantIdentity)
				}
				if len(options) != 1 {
					t.Fatalf("join options = %d, want 1", len(options))
				}
				joined = true
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("JoinPreparedRoom() error = %v", err)
	}
	if !joined {
		t.Fatal("prepared room was not joined")
	}
}

func TestPreparedRoomConnectOptionsFromAcceptedJobCopiesAcceptArgs(t *testing.T) {
	room := lksdk.NewRoom(nil)
	job := &lkprotocol.Job{Room: &lkprotocol.Room{Name: "room-a"}}
	connector := workerlivekit.RoomConnector{}

	opts := workerlivekit.PreparedRoomConnectOptionsFromAcceptedJob(workerlivekit.AcceptedJobRoomConnectOptions{
		Room:          room,
		URL:           "wss://livekit.example",
		Token:         "room-token",
		Job:           job,
		APIKey:        "key",
		APISecret:     "secret",
		AutoSubscribe: "audio_only",
		Connector:     connector,
		Identity:      "resolved-agent",
		Accept: workerlivekit.JobAcceptArguments{
			Name:     "Agent Name",
			Identity: "raw-agent",
			Metadata: "meta",
			Attributes: map[string]string{
				"tier": "gold",
			},
		},
	})

	if opts.Room != room {
		t.Fatal("Room did not preserve prepared room")
	}
	if opts.Job != job {
		t.Fatal("Job did not preserve LiveKit job")
	}
	if opts.Accept.ParticipantName != "Agent Name" {
		t.Fatalf("ParticipantName = %q, want Agent Name", opts.Accept.ParticipantName)
	}
	if opts.Accept.ParticipantIdentity != "resolved-agent" {
		t.Fatalf("ParticipantIdentity = %q, want resolved-agent", opts.Accept.ParticipantIdentity)
	}
	if opts.Accept.ParticipantMetadata != "meta" {
		t.Fatalf("ParticipantMetadata = %q, want meta", opts.Accept.ParticipantMetadata)
	}
	if opts.Accept.ParticipantAttributes["tier"] != "gold" {
		t.Fatalf("ParticipantAttributes[tier] = %q, want gold", opts.Accept.ParticipantAttributes["tier"])
	}
}
