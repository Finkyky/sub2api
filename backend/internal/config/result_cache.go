package config

import "fmt"

// ResultCacheConfig bounds optional cache work, not the normal upstream request.
// The result store currently shares the application's Redis instance.
type ResultCacheConfig struct {
	IOTimeoutMS         int `mapstructure:"io_timeout_ms"`
	WaitTimeoutMS       int `mapstructure:"wait_timeout_ms"`
	LeaseTTLMS          int `mapstructure:"lease_ttl_ms"`
	GenerationTimeoutMS int `mapstructure:"generation_timeout_ms"`
	MaxConcurrentFills  int `mapstructure:"max_concurrent_fills"`
}

func DefaultResultCacheConfig() ResultCacheConfig {
	return ResultCacheConfig{IOTimeoutMS: 300, WaitTimeoutMS: 2000, LeaseTTLMS: 30000, GenerationTimeoutMS: 600000, MaxConcurrentFills: 32}
}

func (c ResultCacheConfig) Validate() error {
	if c.IOTimeoutMS < 1 || c.IOTimeoutMS > 60000 {
		return fmt.Errorf("gateway.result_cache.io_timeout_ms must be between 1 and 60000")
	}
	if c.WaitTimeoutMS < 0 || c.WaitTimeoutMS > 60000 {
		return fmt.Errorf("gateway.result_cache.wait_timeout_ms must be between 0 and 60000")
	}
	if c.LeaseTTLMS < 3 || c.LeaseTTLMS > 300000 {
		return fmt.Errorf("gateway.result_cache.lease_ttl_ms must be between 3 and 300000")
	}
	if c.IOTimeoutMS >= c.LeaseTTLMS/3 {
		return fmt.Errorf("gateway.result_cache.io_timeout_ms must be less than one third of lease_ttl_ms")
	}
	if c.GenerationTimeoutMS < 1 || c.GenerationTimeoutMS > 3600000 {
		return fmt.Errorf("gateway.result_cache.generation_timeout_ms must be between 1 and 3600000")
	}
	if c.MaxConcurrentFills < 1 || c.MaxConcurrentFills > 1024 {
		return fmt.Errorf("gateway.result_cache.max_concurrent_fills must be between 1 and 1024")
	}
	return nil
}
