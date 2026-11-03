package pluginflow

import (
	"errors"
	"reflect"
	"testing"

	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
)

func fixtureBinding(id, group int64, stage contract.Stage, order int) Binding {
	return Binding{ID: id, InstanceID: id + 100, GroupID: group, Stage: stage,
		Order: order, Enabled: true, Ready: true, Version: "1.0.0", ConfigRevision: 1}
}

func TestSelectBindingsScopeAndOrder(t *testing.T) {
	disabled := fixtureBinding(4, 7, contract.Input, 0)
	disabled.Enabled = false
	otherGroup := fixtureBinding(5, 8, contract.Input, 0)
	otherGroup.Ready = false
	bindings := []Binding{
		fixtureBinding(9, 7, contract.Input, 20),
		fixtureBinding(3, 7, contract.Input, 10),
		fixtureBinding(2, 7, contract.Input, 10),
		fixtureBinding(6, 7, contract.Output, 0), disabled, otherGroup,
	}
	original := append([]Binding(nil), bindings...)
	got, err := SelectBindings(bindings, 7, contract.Input)
	if err != nil {
		t.Fatal(err)
	}
	ids := []int64{}
	for _, binding := range got {
		ids = append(ids, binding.ID)
	}
	if !reflect.DeepEqual(ids, []int64{2, 3, 9}) {
		t.Fatalf("ids=%v", ids)
	}
	if !reflect.DeepEqual(bindings, original) {
		t.Fatal("caller bindings were reordered")
	}
	got[0].Version = "changed"
	if !reflect.DeepEqual(bindings, original) {
		t.Fatal("snapshot aliases caller bindings")
	}
}

func TestSelectBindingsRejectsInvalidSnapshots(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Binding)
	}{
		{"id", func(b *Binding) { b.ID = 0 }},
		{"instance", func(b *Binding) { b.InstanceID = 0 }},
		{"version", func(b *Binding) { b.Version = "" }},
		{"revision", func(b *Binding) { b.ConfigRevision = 0 }},
		{"unavailable", func(b *Binding) { b.Ready = false }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := fixtureBinding(1, 7, contract.Input, 0)
			tt.edit(&b)
			got, err := SelectBindings([]Binding{b}, 7, contract.Input)
			if !errors.Is(err, ErrBindingUnavailable) || got != nil {
				t.Fatalf("got=%v err=%v", got, err)
			}
		})
	}
	b := fixtureBinding(1, 7, contract.Input, 0)
	if _, err := SelectBindings([]Binding{b, b}, 7, contract.Input); err == nil {
		t.Fatal("duplicate binding accepted")
	}
	if _, err := SelectBindings(nil, 0, contract.Input); !errors.Is(err, ErrInvalidContext) {
		t.Fatal("missing actual group accepted")
	}
	if _, err := SelectBindings(nil, 7, "unknown"); !errors.Is(err, ErrInvalidContext) {
		t.Fatal("unknown stage accepted")
	}
}
