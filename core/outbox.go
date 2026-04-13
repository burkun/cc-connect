package core

import (
	"log/slog"
	"sync"
	"time"
)

// OutboxConfig configures the outbox behavior.
type OutboxConfig struct {
	// MaxItems is the maximum number of items to buffer.
	MaxItems int
	// MaxBytes is the maximum total bytes to buffer.
	MaxBytes int64
	// MaxItemBytes is the maximum size of a single item.
	MaxItemBytes int64
	// MaxAge is the maximum age of an item before it's dropped.
	MaxAge time.Duration
}

// DefaultOutboxConfig returns the default configuration.
func DefaultOutboxConfig() OutboxConfig {
	return OutboxConfig{
		MaxItems:     500,
		MaxBytes:     16 * 1024 * 1024, // 16 MB
		MaxItemBytes: 1 * 1024 * 1024,  // 1 MB
		MaxAge:       15 * time.Minute,
	}
}

// OutboxItem represents a queued message.
type OutboxItem struct {
	Content    string
	Platform   string
	SessionKey string
	ReplyCtx   any
	EnqueuedAt time.Time
	SizeBytes  int64
}

// Outbox buffers messages when the platform is unavailable.
// It provides memory-bounded buffering with age-based expiration.
type Outbox struct {
	mu     sync.Mutex
	items  []*OutboxItem
	config OutboxConfig

	// Statistics
	totalEnqueued int64
	totalDropped  int64
	totalFlushed  int64
	queuedBytes   int64
}

// NewOutbox creates a new Outbox with the given configuration.
func NewOutbox(config OutboxConfig) *Outbox {
	if config.MaxItems <= 0 {
		config.MaxItems = 500
	}
	if config.MaxBytes <= 0 {
		config.MaxBytes = 16 * 1024 * 1024
	}
	if config.MaxItemBytes <= 0 {
		config.MaxItemBytes = 1 * 1024 * 1024
	}
	if config.MaxAge <= 0 {
		config.MaxAge = 15 * time.Minute
	}

	return &Outbox{
		items:  make([]*OutboxItem, 0),
		config: config,
	}
}

// Enqueue adds a message to the outbox.
// Returns true if enqueued, false if dropped.
func (o *Outbox) Enqueue(content, platform, sessionKey string, replyCtx any) bool {
	o.mu.Lock()
	defer o.mu.Unlock()

	// Drop expired items first
	o.dropExpiredLocked()

	sizeBytes := int64(len(content))

	// Check item size
	if sizeBytes > o.config.MaxItemBytes {
		slog.Warn("outbox: item too large, dropping",
			"size_bytes", sizeBytes,
			"max_item_bytes", o.config.MaxItemBytes,
			"platform", platform,
			"session_key", sessionKey)
		o.totalDropped++
		return false
	}

	// Check and enforce limits
	for (len(o.items) >= o.config.MaxItems || o.queuedBytes+sizeBytes > o.config.MaxBytes) && len(o.items) > 0 {
		// Drop oldest item
		dropped := o.items[0]
		o.items = o.items[1:]
		o.queuedBytes -= dropped.SizeBytes
		o.totalDropped++
		slog.Warn("outbox: buffer full, dropping oldest",
			"dropped_size", dropped.SizeBytes,
			"platform", dropped.Platform,
			"age", time.Since(dropped.EnqueuedAt))
	}

	item := &OutboxItem{
		Content:    content,
		Platform:   platform,
		SessionKey: sessionKey,
		ReplyCtx:   replyCtx,
		EnqueuedAt: time.Now(),
		SizeBytes:  sizeBytes,
	}

	o.items = append(o.items, item)
	o.queuedBytes += sizeBytes
	o.totalEnqueued++

	slog.Debug("outbox: enqueued",
		"platform", platform,
		"session_key", sessionKey,
		"size_bytes", sizeBytes,
		"queue_len", len(o.items))

	return true
}

