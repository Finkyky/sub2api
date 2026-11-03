package domain

import "testing"

func TestResultCacheConfigDefaults(t *testing.T) {
	c, err := NormalizeGroupResponseResultCacheConfig(GroupResponseResultCacheConfig{})
	if err != nil || c.Enabled || c.AllowGroupSharing || c.TTLSeconds != 30 || c.MaxResponseBytes != 2097152 || c.Revision != 1 {
		t.Fatalf("defaults: %+v %v", c, err)
	}
}
func TestResultCacheConfigBounds(t *testing.T) {
	for _, c := range []GroupResponseResultCacheConfig{{TTLSeconds: -1}, {TTLSeconds: 86401}, {MaxResponseBytes: 1023}, {MaxResponseBytes: 2097153}, {Revision: -1}} {
		if _, err := NormalizeGroupResponseResultCacheConfig(c); err == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
}
