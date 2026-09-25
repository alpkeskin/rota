// Package sharedstate keeps limiter and session state in Redis so that several
// Rota instances enforce one set of limits: per-user request rates and
// connection caps, sticky sessions, per-IP proxy rate limits and login
// brute-force protection.
//
// Every operation has a local fallback in its caller. When Redis is
// unreachable the store reports an error at once for a short back-off period
// (instead of making each request wait for a timeout), and callers enforce the
// limit with in-memory, per-instance state until it comes back.
//
// Keys that belong together use a hash tag, and every script touches only
// keys of one tag, so Redis Cluster works as well as a single server.
// Scripts read the server clock (TIME), which needs Redis 5 or newer.
package sharedstate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrUnavailable is returned while the store is backing off after a failure.
var ErrUnavailable = errors.New("shared state store unavailable")

// Timings.
const (
	opTimeout   = 300 * time.Millisecond // per call; limits sit on the request path
	downBackoff = 5 * time.Second        // skip Redis this long after a failure
)

// Store is a Redis-backed shared state store.
type Store struct {
	rdb      redis.UniversalClient
	prefix   string
	instance string

	downUntil atomic.Int64 // unix nanos; calls fail fast before this
	now       func() time.Time
}

// Open creates a store for the Redis at url (redis://, rediss:// or
// unix://). prefix namespaces every key (e.g. "rota:"). It doesn't wait for
// Redis to be reachable: until it is, callers use their local fallbacks.
func Open(url, prefix string) (*Store, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS_URL: %w", err)
	}
	opts.DialTimeout = 2 * time.Second
	opts.ReadTimeout = opTimeout
	opts.WriteTimeout = opTimeout
	opts.MaxRetries = 0 // callers fall back instead of waiting on retries
	return newStore(redis.NewClient(opts), prefix), nil
}

func newStore(rdb redis.UniversalClient, prefix string) *Store {
	b := make([]byte, 8)
	rand.Read(b) //nolint:errcheck // never fails
	return &Store{rdb: rdb, prefix: prefix, instance: hex.EncodeToString(b), now: time.Now}
}

// Close closes the Redis connection pool.
func (s *Store) Close() error { return s.rdb.Close() }

// Instance is this process's id in shared counters.
func (s *Store) Instance() string { return s.instance }

// Ping checks the connection, ignoring the back-off.
func (s *Store) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	return s.rdb.Ping(ctx).Err()
}

// call runs fn unless the store is backing off, and starts a back-off when
// fn fails for any reason other than a missing key.
func (s *Store) call(ctx context.Context, fn func(ctx context.Context) error) error {
	if s.now().UnixNano() < s.downUntil.Load() {
		return ErrUnavailable
	}
	// Detached from the caller: a client hanging up mid-call must not look
	// like a Redis outage and switch every request to local limits.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), opTimeout)
	defer cancel()
	err := fn(ctx)
	if err != nil && !errors.Is(err, redis.Nil) {
		s.downUntil.Store(s.now().Add(downBackoff).UnixNano())
		return err
	}
	return nil
}

func (s *Store) key(parts ...string) string {
	k := s.prefix
	for i, p := range parts {
		if i > 0 {
			k += ":"
		}
		k += p
	}
	return k
}

// userTag is the Redis Cluster hash tag for keys of one user.
func userTag(userID int) string { return "{u" + strconv.Itoa(userID) + "}" }

// ── Rate limiting (GCRA) ────────────────────────────────────────────────

// gcra allows `burst` requests at once and then one per emission interval,
// the same budget as a token bucket (golang.org/x/time/rate) of that size.
var gcraScript = redis.NewScript(`
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + tonumber(t[2]) / 1000
local interval = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local tat = tonumber(redis.call('GET', KEYS[1]))
if not tat or tat < now then tat = now end
local newTat = tat + interval
local allowAt = newTat - interval * burst
if allowAt > now then
  return {0, math.ceil(allowAt - now)}
end
redis.call('SET', KEYS[1], string.format('%.3f', newTat), 'PX', math.ceil(newTat - now) + 1000)
return {1, 0}
`)

