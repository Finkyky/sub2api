package service

import (
	"github.com/Wei-Shaw/sub2api/internal/domain"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrResultCachePolicyConflict = infraerrors.Conflict("RESULT_CACHE_POLICY_CONFLICT", "group changed concurrently; reload and retry")

func NormalizeGroupResponseResultCacheConfig(c GroupResponseResultCacheConfig) (GroupResponseResultCacheConfig, error) {
	return domain.NormalizeGroupResponseResultCacheConfig(c)
}
