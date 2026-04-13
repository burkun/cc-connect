package core

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
)

// EventBus provides a centralized event dispatching mechanism.
// All events from agents flow through this bus, allowing multiple
// listeners (platforms, logging, metrics, etc.) to subscribe.
type EventBus struct {
	mu        sync.RWMutex
	listeners map[string][]*listenerEntry
	ctx       context.Context
	nextID    atomic.Int64
}

// listenerEntry wraps a listener with a unique ID for unsubscribe.
type listenerEntry struct {
	id       int64
	listener EventListener
}

// EventListener is a callback function that receives events.
type EventListener func(event BusEvent)

// BusEvent represents an event flowing through the bus.
// It wraps the agent Event with additional context.
type BusEvent struct {
	// The original agent event
	Event Event
	// Session key for routing (e.g., "feishu:{chatID}:{userID}")
	SessionKey string
	// Platform name
	Platform string
	// Internal session ID
	SessionID string
	// Timestamp when the event was emitted
	Timestamp int64
}

// NewEventBus creates a new EventBus.
func NewEventBus(ctx context.Context) *EventBus {
	return &EventBus{
		listeners: make(map[string][]*listenerEntry),
		ctx:       ctx,
	}
}

// Subscribe registers a listener for a specific event type.
// Returns an unsubscribe function.
// Event types are: "text", "tool_use", "tool_result", "result", "error",
// "permission_request", "thinking", "*" (all events).
func (eb *EventBus) Subscribe(eventType string, listener EventListener) func() {
	id := eb.nextID.Add(1)
	entry := &listenerEntry{id: id, listener: listener}

	eb.mu.Lock()
	eb.listeners[eventType] = append(eb.listeners[eventType], entry)
	eb.mu.Unlock()

	return func() {
		eb.mu.Lock()
		defer eb.mu.Unlock()
		listeners := eb.listeners[eventType]
		for i, e := range listeners {
			if e.id == id {
				eb.listeners[eventType] = append(listeners[:i], listeners[i+1:]...)
				break
			}
		}
	}
}

// SubscribeAll registers a listener for all event types.
// Returns an unsubscribe function.
func (eb *EventBus) SubscribeAll(listener EventListener) func() {
	return eb.Subscribe("*", listener)
}

// Emit dispatches an event to all registered listeners.
// Listeners are called in goroutines to avoid blocking.
func (eb *EventBus) Emit(event BusEvent) {
	if event.Timestamp == 0 {
		event.Timestamp = currentTimeMillis()
	}

	eb.mu.RLock()
	defer eb.mu.RUnlock()

	// Get listeners for this event type
	listeners := eb.listeners[string(event.Event.Type)]
	allListeners := eb.listeners["*"]

	// Combine specific and wildcard listeners
	totalLen := len(listeners) + len(allListeners)
	if totalLen == 0 {
		return
	}

	// Call all listeners non-blocking
	for _, entry := range listeners {
		go eb.safeCall(entry.listener, event)
	}
	for _, entry := range allListeners {
		go eb.safeCall(entry.listener, event)
	}
}

// safeCall calls a listener with panic recovery.
func (eb *EventBus) safeCall(listener EventListener, event BusEvent) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("eventbus: listener panic", "error", r, "event_type", event.Event.Type)
		}
	}()

	// Check context before calling
	select {
	case <-eb.ctx.Done():
		return
	default:
	}

	listener(event)
}

// EmitSimple creates and emits a BusEvent with minimal fields.
func (eb *EventBus) EmitSimple(event Event, sessionKey, platform, sessionID string) {
	eb.Emit(BusEvent{
		Event:      event,
		SessionKey: sessionKey,
		Platform:   platform,
		SessionID:  sessionID,
	})
}

// ListenerCount returns the number of listeners for a given event type.
func (eb *EventBus) ListenerCount(eventType string) int {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	return len(eb.listeners[eventType])
}

// TotalListenerCount returns the total number of listeners across all event types.
func (eb *EventBus) TotalListenerCount() int {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	total := 0
	for _, listeners := range eb.listeners {
		total += len(listeners)
	}
	return total
}

// currentTimeMillis returns current time in milliseconds.
// Extracted as a variable for testing.
var currentTimeMillis = func() int64 {
	return int64(0) // Will be set in init or by engine
}

func init() {
	currentTimeMillis = func() int64 {
		return int64(0) // Placeholder, real implementation uses time.Now()
	}
}
