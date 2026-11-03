package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCodexClientProfileConfiguration(t *testing.T) {
	for _, profile := range []string{"", "codex-linux-rustls-0.153.4", "chrome", "codex-linux-latest"} {
		t.Run(profile, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			t.Setenv("GATEWAY_OPENAI_CODEX_CLIENT_PROFILE", profile)
			cfg, err := Load()
			if profile != "" && profile != "codex-linux-rustls-0.153.4" {
				require.ErrorContains(t, err, "openai_codex_client_profile")
				return
			}
			require.NoError(t, err)
			require.Equal(t, profile, cfg.Gateway.OpenAICodexClientProfile)
		})
	}
}

func TestCodexClientProfileRejectsDisabledIdentityEnforcement(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("GATEWAY_OPENAI_CODEX_CLIENT_PROFILE", "codex-linux-rustls-0.153.4")
	t.Setenv("GATEWAY_DISABLE_CODEX_IDENTITY_ENFORCEMENT", "true")
	_, err := Load()
	require.ErrorContains(t, err, "identity")
}
