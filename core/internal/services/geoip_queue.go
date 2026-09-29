package services

import "sync"

// geoQueueCap bounds the in-memory geo enrichment queue. Addresses that do
// not fit are simply not queued — nothing is lost, because every un-geotagged
// proxy stays in the DB backlog (country_code IS NULL) and is picked up by
// the worker's DB drain.
const geoQueueCap = 10000

// geoQueue is an in-memory FIFO of proxy addresses awaiting GeoIP
// enrichment. The database is the source of truth; the queue is a fast path
// so freshly imported addresses are processed without waiting for the next
// DB drain.
type geoQueue struct {
	mu      sync.Mutex
	queue   []string
	pending map[string]struct{}
}

func newGeoQueue() *geoQueue {
	return &geoQueue{
		queue:   make([]string, 0, 256),
		pending: make(map[string]struct{}),
	}
}

// Enqueue normalizes addresses, drops reserved/unparseable ones, dedupes
// against the pending set (a repeated sync does not re-queue), and adds up
// to the cap. It returns the number of addresses actually queued.
func (q *geoQueue) Enqueue(addresses []string) int {
	if len(addresses) == 0 {
		return 0
	}

	added := 0
	q.mu.Lock()
	for _, addr := range addresses {
		if len(q.queue) >= geoQueueCap {
			break
		}
		if _, dup := q.pending[addr]; dup {
			continue
		}
		if _, reason := ExtractPublicIP(addr); reason != "" {
			continue
		}
		q.pending[addr] = struct{}{}
		q.queue = append(q.queue, addr)
		added++
	}
	q.mu.Unlock()

	return added
}

// Drain removes up to max addresses from the front of the queue (FIFO).
func (q *geoQueue) Drain(max int) []string {
	if max <= 0 {
		return nil
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	if max > len(q.queue) {
		max = len(q.queue)
	}
	out := q.queue[:max]
	q.queue = q.queue[max:]
	for _, addr := range out {
		delete(q.pending, addr)
	}
	return out
}

// Len returns the number of addresses currently queued.
func (q *geoQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.queue)
}