// AllowRate spends one request of a budget of limit requests per period
// shared by all instances. On a refusal it returns how long until the next
// request would be allowed.
func (s *Store) AllowRate(ctx context.Context, key string, limit int, period time.Duration) (bool, time.Duration, error) {
	if limit <= 0 || period <= 0 {
		return true, 0, nil
	}
	interval := float64(period.Milliseconds()) / float64(limit)
	var res []int64
	err := s.call(ctx, func(ctx context.Context) error {
		var err error
		res, err = gcraScript.Run(ctx, s.rdb, []string{s.key("rate", key)}, interval, limit).Int64Slice()
		return err
	})
	if err != nil {
		return false, 0, err
	}
	if len(res) != 2 {
		return false, 0, fmt.Errorf("unexpected rate script reply %v", res)
	}
	return res[0] == 1, time.Duration(res[1]) * time.Millisecond, nil
}

// UserRateKey is the rate key for a proxy user's requests-per-minute limit.
func UserRateKey(userID int) string { return "user" + userTag(userID) }

// ── Concurrency (per-instance counts with expiry) ───────────────────────

// Each instance keeps its own count of a user's open connections in one hash
// field, with an expiry field refreshed by Heartbeat. Counts of instances
// that stopped heartbeating (crashed, partitioned) expire and are dropped, so
// a lost instance can't hold a user's slots forever.
var acquireScript = redis.NewScript(`
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local inst, limit, ttl = ARGV[1], tonumber(ARGV[2]), tonumber(ARGV[3])
local h = redis.call('HGETALL', KEYS[1])
local counts, exps = {}, {}
for i = 1, #h, 2 do
  local f, v = h[i], tonumber(h[i + 1])
  if string.sub(f, -4) == ':exp' then exps[string.sub(f, 1, -5)] = v else counts[f] = v end
end
local total = 0
for f, c in pairs(counts) do
  if f == inst or (exps[f] and exps[f] > now) then
    total = total + c
  else
    redis.call('HDEL', KEYS[1], f, f .. ':exp')
  end
end
if total >= limit then return -1 end
redis.call('HINCRBY', KEYS[1], inst, 1)
redis.call('HSET', KEYS[1], inst .. ':exp', now + ttl)
redis.call('PEXPIRE', KEYS[1], ttl * 3)
return total + 1
`)

var releaseScript = redis.NewScript(`
local c = tonumber(redis.call('HGET', KEYS[1], ARGV[1]) or '0')
if c > 1 then
  redis.call('HINCRBY', KEYS[1], ARGV[1], -1)
else
  redis.call('HDEL', KEYS[1], ARGV[1], ARGV[1] .. ':exp')
end
return 0
`)

var heartbeatScript = redis.NewScript(`
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local inst, count, ttl = ARGV[1], tonumber(ARGV[2]), tonumber(ARGV[3])
if count > 0 then
  redis.call('HSET', KEYS[1], inst, count, inst .. ':exp', now + ttl)
  redis.call('PEXPIRE', KEYS[1], ttl * 3)
else
  redis.call('HDEL', KEYS[1], inst, inst .. ':exp')
end
return 0
`)

// ConnTTL is how long an instance's connection counts survive without a
// heartbeat. Heartbeat must run well within it.
const ConnTTL = 30 * time.Second

// connTTL is ConnTTL, shortened in tests.
var connTTL = ConnTTL

func (s *Store) connKey(userID int) string { return s.key("conn", userTag(userID)) }

