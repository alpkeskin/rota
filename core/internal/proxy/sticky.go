package proxy

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/alpkeskin/rota/core/internal/metrics"
)

// Memory bounds. A user can hold at most maxStickyPerUser sessions, so one
// tenant can't crowd out everyone else; past either limit new sessions are
// served without pinning. A full table is swept at most once a minute.
const (
	maxStickySessions = 200_000
	maxStickyPerUser  = 10_000
	stickySweepEvery  = time.Minute
)

type stickyEntry struct {
	userID  int
	proxyID int
	expires time.Time
}

// StickySessions pins a user's session id to one upstream proxy for the
// session's lifetime, so requests carrying the same id exit from the same IP.
type StickySessions struct {
	// shared, when set, holds the sessions for all instances; the local
	// maps are used only while it is unreachable.
	shared SharedSticky

	mu        sync.Mutex
	entries   map[string]stickyEntry
	perUser   map[int]int
	lastSweep time.Time
	now       func() time.Time
}

// NewStickySessions creates an empty store.
func NewStickySessions() *StickySessions {
	return &StickySessions{entries: make(map[string]stickyEntry), perUser: make(map[int]int), now: time.Now}
}

func stickyKey(userID int, session string) string {
	return strconv.Itoa(userID) + ":" + session
}

// SharedSticky is a sticky session store shared by all instances
// (sharedstate.Store).
type SharedSticky interface {
	StickyGet(ctx context.Context, userID int, session string) (int, bool, error)
	StickyBind(ctx context.Context, userID int, session string, proxyID int, ttl time.Duration, perUserCap int) error
}

// Lookup returns the session's pinned proxy from the shared store, or from
// local memory when there is none or it is unreachable.
func (s *StickySessions) Lookup(ctx context.Context, userID int, session string) (int, bool) {
	if s.shared != nil {
		id, ok, err := s.shared.StickyGet(ctx, userID, session)
		if err == nil && ok {
			return id, true
		}
		if err != nil {
			metrics.SharedStateErrors.WithLabelValues("sticky").Inc()
		}
		// Not in the shared store: it may have been pinned locally while
		// the store was unreachable. Keep that pin rather than moving the
		// session to a new exit IP when the store comes back.
	}
	return s.Get(userID, session)
}

// Pin pins a session like Bind, in the shared store when there is one.
func (s *StickySessions) Pin(ctx context.Context, userID int, session string, proxyID int, ttl time.Duration) {
	if s.shared != nil {
		err := s.shared.StickyBind(ctx, userID, session, proxyID, ttl, maxStickyPerUser)
		if err == nil {
			return
		}
		metrics.SharedStateErrors.WithLabelValues("sticky").Inc()
	}
	s.Bind(userID, session, proxyID, ttl)
}

// Get returns the pinned proxy for a session, if any and not expired.
func (s *StickySessions) Get(userID int, session string) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[stickyKey(userID, session)]
	if !ok || !s.now().Before(e.expires) {
		return 0, false
	}
	return e.proxyID, true
}

// Bind pins (or re-pins) a session to a proxy. The lifetime is counted from
// the session's first use and is not extended by re-pinning, so a session's
// exit IP only changes when its proxy stops working.
func (s *StickySessions) Bind(userID int, session string, proxyID int, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := stickyKey(userID, session)
	now := s.now()
	expires := now.Add(ttl)
	if e, ok := s.entries[key]; ok && now.Before(e.expires) {
		expires = e.expires
	}
	if _, exists := s.entries[key]; !exists {
		full := len(s.entries) >= maxStickySessions || s.perUser[userID] >= maxStickyPerUser
		if full && now.Sub(s.lastSweep) >= stickySweepEvery {
			s.sweepLocked(now)
			full = len(s.entries) >= maxStickySessions || s.perUser[userID] >= maxStickyPerUser
		}
		if full {
			return
		}
		s.perUser[userID]++
	}
	s.entries[key] = stickyEntry{userID: userID, proxyID: proxyID, expires: expires}
}

// Sweep removes expired sessions.
func (s *StickySessions) Sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.now())
}

func (s *StickySessions) sweepLocked(now time.Time) {
	s.lastSweep = now
	for k, e := range s.entries {
		if !now.Before(e.expires) {
			delete(s.entries, k)
			if s.perUser[e.userID]--; s.perUser[e.userID] <= 0 {
				delete(s.perUser, e.userID)
			}
		}
	}
}

// Len returns the number of stored sessions (including expired ones not yet swept).
func (s *StickySessions) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}
