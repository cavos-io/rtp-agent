package livekit_test

import (
	"sync"
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/webrtc/v4"
)

func TestRoomCallbackRegistryDeliversToCallbackRegisteredAfterCallbackWasTaken(t *testing.T) {
	registry := workerlivekit.NewRoomCallbackRegistry()

	// Mirrors room creation: the fan-out callback is taken once, up front.
	fanOut := registry.Callback()

	late := false
	registry.Add(&lksdk.RoomCallback{
		OnDisconnected: func() {
			late = true
		},
	})

	fanOut.OnDisconnected()

	if !late {
		t.Fatal("callback registered after the fan-out callback was taken received no event")
	}
}

func TestRoomCallbackRegistryCallbackPointerIsStable(t *testing.T) {
	registry := workerlivekit.NewRoomCallbackRegistry()

	first := registry.Callback()
	registry.Add(&lksdk.RoomCallback{OnDisconnected: func() {}})
	second := registry.Callback()

	if first != second {
		t.Fatal("Callback() returned a different pointer after registration")
	}
}

func TestRoomCallbackRegistryInvokesEveryCallbackInRegistrationOrder(t *testing.T) {
	registry := workerlivekit.NewRoomCallbackRegistry()

	var order []string
	registry.Add(&lksdk.RoomCallback{
		OnDisconnected: func() {
			order = append(order, "first")
		},
	})
	registry.Add(&lksdk.RoomCallback{
		OnDisconnected: func() {
			order = append(order, "second")
		},
	})

	registry.Callback().OnDisconnected()

	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Fatalf("dispatch order = %v, want [first second]", order)
	}
}

func TestRoomCallbackRegistrySkipsNilHandlers(t *testing.T) {
	registry := workerlivekit.NewRoomCallbackRegistry()

	reached := false
	registry.Add(&lksdk.RoomCallback{}) // every handler nil
	registry.Add(&lksdk.RoomCallback{
		OnDisconnected: func() {
			reached = true
		},
	})

	registry.Callback().OnDisconnected()

	if !reached {
		t.Fatal("a callback with nil handlers stopped dispatch reaching later callbacks")
	}
}

func TestRoomCallbackRegistryAddIgnoresNilCallback(t *testing.T) {
	registry := workerlivekit.NewRoomCallbackRegistry()

	remove := registry.Add(nil)
	if remove == nil {
		t.Fatal("Add(nil) returned a nil remove func")
	}
	remove()

	if registry.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", registry.Len())
	}
	registry.Callback().OnDisconnected()
}

func TestRoomCallbackRegistryRemoveStopsDelivery(t *testing.T) {
	registry := workerlivekit.NewRoomCallbackRegistry()

	calls := 0
	remove := registry.Add(&lksdk.RoomCallback{
		OnDisconnected: func() {
			calls++
		},
	})

	registry.Callback().OnDisconnected()
	remove()
	registry.Callback().OnDisconnected()

	if calls != 1 {
		t.Fatalf("callback invoked %d times, want 1", calls)
	}
	if registry.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", registry.Len())
	}
}

func TestRoomCallbackRegistryRemoveIsIdempotentAndLeavesOthersRegistered(t *testing.T) {
	registry := workerlivekit.NewRoomCallbackRegistry()

	removedCalls := 0
	keptCalls := 0
	remove := registry.Add(&lksdk.RoomCallback{
		OnDisconnected: func() {
			removedCalls++
		},
	})
	registry.Add(&lksdk.RoomCallback{
		OnDisconnected: func() {
			keptCalls++
		},
	})

	remove()
	remove()
	registry.Callback().OnDisconnected()

	if removedCalls != 0 {
		t.Fatalf("removed callback invoked %d times, want 0", removedCalls)
	}
	if keptCalls != 1 {
		t.Fatalf("kept callback invoked %d times, want 1", keptCalls)
	}
}

func TestRoomCallbackRegistryFansOutParticipantEvents(t *testing.T) {
	registry := workerlivekit.NewRoomCallbackRegistry()

	subscribed := 0
	dataPackets := 0
	registry.Add(&lksdk.RoomCallback{
		ParticipantCallback: lksdk.ParticipantCallback{
			OnTrackSubscribed: func(_ *webrtc.TrackRemote, _ *lksdk.RemoteTrackPublication, _ *lksdk.RemoteParticipant) {
				subscribed++
			},
			OnDataPacket: func(_ lksdk.DataPacket, _ lksdk.DataReceiveParams) {
				dataPackets++
			},
		},
	})

	fanOut := registry.Callback()
	fanOut.OnTrackSubscribed(nil, nil, nil)
	fanOut.OnDataPacket(nil, lksdk.DataReceiveParams{})

	if subscribed != 1 {
		t.Fatalf("OnTrackSubscribed invoked %d times, want 1", subscribed)
	}
	if dataPackets != 1 {
		t.Fatalf("OnDataPacket invoked %d times, want 1", dataPackets)
	}
}

func TestRoomCallbackRegistryIsolatesPanickingCallback(t *testing.T) {
	registry := workerlivekit.NewRoomCallbackRegistry()

	// A panicking downstream callback registered first must not stop RoomIO's own
	// handler registered after it, and must not propagate to the caller.
	registry.Add(&lksdk.RoomCallback{
		OnDisconnected: func() {
			panic("downstream handler blew up")
		},
	})
	survived := false
	registry.Add(&lksdk.RoomCallback{
		OnDisconnected: func() {
			survived = true
		},
	})

	// Must not panic out of the fan-out callback.
	registry.Callback().OnDisconnected()

	if !survived {
		t.Fatal("a panicking callback stopped a later callback from running")
	}
}

func TestRoomCallbackRegistryAllowsConcurrentRegistrationDuringDispatch(t *testing.T) {
	registry := workerlivekit.NewRoomCallbackRegistry()
	fanOut := registry.Callback()

	var mu sync.Mutex
	calls := 0
	registry.Add(&lksdk.RoomCallback{
		OnDisconnected: func() {
			mu.Lock()
			calls++
			mu.Unlock()
		},
	})

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			remove := registry.Add(&lksdk.RoomCallback{OnDisconnected: func() {}})
			remove()
		}()
		go func() {
			defer wg.Done()
			fanOut.OnDisconnected()
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if calls != 16 {
		t.Fatalf("callback invoked %d times, want 16", calls)
	}
}
