package handlers

import (
	"context"
	"net/url"
	"strings"

	"github.com/alpkeskin/rota/core/internal/auth"
)

// Redacted replaces a secret shown to a caller who may not see it. Settings
// updates treat it as "keep the stored value", so a form that round-trips a
// redacted page can never overwrite the real secret with it.
const Redacted = "••••••"

// canSeeSecrets reports whether the caller may read credentials embedded in
// configuration (webhook URLs, source URLs, health-check headers, license
// keys): only admins, who are also the only ones who can change them.
func canSeeSecrets(ctx context.Context) bool {
	p := auth.FromContext(ctx)
	return p != nil && p.Role.AtLeast(auth.RoleAdmin)
}

// redactURL keeps only the scheme and host of a URL. Webhook and list URLs
// often carry their credential in the path (Slack, Discord), query
// (?apikey=) or userinfo, so everything past the host is hidden.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return Redacted
	}
	out := u.Scheme + "://" + u.Host
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.User != nil {
		out += "/" + Redacted
	}
	return out
}

// redactHeaders hides header values ("Name: value" lines), keeping names.
func redactHeaders(headers []string) []string {
	out := make([]string, len(headers))
	for i, h := range headers {
		name, _, found := strings.Cut(h, ":")
		if !found {
			out[i] = Redacted
			continue
		}
		out[i] = strings.TrimSpace(name) + ": " + Redacted
	}
	return out
}

// restoreRedactedHeaders puts back the stored value of every "Name: ••••••"
// line (matched by header name), so saving a redacted form keeps secrets.
func restoreRedactedHeaders(incoming, stored []string) []string {
	byName := map[string]string{}
	for _, h := range stored {
		if name, _, ok := strings.Cut(h, ":"); ok {
			byName[strings.ToLower(strings.TrimSpace(name))] = h
		}
	}
	out := make([]string, 0, len(incoming))
	for _, h := range incoming {
		name, value, ok := strings.Cut(h, ":")
		if ok && strings.TrimSpace(value) == Redacted {
			if orig, found := byName[strings.ToLower(strings.TrimSpace(name))]; found {
				out = append(out, orig)
			}
			continue // a redacted header with no stored value is dropped
		}
		if h == Redacted {
			continue
		}
		out = append(out, h)
	}
	return out
}
