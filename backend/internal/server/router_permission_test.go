package server

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRBACToCheckerNilService(t *testing.T) {
	require.Nil(t, rbacToChecker(nil))
	require.True(t, rbacToChecker([]*service.RBACService{nil}) == nil, "typed nil must not become a callable checker")
}
