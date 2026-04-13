package core

import (
	"testing"
	"time"
)

func TestOutboxEnqueue(t *testing.T) {
	o := NewOutbox(DefaultOutboxConfig())

	if !o.Enqueue("test content", "test-platform", "test-session", nil) {
		t.Error("expected enqueue to succeed")
	}

	if o.Len() != 1 {
		t.Errorf("expected 1 item, got %d", o.Len())
	}
}

func TestOutboxEnqueueTooLarge(t *testing.T) {
	cfg := DefaultOutboxConfig()
	cfg.MaxItemBytes = 10
	o := NewOutbox(cfg)

	largeContent := "this content is way too large for the max item bytes"
	if o.Enqueue(largeContent, "test-platform", "test-session", nil) {
		t.Error("expected enqueue to fail for too large item")
	}

	if o.Len() != 0 {
		t.Errorf("expected 0 items, got %d", o.Len())
	}
}

func TestOutboxMaxItems(t *testing.T) {
	cfg := DefaultOutboxConfig()
	cfg.MaxItems = 3
	o := NewOutbox(cfg)

	for i := 0; i < 5; i++ {
		o.Enqueue("content", "platform", "session", nil)
	}

	if o.Len() != 3 {
		t.Errorf("expected 3 items (max), got %d", o.Len())
	}

	stats := o.Stats()
	if stats.TotalDropped < 2 {
		t.Errorf("expected at least 2 dropped, got %d", stats.TotalDropped)
	}
}

func TestOutboxFlush(t *testing.T) {
	o := NewOutbox(DefaultOutboxConfig())

	o.Enqueue("content1", "platform", "session", nil)
	o.Enqueue("content2", "platform", "session", nil)

	var sent []string
	count := o.Flush(func(item *OutboxItem) error {
		sent = append(sent, item.Content)
		return nil
	})

	if count != 2 {
		t.Errorf("expected 2 sent, got %d", count)
	}

	if len(sent) != 2 {
		t.Errorf("expected 2 items in sent, got %d", len(sent))
	}

	if o.Len() != 0 {
		t.Errorf("expected 0 items after flush, got %d", o.Len())
	}
}

func TestOutboxFlushExpired(t *testing.T) {
	cfg := DefaultOutboxConfig()
	cfg.MaxAge = 1 * time.Millisecond
	o := NewOutbox(cfg)

	o.Enqueue("content", "platform", "session", nil)

	// Wait for expiration
	time.Sleep(10 * time.Millisecond)

	count := o.Flush(func(item *OutboxItem) error {
		return nil
	})

	if count != 0 {
		t.Errorf("expected 0 sent (expired), got %d", count)
	}
}

func TestOutboxFlushForPlatform(t *testing.T) {
	o := NewOutbox(DefaultOutboxConfig())

	o.Enqueue("content1", "platform-a", "session", nil)
	o.Enqueue("content2", "platform-b", "session", nil)
	o.Enqueue("content3", "platform-a", "session", nil)

	var sent []string
	count := o.FlushForPlatform("platform-a", func(item *OutboxItem) error {
		sent = append(sent, item.Content)
		return nil
	})

	if count != 2 {
		t.Errorf("expected 2 sent, got %d", count)
	}

	if o.Len() != 1 {
		t.Errorf("expected 1 item remaining, got %d", o.Len())
	}
}

func TestOutboxStats(t *testing.T) {
	o := NewOutbox(DefaultOutboxConfig())

	o.Enqueue("content1", "platform", "session", nil)
	o.Enqueue("content2", "platform", "session", nil)

	stats := o.Stats()
	if stats.Items != 2 {
		t.Errorf("expected 2 items, got %d", stats.Items)
	}
	if stats.TotalEnqueued != 2 {
		t.Errorf("expected 2 total enqueued, got %d", stats.TotalEnqueued)
	}

	o.Flush(func(item *OutboxItem) error { return nil })

	stats = o.Stats()
	if stats.TotalFlushed != 2 {
		t.Errorf("expected 2 total flushed, got %d", stats.TotalFlushed)
	}
}

func TestOutboxHasItems(t *testing.T) {
	o := NewOutbox(DefaultOutboxConfig())

	if o.HasItems() {
		t.Error("expected no items initially")
	}

	o.Enqueue("content", "platform", "session", nil)

	if !o.HasItems() {
		t.Error("expected items after enqueue")
	}
}

func TestOutboxHasItemsForPlatform(t *testing.T) {
	o := NewOutbox(DefaultOutboxConfig())

	o.Enqueue("content", "platform-a", "session", nil)

	if !o.HasItemsForPlatform("platform-a") {
		t.Error("expected items for platform-a")
	}
	if o.HasItemsForPlatform("platform-b") {
		t.Error("expected no items for platform-b")
	}
}

func TestOutboxClear(t *testing.T) {
	o := NewOutbox(DefaultOutboxConfig())

	o.Enqueue("content1", "platform", "session", nil)
	o.Enqueue("content2", "platform", "session", nil)

	o.Clear()

	if o.Len() != 0 {
		t.Errorf("expected 0 items after clear, got %d", o.Len())
	}
}
