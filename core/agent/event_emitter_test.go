package agent

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEventEmitterOn(t *testing.T) {
	emitter := NewEventEmitter()

	callCount := 0
	callback := func(ev *AgentEvent) {
		callCount++
	}

	// On should return the callback
	returned := emitter.On("test_event", callback)
	if returned == nil {
		t.Error("On() should return the callback")
	}

	// ListenerCount should be 1
	if emitter.ListenerCount("test_event") != 1 {
		t.Errorf("Expected 1 listener, got %d", emitter.ListenerCount("test_event"))
	}
}

func TestEventEmitterMultipleListeners(t *testing.T) {
	emitter := NewEventEmitter()

	var callCounts [3]int32

	// Register 3 callbacks for same event
	for i := 0; i < 3; i++ {
		idx := i
		emitter.On("user_state_changed", func(ev *AgentEvent) {
			atomic.AddInt32(&callCounts[idx], 1)
		})
	}

	if emitter.ListenerCount("user_state_changed") != 3 {
		t.Errorf("Expected 3 listeners, got %d", emitter.ListenerCount("user_state_changed"))
	}

	// Emit should call all 3
	event := &AgentEvent{
		Type: "user_state_changed",
		UserStateChanged: &UserStateChangedEvent{
			OldState:  UserStateListening,
			NewState:  UserStateSpeaking,
			CreatedAt: time.Now(),
		},
	}
	emitter.Emit(event)

	// All should be called
	for i := 0; i < 3; i++ {
		if atomic.LoadInt32(&callCounts[i]) != 1 {
			t.Errorf("Callback %d not called", i)
		}
	}
}

func TestEventEmitterTypeSafeFiltering(t *testing.T) {
	emitter := NewEventEmitter()

	var userStateCount int32
	var agentStateCount int32

	// Register for user_state_changed
	emitter.On("user_state_changed", func(ev *AgentEvent) {
		atomic.AddInt32(&userStateCount, 1)
	})

	// Register for agent_state_changed
	emitter.On("agent_state_changed", func(ev *AgentEvent) {
		atomic.AddInt32(&agentStateCount, 1)
	})

	// Emit user_state_changed
	emitter.Emit(&AgentEvent{
		Type: "user_state_changed",
		UserStateChanged: &UserStateChangedEvent{
			OldState:  UserStateListening,
			NewState:  UserStateSpeaking,
			CreatedAt: time.Now(),
		},
	})

	// Only user_state callback should be called
	if atomic.LoadInt32(&userStateCount) != 1 {
		t.Error("user_state_changed callback not called")
	}
	if atomic.LoadInt32(&agentStateCount) != 0 {
		t.Error("agent_state_changed callback should not be called")
	}

	// Emit agent_state_changed
	emitter.Emit(&AgentEvent{
		Type: "agent_state_changed",
		AgentStateChanged: &AgentStateChangedEvent{
			OldState:  AgentStateIdle,
			NewState:  AgentStateListening,
			CreatedAt: time.Now(),
		},
	})

	// Now both should have been called
	if atomic.LoadInt32(&userStateCount) != 1 {
		t.Error("user_state_changed count should still be 1")
	}
	if atomic.LoadInt32(&agentStateCount) != 1 {
		t.Error("agent_state_changed callback not called")
	}
}

func TestEventEmitterErrorIsolation(t *testing.T) {
	emitter := NewEventEmitter()

	var callback1Called, callback2Called, callback3Called int32

	// Callback 1: panics
	emitter.On("test_event", func(ev *AgentEvent) {
		atomic.AddInt32(&callback1Called, 1)
		panic("intentional panic in callback 1")
	})

	// Callback 2: normal
	emitter.On("test_event", func(ev *AgentEvent) {
		atomic.AddInt32(&callback2Called, 1)
	})

	// Callback 3: normal
	emitter.On("test_event", func(ev *AgentEvent) {
		atomic.AddInt32(&callback3Called, 1)
	})

	// Emit - should not crash despite panic in callback 1
	emitter.Emit(&AgentEvent{Type: "test_event"})

	// All should have been called (panic is recovered)
	if atomic.LoadInt32(&callback1Called) != 1 {
		t.Error("Callback 1 (panic) not called")
	}
	if atomic.LoadInt32(&callback2Called) != 1 {
		t.Error("Callback 2 should still be called despite panic in callback 1")
	}
	if atomic.LoadInt32(&callback3Called) != 1 {
		t.Error("Callback 3 should still be called despite panic in callback 1")
	}
}

