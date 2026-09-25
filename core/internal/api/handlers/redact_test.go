package handlers

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alpkeskin/rota/core/internal/auth"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/pkg/logger"
)

func TestRedactURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://hooks.slack.com/services/T0/B0/secret": "https://hooks.slack.com/" + Redacted,
		"https://lists.example.com/p.txt?apikey=abc":    "https://lists.example.com/" + Redacted,
		"http://user:pass@host.example/":                "http://host.example/" + Redacted,
		"https://example.com":                           "https://example.com",
		"https://example.com/":                          "https://example.com",
		"not a url":                                     Redacted,
	} {
		if got := redactURL(in); got != want {
			t.Errorf("redactURL(%q) = %q, want %q", in, got, want)
		}
		if strings.Contains(redactURL(in), "secret") || strings.Contains(redactURL(in), "abc") || strings.Contains(redactURL(in), "pass") {
			t.Errorf("redactURL(%q) leaks a secret", in)
		}
	}
}

func TestRedactAndRestoreHeaders(t *testing.T) {
	stored := []string{"Authorization: Bearer s3cret", "X-Env: prod"}
	red := redactHeaders(stored)
	if !reflect.DeepEqual(red, []string{"Authorization: " + Redacted, "X-Env: " + Redacted}) {
		t.Fatalf("redactHeaders = %q", red)
	}
	// Round-tripping a redacted form keeps the stored values; edits apply;
	// new headers are added; removed ones stay removed.
	incoming := []string{"authorization: " + Redacted, "X-New: 1"}
	got := restoreRedactedHeaders(incoming, stored)
	if !reflect.DeepEqual(got, []string{"Authorization: Bearer s3cret", "X-New: 1"}) {
		t.Fatalf("restoreRedactedHeaders = %q", got)
	}
	// A redacted header with nothing stored under that name is dropped,
	// never saved as the placeholder.
	if got := restoreRedactedHeaders([]string{"X-Ghost: " + Redacted, Redacted}, stored); len(got) != 0 {
		t.Fatalf("placeholders saved: %q", got)
	}
}

func TestRedactSourceHidesURLInLastError(t *testing.T) {
	e := `fetch failed: Get "https://list.example.com/get?apikey=SECRET": dial tcp: refused`
	src := &models.ProxySource{URL: "https://list.example.com/get?apikey=SECRET", LastError: &e}
	redactSource(src)
	if strings.Contains(src.URL, "SECRET") || strings.Contains(*src.LastError, "SECRET") {
		t.Fatalf("secret survived: url=%q last_error=%q", src.URL, *src.LastError)
	}
	if !strings.Contains(*src.LastError, "dial tcp: refused") || !strings.Contains(*src.LastError, "list.example.com") {
		t.Fatalf("lost the useful part: %q", *src.LastError)
	}
	// Userinfo credentials too.
	if got := redactURLsInText("x https://acct:key@download.example.com/db y"); strings.Contains(got, "key") {
		t.Fatalf("userinfo survived: %q", got)
	}
}

type fakeFailures struct{ hits, clears int }

func (f *fakeFailures) Hit(context.Context, string, time.Duration) (int, error) {
	f.hits++
	return f.hits, nil
}
func (f *fakeFailures) ClearHits(context.Context, string) error { f.clears++; f.hits = 0; return nil }

func TestPasswordGuardCountsInSharedStore(t *testing.T) {
	g := NewPasswordConfirmGuard(nil, nil, logger.New("error"))
	fs := &fakeFailures{}
	g.SetShared(fs)
	p := &auth.Principal{AccountID: 7}
	for i := 0; i < passwordConfirmMaxFailures-1; i++ {
		if g.Failed(context.Background(), p, "1.2.3.4") {
			t.Fatalf("revoked after %d failures", i+1)
		}
	}
	if len(g.failures) != 0 {
		t.Fatal("counted locally although the shared store works")
	}
	g.Succeeded(7)
	if fs.clears != 1 || fs.hits != 0 {
		t.Fatal("success didn't clear the shared count")
	}
}
