package repository

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/codexclient"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (s *httpUpstreamService) resolveCodexClientProfile(request *http.Request, profile service.HTTPUpstreamProfile) service.HTTPUpstreamProfile {
	if s == nil || s.cfg == nil || s.cfg.Gateway.OpenAICodexClientProfile != codexclient.Profile || request == nil || !codexclient.IsCodexURL(request.URL) || request.Header.Get("originator") == "" {
		return profile
	}
	switch profile {
	case service.HTTPUpstreamProfileOpenAI:
		profile = service.HTTPUpstreamProfileCodexLinux
	case service.HTTPUpstreamProfileOpenAIHarvest:
		profile = service.HTTPUpstreamProfileCodexLinuxHarvest
	default:
		return profile
	}
	request.Header.Set("User-Agent", codexclient.UserAgent)
	request.Header.Set("originator", codexclient.Originator)
	request.Header.Set("version", codexclient.Version)
	request.Header.Del("Accept-Encoding")
	return profile
}