// Flush sends all buffered messages via the provided send function.
// The send function should return an error if the send fails.
// Flush continues sending even if some sends fail.
func (o *Outbox) Flush(sendFn func(item *OutboxItem) error) int {
	o.mu.Lock()
	items := o.items
	o.items = nil
	o.queuedBytes = 0
	o.mu.Unlock()

	if len(items) == 0 {
		return 0
	}

	sent := 0
	for _, item := range items {
		// Check age before sending
		if time.Since(item.EnqueuedAt) > o.config.MaxAge {
			o.totalDropped++
			slog.Debug("outbox: item expired during flush",
				"platform", item.Platform,
				"session_key", item.SessionKey,
				"age", time.Since(item.EnqueuedAt))
			continue
		}

		if err := sendFn(item); err != nil {
			slog.Error("outbox: flush send failed",
				"error", err,
				"platform", item.Platform,
				"session_key", item.SessionKey)
			// Re-queue on failure? For now, we drop on flush failure
			o.totalDropped++
			continue
		}

		sent++
		o.totalFlushed++
	}

	if sent > 0 {
		slog.Info("outbox: flushed", "count", sent, "dropped", len(items)-sent)
	}

	return sent
}

// FlushForPlatform sends all buffered messages for a specific platform.
func (o *Outbox) FlushForPlatform(platform string, sendFn func(item *OutboxItem) error) int {
	o.mu.Lock()
	var items []*OutboxItem
	var remaining []*OutboxItem
	for _, item := range o.items {
		if item.Platform == platform {
			items = append(items, item)
		} else {
			remaining = append(remaining, item)
		}
	}
	o.items = remaining
	o.mu.Unlock()

	if len(items) == 0 {
		return 0
	}

	sent := 0
	for _, item := range items {
		if time.Since(item.EnqueuedAt) > o.config.MaxAge {
			o.totalDropped++
			continue
		}

		if err := sendFn(item); err != nil {
			o.totalDropped++
			continue
		}

		sent++
		o.totalFlushed++
	}

	// Update queued bytes
	o.mu.Lock()
	o.queuedBytes = 0
	for _, item := range o.items {
		o.queuedBytes += item.SizeBytes
	}
	o.mu.Unlock()

	return sent
}

// dropExpiredLocked removes items older than MaxAge.
// Must be called with lock held.
func (o *Outbox) dropExpiredLocked() {
	now := time.Now()
	var remaining []*OutboxItem
	for _, item := range o.items {
		if now.Sub(item.EnqueuedAt) > o.config.MaxAge {
			o.queuedBytes -= item.SizeBytes
			o.totalDropped++
			slog.Debug("outbox: dropped expired item",
				"platform", item.Platform,
				"session_key", item.SessionKey,
				"age", now.Sub(item.EnqueuedAt))
		} else {
			remaining = append(remaining, item)
		}
	}
	o.items = remaining
}

// Len returns the number of items in the outbox.
func (o *Outbox) Len() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.items)
}

// Bytes returns the total bytes in the outbox.
func (o *Outbox) Bytes() int64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.queuedBytes
}

// Stats returns outbox statistics.
func (o *Outbox) Stats() OutboxStats {
	o.mu.Lock()
	defer o.mu.Unlock()
	return OutboxStats{
		Items:         len(o.items),
		Bytes:         o.queuedBytes,
		TotalEnqueued: o.totalEnqueued,
		TotalDropped:  o.totalDropped,
		TotalFlushed:  o.totalFlushed,
	}
}

// OutboxStats contains outbox statistics.
type OutboxStats struct {
	Items         int   // Current number of items
	Bytes         int64 // Current bytes buffered
	TotalEnqueued int64 // Total items ever enqueued
	TotalDropped  int64 // Total items dropped
	TotalFlushed  int64 // Total items successfully flushed
}

// Clear removes all items from the outbox.
func (o *Outbox) Clear() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.totalDropped += int64(len(o.items))
	o.items = nil
	o.queuedBytes = 0
}

// HasItems returns true if there are items in the outbox.
func (o *Outbox) HasItems() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.items) > 0
}

// HasItemsForPlatform returns true if there are items for a specific platform.
func (o *Outbox) HasItemsForPlatform(platform string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, item := range o.items {
		if item.Platform == platform {
			return true
		}
	}
	return false
}
