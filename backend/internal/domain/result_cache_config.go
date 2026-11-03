package domain

import "fmt"

// GroupResponseResultCacheConfig is server-owned, versioned group policy.
type GroupResponseResultCacheConfig struct {
	Enabled           bool  `json:"enabled"`
	TTLSeconds        int   `json:"ttl_seconds"`
	MaxResponseBytes  int   `json:"max_response_bytes"`
	AllowGroupSharing bool  `json:"allow_group_sharing"`
	Revision          int64 `json:"revision"`
}

// GroupResponseResultCacheInput deliberately excludes server-owned Revision.
type GroupResponseResultCacheInput struct {
	Enabled           bool `json:"enabled"`
	TTLSeconds        int  `json:"ttl_seconds"`
	MaxResponseBytes  int  `json:"max_response_bytes"`
	AllowGroupSharing bool `json:"allow_group_sharing"`
}

func (in GroupResponseResultCacheInput) Config() GroupResponseResultCacheConfig {
	return GroupResponseResultCacheConfig{Enabled: in.Enabled, TTLSeconds: in.TTLSeconds, MaxResponseBytes: in.MaxResponseBytes, AllowGroupSharing: in.AllowGroupSharing}
}
func NormalizeGroupResponseResultCacheConfig(c GroupResponseResultCacheConfig) (GroupResponseResultCacheConfig, error) {
	if c.TTLSeconds == 0 {
		c.TTLSeconds = 30
	}
	if c.MaxResponseBytes == 0 {
		c.MaxResponseBytes = 2 << 20
	}
	if c.Revision == 0 {
		c.Revision = 1
	}
	if c.TTLSeconds < 1 || c.TTLSeconds > 86400 {
		return c, fmt.Errorf("result cache ttl_seconds must be between 1 and 86400")
	}
	if c.MaxResponseBytes < 1024 || c.MaxResponseBytes > 2<<20 {
		return c, fmt.Errorf("result cache max_response_bytes must be between 1024 and 2097152")
	}
	if c.Revision < 1 {
		return c, fmt.Errorf("result cache revision must be positive")
	}
	return c, nil
}
