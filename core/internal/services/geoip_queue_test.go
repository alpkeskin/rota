package services

import (
	"fmt"
	"testing"
)

func TestGeoQueueEnqueueDrain(t *testing.T) {
	q := newGeoQueue()

	// Reserved/unparseable are dropped, duplicates deduped.
	n := q.Enqueue([]string{
		"8.8.8.8:8080",
		"8.8.8.8:8080", // duplicate
		"user:pass@1.1.1.1:1080",
		"10.0.0.1:80", // reserved
		"garbage",     // unparseable
	})
	if n != 2 {
		t.Fatalf("enqueued = %d, want 2", n)
	}
	if q.Len() != 2 {
		t.Fatalf("Len = %d, want 2", q.Len())
	}

	// A repeated sync does not re-queue what is already pending.
	if n := q.Enqueue([]string{"8.8.8.8:8080"}); n != 0 {
		t.Fatalf("re-enqueue = %d, want 0", n)
	}

	// FIFO drain.
	out := q.Drain(1)
	if len(out) != 1 || out[0] != "8.8.8.8:8080" {
		t.Fatalf("drain = %v, want [8.8.8.8:8080]", out)
	}
	out = q.Drain(10)
	if len(out) != 1 || out[0] != "user:pass@1.1.1.1:1080" {
		t.Fatalf("drain = %v, want [user:pass@1.1.1.1:1080]", out)
	}
	if q.Len() != 0 {
		t.Fatalf("Len = %d, want 0", q.Len())
	}

	// Draining an empty queue returns nothing.
	if out := q.Drain(5); len(out) != 0 {
		t.Fatalf("drain = %v, want empty", out)
	}
}

func TestGeoQueueCap(t *testing.T) {
	q := newGeoQueue()
	addresses := make([]string, 0, geoQueueCap+10)
	for i := 0; i < geoQueueCap+10; i++ {
		addresses = append(addresses, fmt.Sprintf("9.0.%d.%d:8080", i/256, i%256))
	}

	n := q.Enqueue(addresses)
	if n != geoQueueCap {
		t.Fatalf("enqueued = %d, want cap %d (excess stays in the DB backlog)", n, geoQueueCap)
	}
	if q.Len() != geoQueueCap {
		t.Fatalf("Len = %d, want cap %d", q.Len(), geoQueueCap)
	}
}
