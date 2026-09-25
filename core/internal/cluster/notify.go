package cluster

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/alpkeskin/rota/core/internal/metrics"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Change topics published when shared configuration changes.
const (
	TopicSettings = "settings" // settings row changed: reload rotation, auth, rate limits, GeoIP
	TopicProxies  = "proxies"  // upstream proxies changed: refresh selectors, drop transports
	TopicUsers    = "users"    // proxy users or pools changed: drop cached users and chains
)

const (
	notifyChannel  = "rota_changes"
	handlerTimeout = 30 * time.Second
)

// Notifier tells the other instances that shared configuration changed, so
// they reload it at once instead of on their next periodic refresh.
//
// Delivery is best effort (Postgres NOTIFY). After its listening connection
// drops, an instance runs every handler once, since it may have missed
// events while disconnected; the periodic refreshes remain as the backstop.
type Notifier struct {
	pool *pgxpool.Pool
	log  *logger.Logger
	id   string

	mu       sync.Mutex
	handlers map[string][]func(context.Context)
	cancel   context.CancelFunc
	done     chan struct{}

	retry time.Duration
}

// NewNotifier creates a notifier. Handlers run for changes made by other
// instances only; the instance making a change updates itself directly.
func NewNotifier(pool *pgxpool.Pool, log *logger.Logger) *Notifier {
	b := make([]byte, 8)
	rand.Read(b) //nolint:errcheck // never fails
	return &Notifier{
		pool:     pool,
		log:      log,
		id:       hex.EncodeToString(b),
		handlers: make(map[string][]func(context.Context)),
		retry:    5 * time.Second,
	}
}

// Subscribe registers fn to run when another instance publishes topic.
// Register every handler before Start.
func (n *Notifier) Subscribe(topic string, fn func(ctx context.Context)) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.handlers[topic] = append(n.handlers[topic], fn)
}

// Publish tells the other instances that topic changed. A failure is logged,
// not returned: the change itself has been saved, and the other instances
// pick it up on their next periodic refresh anyway.
func (n *Notifier) Publish(ctx context.Context, topic string) {
	if n == nil {
		return
	}
	// Detached from the request: a client hanging up right after a change
	// shouldn't keep it from the other instances.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := n.pool.Exec(ctx, `SELECT pg_notify($1, $2)`, notifyChannel, topic+":"+n.id); err != nil {
		n.log.Warn("failed to notify other instances of a change; they will pick it up on their next refresh",
			"topic", topic, "error", err)
	}
}

// Start begins listening in the background.
func (n *Notifier) Start() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.done != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	n.cancel = cancel
	n.done = make(chan struct{})
	go n.run(ctx, n.done)
}

// Stop stops listening and waits for a running handler to return.
func (n *Notifier) Stop() {
	n.mu.Lock()
	cancel, done := n.cancel, n.done
	n.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func (n *Notifier) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	first := true
	for {
		n.listen(ctx, first)
		first = false
		if !sleepCtx(ctx, n.retry) {
			return
		}
	}
}

// listen holds a LISTEN connection until it fails or ctx is cancelled.
func (n *Notifier) listen(ctx context.Context, first bool) {
	conn, err := dedicatedConn(ctx, n.pool, "rota-notify")
	if err != nil {
		if ctx.Err() == nil {
			n.log.Warn("change notifications: cannot connect to the database", "error", err)
		}
		return
	}
	defer closeConn(conn)
	if _, err := conn.Exec(ctx, `LISTEN `+notifyChannel); err != nil {
		if ctx.Err() == nil {
			n.log.Warn("change notifications: LISTEN failed", "error", err)
		}
		return
	}
	if !first {
		// Changes made while we were disconnected were not delivered.
		n.dispatchAll(ctx)
	}
	for {
		note, err := conn.WaitForNotification(ctx)
		if err != nil {
			if ctx.Err() == nil {
				n.log.Warn("change notifications: connection lost; reconnecting", "error", err)
			}
			return
		}
		n.handle(ctx, note)
	}
}

func (n *Notifier) handle(ctx context.Context, note *pgconn.Notification) {
	topic, sender, ok := strings.Cut(note.Payload, ":")
	if !ok || sender == n.id {
		return
	}
	metrics.ChangeEvents.WithLabelValues(topic).Inc()
	n.dispatch(ctx, topic)
}

func (n *Notifier) dispatchAll(ctx context.Context) {
	n.mu.Lock()
	topics := make([]string, 0, len(n.handlers))
	for t := range n.handlers {
		topics = append(topics, t)
	}
	n.mu.Unlock()
	for _, t := range topics {
		n.dispatch(ctx, t)
	}
}

func (n *Notifier) dispatch(ctx context.Context, topic string) {
	n.mu.Lock()
	fns := append([]func(context.Context){}, n.handlers[topic]...)
	n.mu.Unlock()
	for _, fn := range fns {
		n.call(ctx, topic, fn)
	}
}

func (n *Notifier) call(ctx context.Context, topic string, fn func(context.Context)) {
	defer func() {
		if r := recover(); r != nil {
			n.log.Error("panic in change handler", "topic", topic, "panic", r)
		}
	}()
	hctx, cancel := context.WithTimeout(ctx, handlerTimeout)
	defer cancel()
	fn(hctx)
}
