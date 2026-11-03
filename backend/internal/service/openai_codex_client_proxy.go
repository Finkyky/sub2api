package service

import (
	"errors"
	"net/url"
	"strings"

	"golang.org/x/net/http/httpproxy"
)

func resolveCodexWebSocketProxy(target *url.URL, configured string) (string, error) {
	if configured = strings.TrimSpace(configured); configured != "" {
		return configured, nil
	}
	if target == nil {
		return "", errors.New("invalid Codex WebSocket URL")
	}
	proxyTarget := *target
	if proxyTarget.Scheme == "wss" {
		proxyTarget.Scheme = "https"
	} else if proxyTarget.Scheme == "ws" {
		proxyTarget.Scheme = "http"
	}
	selected, err := httpproxy.FromEnvironment().ProxyFunc()(&proxyTarget)
	if err != nil {
		return "", errors.New("invalid Codex WebSocket environment proxy")
	}
	if selected != nil {
		return selected.String(), nil
	}
	return "", nil
}
