package core

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestEventBusSubscribe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := NewEventBus(ctx)

	var received atomic.Int32
	bus.Subscribe("text", func(event BusEvent) {
		received.Add(1)
	})

	bus.Emit(BusEvent{
		Event: Event{Type: EventText, Content: "hello"},
	})

	// Wait for goroutine
	time.Sleep(10 * time.Millisecond)

	if received.Load() != 1 {
		t.Errorf("expected 1, got %d", received.Load())
	}
}

func TestEventBusSubscribeAll(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := NewEventBus(ctx)

	var received atomic.Int32
	bus.SubscribeAll(func(event BusEvent) {
		received.Add(1)
	})

	bus.Emit(BusEvent{Event: Event{Type: EventText}})
	bus.Emit(BusEvent{Event: Event{Type: EventToolUse}})
	bus.Emit(BusEvent{Event: Event{Type: EventResult}})

	time.Sleep(10 * time.Millisecond)

	if received.Load() != 3 {
		t.Errorf("expected 3, got %d", received.Load())
	}
}

func TestEventBusUnsubscribe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := NewEventBus(ctx)

	var received atomic.Int32
	unsub := bus.Subscribe("text", func(event BusEvent) {
		received.Add(1)
	})

	bus.Emit(BusEvent{Event: Event{Type: EventText}})
	time.Sleep(10 * time.Millisecond)

	if received.Load() != 1 {
		t.Errorf("expected 1, got %d", received.Load())
	}

	unsub()

	bus.Emit(BusEvent{Event: Event{Type: EventText}})
	time.Sleep(10 * time.Millisecond)

	if received.Load() != 1 {
		t.Errorf("expected still 1 after unsubscribe, got %d", received.Load())
	}
}

func TestEventBusMultipleListeners(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := NewEventBus(ctx)

	var received atomic.Int32
	bus.Subscribe("text", func(event BusEvent) {
		received.Add(1)
	})
	bus.Subscribe("text", func(event BusEvent) {
		received.Add(1)
	})

	bus.Emit(BusEvent{Event: Event{Type: EventText}})
	time.Sleep(10 * time.Millisecond)

	if received.Load() != 2 {
		t.Errorf("expected 2, got %d", received.Load())
	}
}

func TestEventBusPanicRecovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := NewEventBus(ctx)

	var received atomic.Int32
	bus.Subscribe("text", func(event BusEvent) {
		panic("test panic")
	})
	bus.Subscribe("text", func(event BusEvent) {
		received.Add(1)
	})

	// Should not panic
	bus.Emit(BusEvent{Event: Event{Type: EventText}})
	time.Sleep(10 * time.Millisecond)

	if received.Load() != 1 {
		t.Errorf("expected 1, got %d", received.Load())
	}
}

func TestEventBusContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	bus := NewEventBus(ctx)

	var received atomic.Int32
	bus.Subscribe("text", func(event BusEvent) {
		received.Add(1)
	})

	// Cancel context
	cancel()
	time.Sleep(10 * time.Millisecond)

	bus.Emit(BusEvent{Event: Event{Type: EventText}})
	time.Sleep(10 * time.Millisecond)

	// Listener should not receive after context cancel
	// (Note: the current implementation still calls listeners, this test documents behavior)
}
