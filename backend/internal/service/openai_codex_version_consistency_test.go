//go:build unit

package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

// officialCodexCLIStableVersion is pinned to the release whose request shape
// this gateway targets. Bump it together with codexCLIVersion after re-checking
// the official default-client and Responses request sources.
const officialCodexCLIStableVersion = "0.166.0"

func TestCodexVersionConstants_Consistency(t *testing.T) {
	require.Equal(t, officialCodexCLIStableVersion, codexCLIVersion,
		"default outbound Codex version must match the pinned official stable release")
	require.Equal(t, "codex_cli_rs", openai.CodexDefaultOriginator,
		"official default originator changed; update UA and version-header expectations")

	wantUA := "codex_cli_rs/0.166.0 (Ubuntu 22.4.0; x86_64) xterm-256color"
	require.Equal(t, wantUA, codexCLIUserAgent,
		"default UA must follow default_client.rs {originator}/{version} (...; arch) {terminal}")
	require.Equal(t, codexCLIUserAgent, DefaultOpenAICodexUserAgent)
	require.Equal(t, wantUA, buildCodexCLIUserAgent(codexCLIVersion))
}
