package worker

import (
	"cmp"
	"maps"
	"slices"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/webrtc/v4"
)

type Room struct {
	*lksdk.Room

	callbacks map[int8][]*lksdk.RoomCallback
}

func NewRoom() *Room {
	room := new(Room)
	room.callbacks = make(map[int8][]*lksdk.RoomCallback)

	emit := func(f func(*lksdk.RoomCallback)) {
		sortedPriorities := slices.SortedFunc(maps.Keys(room.callbacks), func(a, b int8) int {
			return cmp.Compare(b, a)
		})
		for _, priority := range sortedPriorities {
			for _, cb := range room.callbacks[priority] {
				f(cb)
			}
		}
	}

	room.Room = lksdk.NewRoom(&lksdk.RoomCallback{
		OnDisconnected: func() {
			emit(func(cb *lksdk.RoomCallback) {
				if cb.OnDisconnected != nil {
					cb.OnDisconnected()
				}
			})
		},
		OnDisconnectedWithReason: func(reason lksdk.DisconnectionReason) {
			emit(func(cb *lksdk.RoomCallback) {
				if cb.OnDisconnectedWithReason != nil {
					cb.OnDisconnectedWithReason(reason)
				}
			})
		},
		OnParticipantConnected: func(p *lksdk.RemoteParticipant) {
			emit(func(cb *lksdk.RoomCallback) {
				if cb.OnParticipantConnected != nil {
					cb.OnParticipantConnected(p)
				}
			})
		},
		OnParticipantDisconnected: func(p *lksdk.RemoteParticipant) {
			emit(func(cb *lksdk.RoomCallback) {
				if cb.OnParticipantDisconnected != nil {
					cb.OnParticipantDisconnected(p)
				}
			})
		},
		OnActiveSpeakersChanged: func(p []lksdk.Participant) {
			emit(func(cb *lksdk.RoomCallback) {
				if cb.OnActiveSpeakersChanged != nil {
					cb.OnActiveSpeakersChanged(p)
				}
			})
		},
		OnRoomMetadataChanged: func(metadata string) {
			emit(func(cb *lksdk.RoomCallback) {
				if cb.OnRoomMetadataChanged != nil {
					cb.OnRoomMetadataChanged(metadata)
				}
			})
		},
		OnRecordingStatusChanged: func(recording bool) {
			emit(func(cb *lksdk.RoomCallback) {
				if cb.OnRecordingStatusChanged != nil {
					cb.OnRecordingStatusChanged(recording)
				}
			})
		},
		OnRoomMoved: func(roomName string, token string) {
			emit(func(cb *lksdk.RoomCallback) {
				if cb.OnRoomMoved != nil {
					cb.OnRoomMoved(roomName, token)
				}
			})
		},
		OnReconnecting: func() {
			emit(func(cb *lksdk.RoomCallback) {
				if cb.OnReconnecting != nil {
					cb.OnReconnecting()
				}
			})
		},
		OnReconnected: func() {
			emit(func(cb *lksdk.RoomCallback) {
				if cb.OnReconnected != nil {
					cb.OnReconnected()
				}
			})
		},
		OnLocalTrackSubscribed: func(pub *lksdk.LocalTrackPublication, p *lksdk.LocalParticipant) {
			emit(func(cb *lksdk.RoomCallback) {
				if cb.OnLocalTrackSubscribed != nil {
					cb.OnLocalTrackSubscribed(pub, p)
				}
			})
		},
		ParticipantCallback: lksdk.ParticipantCallback{
			OnLocalTrackPublished: func(pub *lksdk.LocalTrackPublication, p *lksdk.LocalParticipant) {
				emit(func(cb *lksdk.RoomCallback) {
					if cb.OnLocalTrackPublished != nil {
						cb.OnLocalTrackPublished(pub, p)
					}
				})
			},
			OnLocalTrackUnpublished: func(pub *lksdk.LocalTrackPublication, p *lksdk.LocalParticipant) {
				emit(func(cb *lksdk.RoomCallback) {
					if cb.OnLocalTrackUnpublished != nil {
						cb.OnLocalTrackUnpublished(pub, p)
					}
				})
			},
			OnTrackMuted: func(pub lksdk.TrackPublication, p lksdk.Participant) {
				emit(func(cb *lksdk.RoomCallback) {
					if cb.OnTrackMuted != nil {
						cb.OnTrackMuted(pub, p)
					}
				})
			},
			OnTrackUnmuted: func(pub lksdk.TrackPublication, p lksdk.Participant) {
				emit(func(cb *lksdk.RoomCallback) {
					if cb.OnTrackUnmuted != nil {
						cb.OnTrackUnmuted(pub, p)
					}
				})
			},
			OnMetadataChanged: func(oldMetadata string, p lksdk.Participant) {
				emit(func(cb *lksdk.RoomCallback) {
					if cb.OnMetadataChanged != nil {
						cb.OnMetadataChanged(oldMetadata, p)
					}
				})
			},
			OnAttributesChanged: func(changed map[string]string, p lksdk.Participant) {
				emit(func(cb *lksdk.RoomCallback) {
					if cb.OnAttributesChanged != nil {
						cb.OnAttributesChanged(changed, p)
					}
				})
			},
			OnIsSpeakingChanged: func(p lksdk.Participant) {
				emit(func(cb *lksdk.RoomCallback) {
					if cb.OnIsSpeakingChanged != nil {
						cb.OnIsSpeakingChanged(p)
					}
				})
			},
			OnConnectionQualityChanged: func(update *livekit.ConnectionQualityInfo, p lksdk.Participant) {
				emit(func(cb *lksdk.RoomCallback) {
					if cb.OnConnectionQualityChanged != nil {
						cb.OnConnectionQualityChanged(update, p)
					}
				})
			},
			OnTrackSubscribed: func(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, p *lksdk.RemoteParticipant) {
				emit(func(cb *lksdk.RoomCallback) {
					if cb.OnTrackSubscribed != nil {
						cb.OnTrackSubscribed(track, pub, p)
					}
				})
			},
			OnTrackUnsubscribed: func(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, p *lksdk.RemoteParticipant) {
				emit(func(cb *lksdk.RoomCallback) {
					if cb.OnTrackUnsubscribed != nil {
						cb.OnTrackUnsubscribed(track, pub, p)
					}
				})
			},
			OnTrackSubscriptionFailed: func(sid string, p *lksdk.RemoteParticipant) {
				emit(func(cb *lksdk.RoomCallback) {
					if cb.OnTrackSubscriptionFailed != nil {
						cb.OnTrackSubscriptionFailed(sid, p)
					}
				})
			},
			OnTrackPublished: func(pub *lksdk.RemoteTrackPublication, p *lksdk.RemoteParticipant) {
				emit(func(cb *lksdk.RoomCallback) {
					if cb.OnTrackPublished != nil {
						cb.OnTrackPublished(pub, p)
					}
				})
			},
			OnTrackUnpublished: func(pub *lksdk.RemoteTrackPublication, p *lksdk.RemoteParticipant) {
				emit(func(cb *lksdk.RoomCallback) {
					if cb.OnTrackUnpublished != nil {
						cb.OnTrackUnpublished(pub, p)
					}
				})
			},
			OnDataReceived: func(data []byte, params lksdk.DataReceiveParams) {
				emit(func(cb *lksdk.RoomCallback) {
					if cb.OnDataReceived != nil {
						cb.OnDataReceived(data, params)
					}
				})
			},
			OnDataPacket: func(packet lksdk.DataPacket, params lksdk.DataReceiveParams) {
				emit(func(cb *lksdk.RoomCallback) {
					if cb.OnDataPacket != nil {
						cb.OnDataPacket(packet, params)
					}
				})
			},
			OnTranscriptionReceived: func(segments []*lksdk.TranscriptionSegment, p lksdk.Participant, pub lksdk.TrackPublication) {
				emit(func(cb *lksdk.RoomCallback) {
					if cb.OnTranscriptionReceived != nil {
						cb.OnTranscriptionReceived(segments, p, pub)
					}
				})
			},
		},
	})

	return room
}

func (r *Room) AddCallback(callback *lksdk.RoomCallback) {
	r.AddCallbackWithPriority(callback, 0)
}

func (r *Room) AddCallbackWithPriority(callback *lksdk.RoomCallback, priority int8) {
	r.callbacks[priority] = append(r.callbacks[priority], callback)
}
