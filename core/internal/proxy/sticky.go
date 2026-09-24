package proxy

import (
	"strconv"
	"sync"
	"time"
)

// maxStickySessions bounds memory; when full, expired entries are swept and,
// if none expired, new sessions simply aren't pinned.
const maxStickySessions = 200_000

type stickyEntry struct {
	proxyID int
	expires time.Time
}

// StickySessions pins a user's session id to one upstream proxy for the
// session's lifetime, so requests carrying the same id exit from the same IP.
type StickySessions struct {
	mu      sync.Mutex
	entries map[string]stickyEntry
	now     func() time.Time
}

// NewStickySessions creates an empty store.
func NewStickySessions() *StickySessions {
	return &StickySessions{entries: make(map[string]stickyEntry), now: time.Now}
}

func stickyKey(userID int, session string) string {
	return strconv.Itoa(userID) + ":" + session
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
	if _, exists := s.entries[key]; !exists && len(s.entries) >= maxStickySessions {
		s.sweepLocked(now)
		if len(s.entries) >= maxStickySessions {
			return
		}
	}
	s.entries[key] = stickyEntry{proxyID: proxyID, expires: expires}
}

// Sweep removes expired sessions.
func (s *StickySessions) Sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.now())
}

func (s *StickySessions) sweepLocked(now time.Time) {
	for k, e := range s.entries {
		if !now.Before(e.expires) {
			delete(s.entries, k)
		}
	}
}

// Len returns the number of stored sessions (including expired ones not yet swept).
func (s *StickySessions) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}