// AcquireConn takes one of a user's limit connection slots shared by all
// instances; false means all are in use.
func (s *Store) AcquireConn(ctx context.Context, userID, limit int) (bool, error) {
	var n int64
	err := s.call(ctx, func(ctx context.Context) error {
		var err error
		n, err = acquireScript.Run(ctx, s.rdb, []string{s.connKey(userID)},
			s.instance, limit, connTTL.Milliseconds()).Int64()
		return err
	})
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ReleaseConn returns a slot taken with AcquireConn.
func (s *Store) ReleaseConn(ctx context.Context, userID int) error {
	return s.call(ctx, func(ctx context.Context) error {
		return releaseScript.Run(ctx, s.rdb, []string{s.connKey(userID)}, s.instance).Err()
	})
}

// HeartbeatConns sets this instance's connection count for each user (0
// removes it) and renews their expiry. Counts are this instance's own, so a
// heartbeat also corrects any drift from failed releases.
func (s *Store) HeartbeatConns(ctx context.Context, counts map[int]int) error {
	for id, c := range counts {
		err := s.call(ctx, func(ctx context.Context) error {
			return heartbeatScript.Run(ctx, s.rdb, []string{s.connKey(id)}, s.instance, c, connTTL.Milliseconds()).Err()
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// ── Sticky sessions ─────────────────────────────────────────────────────

var bindScript = redis.NewScript(`
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local proxy, ttl, cap, member = ARGV[1], tonumber(ARGV[2]), tonumber(ARGV[3]), ARGV[4]
local left = redis.call('PTTL', KEYS[1])
if left > 0 then
  -- Re-pin: keep the session's original lifetime.
  redis.call('SET', KEYS[1], proxy, 'PX', left)
  return 1
end
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now)
if redis.call('ZCARD', KEYS[2]) >= cap then return 0 end
redis.call('SET', KEYS[1], proxy, 'PX', ttl)
redis.call('ZADD', KEYS[2], now + ttl, member)
if redis.call('PTTL', KEYS[2]) < ttl then redis.call('PEXPIRE', KEYS[2], ttl) end
return 1
`)

func (s *Store) stickyKeys(userID int, session string) (string, string) {
	tag := userTag(userID)
	return s.key("sticky", tag, "s", session), s.key("sticky", tag, "idx")
}

// StickyGet returns the proxy pinned to a user's session, if any.
func (s *Store) StickyGet(ctx context.Context, userID int, session string) (int, bool, error) {
	k, _ := s.stickyKeys(userID, session)
	var v string
	err := s.call(ctx, func(ctx context.Context) error {
		var err error
		v, err = s.rdb.Get(ctx, k).Result()
		return err
	})
	if err != nil || v == "" {
		return 0, false, err
	}
	id, err := strconv.Atoi(v)
	if err != nil {
		return 0, false, nil
	}
	return id, true, nil
}

// StickyBind pins (or re-pins, keeping the original expiry) a user's session
// to a proxy. A user holds at most perUserCap live sessions; past that new
// sessions are not pinned.
func (s *Store) StickyBind(ctx context.Context, userID int, session string, proxyID int, ttl time.Duration, perUserCap int) error {
	k, idx := s.stickyKeys(userID, session)
	return s.call(ctx, func(ctx context.Context) error {
		return bindScript.Run(ctx, s.rdb, []string{k, idx}, proxyID, ttl.Milliseconds(), perUserCap, session).Err()
	})
}

// ── Sliding windows and blocks (login protection) ──────────────────────

var hitScript = redis.NewScript(`
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local window = tonumber(ARGV[1])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now - window)
redis.call('ZADD', KEYS[1], now, ARGV[2])
redis.call('PEXPIRE', KEYS[1], window)
return redis.call('ZCARD', KEYS[1])
`)

// Hit records one event under key and returns how many happened in the last
// window (including this one).
func (s *Store) Hit(ctx context.Context, key string, window time.Duration) (int, error) {
	b := make([]byte, 8)
	rand.Read(b) //nolint:errcheck
	var n int64
	err := s.call(ctx, func(ctx context.Context) error {
		var err error
		n, err = hitScript.Run(ctx, s.rdb, []string{s.key("hits", "{"+key+"}")}, window.Milliseconds(), hex.EncodeToString(b)).Int64()
		return err
	})
	return int(n), err
}

// Block marks key as blocked for d.
func (s *Store) Block(ctx context.Context, key string, d time.Duration) error {
	return s.call(ctx, func(ctx context.Context) error {
		return s.rdb.Set(ctx, s.key("block", "{"+key+"}"), "1", d).Err()
	})
}

// Blocked returns how much longer key stays blocked (0 = not blocked).
func (s *Store) Blocked(ctx context.Context, key string) (time.Duration, error) {
	var d time.Duration
	err := s.call(ctx, func(ctx context.Context) error {
		var err error
		d, err = s.rdb.PTTL(ctx, s.key("block", "{"+key+"}")).Result()
		return err
	})
	if err != nil || d < 0 {
		return 0, err
	}
	return d, nil
}
