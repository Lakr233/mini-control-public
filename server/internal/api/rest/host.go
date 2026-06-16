package rest

import (
	"net/http"
	"net/url"
	"strings"
)

func (h *Handler) requireBrowserHost(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.matchesBrowserHost(r) {
			http.NotFound(w, r)
			return
		}
		next(w, r)
	}
}

func (h *Handler) requireWorkerHost(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.matchesWorkerHost(r) {
			http.NotFound(w, r)
			return
		}
		next(w, r)
	}
}

func (h *Handler) matchesBrowserHost(r *http.Request) bool {
	return hostMatches(h.config.BrowserPublicHost, requestHost(r))
}

func (h *Handler) matchesWorkerHost(r *http.Request) bool {
	return hostMatches(h.config.WorkerPublicHost, requestHost(r))
}

func (h *Handler) workerScriptAddress(r *http.Request) string {
	if configured := normalizeHost(h.config.WorkerPublicHost); configured != "" {
		return configured
	}
	return requestHost(r)
}

func hostMatches(configuredHost, actualHost string) bool {
	configured := normalizeHost(configuredHost)
	actual := normalizeHost(actualHost)
	if configured == "" {
		return true
	}
	return configured == actual
}

func requestHost(r *http.Request) string {
	if r == nil {
		return ""
	}
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); forwarded != "" {
		if first, _, ok := strings.Cut(forwarded, ","); ok {
			return normalizeHost(first)
		}
		return normalizeHost(forwarded)
	}
	return normalizeHost(r.Host)
}

// normalizeHost lowercases a host value and strips any scheme, port, and
// IPv6 brackets so configured and request hosts compare consistently.
func normalizeHost(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		if parsed, err := url.Parse(value); err == nil {
			value = strings.TrimSpace(parsed.Host)
		}
	}
	if strings.HasPrefix(value, "[") {
		// Bracketed IPv6, with or without a port: "[::1]:8080" -> "::1".
		if address, _, found := strings.Cut(value, "]"); found {
			return strings.TrimPrefix(address, "[")
		}
		return value
	}
	if host, _, found := strings.Cut(value, ":"); found && !strings.Contains(host, ":") && host != "" {
		// "example.com:8080" -> "example.com"; bare IPv6 stays untouched.
		return host
	}
	return value
}
