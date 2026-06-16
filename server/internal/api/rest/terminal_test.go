package rest

import "testing"

func TestInternalProxyDialOptions_UsesBrowserHostForInternalClientConnections(t *testing.T) {
	handler := newAuthOnlyHandler(HandlerConfig{
		BrowserPublicHost: "https://minis.example.com",
	})

	options := handler.internalProxyDialOptions()

	if options.Host != "minis.example.com" {
		t.Fatalf("Host = %q, want %q", options.Host, "minis.example.com")
	}
	if got := options.HTTPHeader.Get("X-Forwarded-Host"); got != "minis.example.com" {
		t.Fatalf("X-Forwarded-Host = %q, want %q", got, "minis.example.com")
	}
}

func TestInternalProxyDialOptions_LeavesHostEmptyWhenBrowserHostUnset(t *testing.T) {
	handler := newAuthOnlyHandler(HandlerConfig{})

	options := handler.internalProxyDialOptions()

	if options.Host != "" {
		t.Fatalf("Host = %q, want empty", options.Host)
	}
	if got := options.HTTPHeader.Get("X-Forwarded-Host"); got != "" {
		t.Fatalf("X-Forwarded-Host = %q, want empty", got)
	}
}