func TestEventEmitterEventDataAccess(t *testing.T) {
	emitter := NewEventEmitter()

	var receivedEvent *AgentEvent

	emitter.On("user_state_changed", func(ev *AgentEvent) {
		receivedEvent = ev
	})

	expectedOldState := UserStateListening
	expectedNewState := UserStateSpeaking
	expectedTime := time.Now()

	event := &AgentEvent{
		Type: "user_state_changed",
		UserStateChanged: &UserStateChangedEvent{
			OldState:  expectedOldState,
			NewState:  expectedNewState,
			CreatedAt: expectedTime,
		},
	}

	emitter.Emit(event)

	if receivedEvent == nil {
		t.Fatal("Event not received")
	}

	if receivedEvent.UserStateChanged == nil {
		t.Fatal("UserStateChanged is nil")
	}

	if receivedEvent.UserStateChanged.OldState != expectedOldState {
		t.Errorf("Expected OldState %v, got %v", expectedOldState, receivedEvent.UserStateChanged.OldState)
	}

	if receivedEvent.UserStateChanged.NewState != expectedNewState {
		t.Errorf("Expected NewState %v, got %v", expectedNewState, receivedEvent.UserStateChanged.NewState)
	}
}

func TestEventEmitterConcurrentEmit(t *testing.T) {
	emitter := NewEventEmitter()

	var callCount int32

	// Register callback
	emitter.On("concurrent_event", func(ev *AgentEvent) {
		atomic.AddInt32(&callCount, 1)
	})

	// Emit from multiple goroutines
	var wg sync.WaitGroup
	numGoroutines := 10
	emitsPerGoroutine := 100

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < emitsPerGoroutine; i++ {
				emitter.Emit(&AgentEvent{Type: "concurrent_event"})
			}
		}()
	}

	wg.Wait()

	expectedCount := int32(numGoroutines * emitsPerGoroutine)
	if atomic.LoadInt32(&callCount) != expectedCount {
		t.Errorf("Expected %d calls, got %d", expectedCount, atomic.LoadInt32(&callCount))
	}
}

func TestEventEmitterConcurrentRegisterAndEmit(t *testing.T) {
	emitter := NewEventEmitter()

	var callCount int32
	var wg sync.WaitGroup

	// Register listeners concurrently
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			emitter.On("concurrent_event", func(ev *AgentEvent) {
				atomic.AddInt32(&callCount, 1)
			})
		}()
	}

	// Emit concurrently
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			emitter.Emit(&AgentEvent{Type: "concurrent_event"})
		}()
	}

	wg.Wait()

	// Should have some calls (exact count depends on race, but should be > 0)
	count := atomic.LoadInt32(&callCount)
	if count == 0 {
		t.Error("No callbacks were called")
	}
}

func TestEventEmitterNilHandling(t *testing.T) {
	// Nil emitter should not panic
	var emitter *EventEmitter
	emitter.On("test", func(ev *AgentEvent) {})
	emitter.Emit(&AgentEvent{Type: "test"})
	_ = emitter.ListenerCount("test")
	emitter.Clear()

	// Nil callback should not panic
	emitter = NewEventEmitter()
	emitter.On("test", nil)
	if emitter.ListenerCount("test") != 0 {
		t.Error("Nil callback should not be registered")
	}

	// Nil event should not panic
	emitter.Emit(nil)
}

func TestEventEmitterClear(t *testing.T) {
	emitter := NewEventEmitter()

	// Register multiple listeners
	emitter.On("event1", func(ev *AgentEvent) {})
	emitter.On("event1", func(ev *AgentEvent) {})
	emitter.On("event2", func(ev *AgentEvent) {})

	if emitter.ListenerCount("event1") != 2 {
		t.Error("Expected 2 listeners for event1")
	}
	if emitter.ListenerCount("event2") != 1 {
		t.Error("Expected 1 listener for event2")
	}

	// Clear all
	emitter.Clear()

	if emitter.ListenerCount("event1") != 0 {
		t.Error("Expected 0 listeners after clear")
	}
	if emitter.ListenerCount("event2") != 0 {
		t.Error("Expected 0 listeners after clear")
	}
}

func TestEventEmitterListenerCountNonExistent(t *testing.T) {
	emitter := NewEventEmitter()

	// ListenerCount for non-existent event should return 0
	if emitter.ListenerCount("non_existent") != 0 {
		t.Error("Expected 0 listeners for non-existent event")
	}
}

// Benchmark tests

func BenchmarkEmitterOn(b *testing.B) {
	emitter := NewEventEmitter()
	callback := func(ev *AgentEvent) {}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		emitter.On("benchmark_event", callback)
	}
}

func BenchmarkEmitterEmitSingleListener(b *testing.B) {
	emitter := NewEventEmitter()
	emitter.On("benchmark_event", func(ev *AgentEvent) {})

	event := &AgentEvent{Type: "benchmark_event"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		emitter.Emit(event)
	}
}

func BenchmarkEmitterEmitMultipleListeners(b *testing.B) {
	emitter := NewEventEmitter()
	for i := 0; i < 10; i++ {
		emitter.On("benchmark_event", func(ev *AgentEvent) {})
	}

	event := &AgentEvent{Type: "benchmark_event"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		emitter.Emit(event)
	}
}
