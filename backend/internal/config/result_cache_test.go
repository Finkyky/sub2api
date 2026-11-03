package config

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestResultCacheRuntimeDefaults(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, ResultCacheConfig{IOTimeoutMS: 300, WaitTimeoutMS: 2000, LeaseTTLMS: 30000, GenerationTimeoutMS: 600000, MaxConcurrentFills: 32}, cfg.Gateway.ResultCache)
}

func TestResultCacheRuntimeEnv(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("GATEWAY_RESULT_CACHE_IO_TIMEOUT_MS", "100")
	t.Setenv("GATEWAY_RESULT_CACHE_WAIT_TIMEOUT_MS", "0")
	t.Setenv("GATEWAY_RESULT_CACHE_LEASE_TTL_MS", "6000")
	t.Setenv("GATEWAY_RESULT_CACHE_GENERATION_TIMEOUT_MS", "15000")
	t.Setenv("GATEWAY_RESULT_CACHE_MAX_CONCURRENT_FILLS", "4")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, ResultCacheConfig{IOTimeoutMS: 100, WaitTimeoutMS: 0, LeaseTTLMS: 6000, GenerationTimeoutMS: 15000, MaxConcurrentFills: 4}, cfg.Gateway.ResultCache)
}

func TestResultCacheRuntimeValidation(t *testing.T) {
	tests := []struct {
		name   string
		change func(*ResultCacheConfig)
	}{
		{"zero io", func(c *ResultCacheConfig) { c.IOTimeoutMS = 0 }},
		{"negative wait", func(c *ResultCacheConfig) { c.WaitTimeoutMS = -1 }},
		{"excessive wait", func(c *ResultCacheConfig) { c.WaitTimeoutMS = 60001 }},
		{"too short lease", func(c *ResultCacheConfig) { c.LeaseTTLMS = 2 }},
		{"io exceeds renewal window", func(c *ResultCacheConfig) { c.IOTimeoutMS = 10000 }},
		{"zero generation", func(c *ResultCacheConfig) { c.GenerationTimeoutMS = 0 }},
		{"excessive generation", func(c *ResultCacheConfig) { c.GenerationTimeoutMS = 3600001 }},
		{"zero fills", func(c *ResultCacheConfig) { c.MaxConcurrentFills = 0 }},
		{"excessive fills", func(c *ResultCacheConfig) { c.MaxConcurrentFills = 1025 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			cfg, err := Load()
			require.NoError(t, err)
			tt.change(&cfg.Gateway.ResultCache)
			require.ErrorContains(t, cfg.Validate(), "gateway.result_cache.")
		})
	}
}
