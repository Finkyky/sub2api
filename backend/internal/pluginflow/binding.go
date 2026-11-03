// Package pluginflow executes group-scoped checks without network or HTTP I/O.
package pluginflow

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
)

var ErrInvalidContext = errors.New("invalid plugin execution context")
var ErrBindingUnavailable = errors.New("plugin binding unavailable")

// Binding is a host-owned snapshot of one enabled or disabled check.
// Version and ConfigRevision must resolve to a pinned runtime in the adapter.
type Binding struct {
	ID             int64
	InstanceID     int64
	GroupID        int64
	Stage          contract.Stage
	Order          int
	Enabled        bool
	Ready          bool
	Version        string
	ConfigRevision int64
	CanReplace     bool
}

// SelectBindings copies and orders matching bindings by Order then ID.
// An unavailable matching binding fails closed; unrelated bindings are ignored.
func SelectBindings(all []Binding, groupID int64, stage contract.Stage) ([]Binding, error) {
	if groupID <= 0 || !stage.Valid() {
		return nil, ErrInvalidContext
	}
	selected := make([]Binding, 0)
	seen := make(map[int64]struct{})
	for _, b := range all {
		if !b.Enabled || b.GroupID != groupID || b.Stage != stage {
			continue
		}
		if b.ID <= 0 || b.InstanceID <= 0 || !b.Ready || strings.TrimSpace(b.Version) == "" || b.ConfigRevision <= 0 {
			return nil, fmt.Errorf("%w: binding=%d", ErrBindingUnavailable, b.ID)
		}
		if _, exists := seen[b.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate binding=%d", ErrBindingUnavailable, b.ID)
		}
		seen[b.ID] = struct{}{}
		selected = append(selected, b)
	}
	sort.Slice(selected, func(i, j int) bool {
		if selected[i].Order == selected[j].Order {
			return selected[i].ID < selected[j].ID
		}
		return selected[i].Order < selected[j].Order
	})
	return selected, nil
}
