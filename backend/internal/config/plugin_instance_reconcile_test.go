package config

import (
	"github.com/stretchr/testify/require"
	"reflect"
	"testing"
)

func pluginReconcileSetting(t *testing.T, cfg *Config, name string) int {
	t.Helper()
	value := reflect.ValueOf(cfg.Plugins).FieldByName(name)
	require.True(t, value.IsValid(), "missing configurable instance reconciliation field: "+name)
	return int(value.Int())
}
func TestPluginInstanceReconcileConfigDefaults(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, 5, pluginReconcileSetting(t, cfg, "InstanceReconcileIntervalSeconds"))
	require.Equal(t, 30, pluginReconcileSetting(t, cfg, "InstanceReconcileTimeoutSeconds"))
}
func TestPluginInstanceReconcileConfigEnvironment(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("PLUGINS_INSTANCE_RECONCILE_INTERVAL_SECONDS", "7")
	t.Setenv("PLUGINS_INSTANCE_RECONCILE_TIMEOUT_SECONDS", "45")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, 7, pluginReconcileSetting(t, cfg, "InstanceReconcileIntervalSeconds"))
	require.Equal(t, 45, pluginReconcileSetting(t, cfg, "InstanceReconcileTimeoutSeconds"))
}
func TestPluginInstanceReconcileConfigRejectsInvalid(t *testing.T) {
	for _, tc := range []struct{ env, value, key string }{{"PLUGINS_INSTANCE_RECONCILE_INTERVAL_SECONDS", "0", "interval"}, {"PLUGINS_INSTANCE_RECONCILE_INTERVAL_SECONDS", "3601", "interval"}, {"PLUGINS_INSTANCE_RECONCILE_TIMEOUT_SECONDS", "-1", "timeout"}, {"PLUGINS_INSTANCE_RECONCILE_TIMEOUT_SECONDS", "301", "timeout"}} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			t.Setenv(tc.env, tc.value)
			_, err := Load()
			require.ErrorContains(t, err, "plugins.instance_reconcile_"+tc.key+"_seconds")
		})
	}
}
func TestPluginInstanceReconcileConfigAcceptsBounds(t *testing.T) {
	for _, values := range [][2]string{{"1", "1"}, {"3600", "300"}} {
		t.Run(values[0]+"_"+values[1], func(t *testing.T) {
			resetViperWithJWTSecret(t)
			t.Setenv("PLUGINS_INSTANCE_RECONCILE_INTERVAL_SECONDS", values[0])
			t.Setenv("PLUGINS_INSTANCE_RECONCILE_TIMEOUT_SECONDS", values[1])
			_, err := Load()
			require.NoError(t, err)
		})
	}
}
