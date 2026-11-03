package config

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestPluginGatewayConfigDefaults(t *testing.T) {
	resetViperWithJWTSecret(t)
	c, e := Load()
	require.NoError(t, e)
	require.Equal(t, 10, c.Plugins.GatewayHookTimeoutSeconds)
	require.Equal(t, 120, c.Plugins.StrictOutputTimeoutSeconds)
	require.EqualValues(t, 8<<20, c.Plugins.StrictOutputMaxBytes)
	require.EqualValues(t, 128<<20, c.Plugins.StrictOutputTotalBytes)
	require.Equal(t, 16, c.Plugins.StrictOutputMaxConcurrency)
}
func TestPluginGatewayConfigBounds(t *testing.T) {
	for _, tc := range []struct{ key, value string }{{"GATEWAY_HOOK_TIMEOUT_SECONDS", "121"}, {"STRICT_OUTPUT_TIMEOUT_SECONDS", "1801"}, {"STRICT_OUTPUT_MAX_BYTES", "67108865"}, {"STRICT_OUTPUT_TOTAL_BYTES", "1073741825"}, {"STRICT_OUTPUT_MAX_CONCURRENCY", "1025"}, {"STRICT_OUTPUT_MAX_BYTES", "-1"}, {"STRICT_OUTPUT_TOTAL_BYTES", "-1"}} {
		t.Run(tc.key, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			t.Setenv("PLUGINS_"+tc.key, tc.value)
			_, e := Load()
			require.Error(t, e)
		})
	}
}
func TestPluginGatewayConfigEffectiveDefaultBudget(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("PLUGINS_STRICT_OUTPUT_MAX_BYTES", "0")
	t.Setenv("PLUGINS_STRICT_OUTPUT_TOTAL_BYTES", "1024")
	_, e := Load()
	require.Error(t, e, "effective default per-response budget exceeds explicit total")
}
