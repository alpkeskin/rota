package proxy

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/alpkeskin/rota/core/internal/models"
)

// Per-request routing options are passed in the proxy username, the same
// convention commercial proxy networks use:
//
//	alice-country-us-city-new_york-session-a1b2c3-sesstime-30
//
// The part before the first recognised "-<option>-" is the account username.
// Options (all optional, any order, each at most once):
//
//	country   ISO 3166-1 alpha-2 code of the exit IP ("us")
//	city      city of the exit IP, "_" for spaces ("new_york")
//	session   sticky session id: requests with the same id reuse the same
//	          upstream proxy while it keeps working ([A-Za-z0-9]{1,64})
//	sesstime  sticky session lifetime in minutes (1-1440, default 10)
const (
	optCountry  = "country"
	optCity     = "city"
	optSession  = "session"
	optSessTime = "sesstime"

	defaultSessionTTL = 10 * time.Minute
	maxSessionTTL     = 24 * time.Hour
)

var (
	countryRe = regexp.MustCompile(`^[a-zA-Z]{2}$`)
	cityRe    = regexp.MustCompile(`^[a-zA-Z0-9_.']{1,64}$`)
	sessionRe = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)
)

func isOption(s string) bool {
	switch s {
	case optCountry, optCity, optSession, optSessTime:
		return true
	}
	return false
}

// Targeting restricts which upstream proxies may serve a request.
type Targeting struct {
	Country string // upper-case ISO code, "" = any
	City    string // lower-case, spaces not underscores, "" = any
}

// Empty reports whether no restriction applies.
func (t Targeting) Empty() bool { return t.Country == "" && t.City == "" }

// Matches reports whether a proxy satisfies the targeting.
func (t Targeting) Matches(p *models.Proxy) bool {
	if t.Country != "" && (p.CountryCode == nil || !strings.EqualFold(*p.CountryCode, t.Country)) {
		return false
	}
	if t.City != "" && (p.CityName == nil || !strings.EqualFold(strings.TrimSpace(*p.CityName), t.City)) {
		return false
	}
	return true
}

func (t Targeting) String() string {
	var parts []string
	if t.Country != "" {
		parts = append(parts, "country="+t.Country)
	}
	if t.City != "" {
		parts = append(parts, "city="+t.City)
	}
	return strings.Join(parts, " ")
}

// UsernameOptions is the result of parsing a proxy username.
type UsernameOptions struct {
	Username   string // the account username
	Target     Targeting
	Session    string        // sticky session id, "" = none
	SessionTTL time.Duration // lifetime of the sticky session
}

// ParseUsername splits a proxy username into the account name and routing
// options. A username without options is returned unchanged. An error means
// the options are present but invalid (the client should fix the request).
func ParseUsername(raw string) (UsernameOptions, error) {
	out := UsernameOptions{Username: raw, SessionTTL: defaultSessionTTL}
	tokens := strings.Split(raw, "-")
	start := -1
	for i := 1; i < len(tokens); i++ {
		if isOption(tokens[i]) {
			start = i
			break
		}
	}
	if start < 0 {
		return out, nil
	}
	out.Username = strings.Join(tokens[:start], "-")
	if out.Username == "" {
		return out, fmt.Errorf("missing username before routing options")
	}

	rest := tokens[start:]
	if len(rest)%2 != 0 {
		return out, fmt.Errorf("routing option %q has no value", rest[len(rest)-1])
	}
	seen := map[string]bool{}
	for i := 0; i < len(rest); i += 2 {
		key, val := rest[i], rest[i+1]
		if !isOption(key) {
			return out, fmt.Errorf("unknown routing option %q", key)
		}
		if seen[key] {
			return out, fmt.Errorf("routing option %q given twice", key)
		}
		seen[key] = true
		switch key {
		case optCountry:
			if !countryRe.MatchString(val) {
				return out, fmt.Errorf("country must be a 2-letter ISO code, got %q", val)
			}
			out.Target.Country = strings.ToUpper(val)
		case optCity:
			if !cityRe.MatchString(val) {
				return out, fmt.Errorf("invalid city %q (use _ for spaces)", val)
			}
			out.Target.City = strings.ToLower(strings.ReplaceAll(val, "_", " "))
		case optSession:
			if !sessionRe.MatchString(val) {
				return out, fmt.Errorf("session id must be 1-64 letters or digits, got %q", val)
			}
			out.Session = val
		case optSessTime:
			m, err := strconv.Atoi(val)
			if err != nil || m < 1 || time.Duration(m)*time.Minute > maxSessionTTL {
				return out, fmt.Errorf("sesstime must be 1-1440 minutes, got %q", val)
			}
			out.SessionTTL = time.Duration(m) * time.Minute
		}
	}
	if seen[optSessTime] && !seen[optSession] {
		return out, fmt.Errorf("sesstime needs a session id")
	}
	return out, nil
}

// ReservedUsername reports whether a proxy account username would be read as
// carrying routing options, which would make the account unusable. Account
// creation rejects such names.
func ReservedUsername(username string) bool {
	tokens := strings.Split(username, "-")
	for i := 1; i < len(tokens); i++ {
		if isOption(tokens[i]) {
			return true
		}
	}
	return false
}
