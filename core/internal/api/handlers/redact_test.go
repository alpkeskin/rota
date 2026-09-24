package handlers

import (
	"reflect"
	"strings"
	"testing"
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
