package libbox

import (
	"github.com/sagernet/sing/common/control"
	"net"
	"net/netip"
	"testing"
)

func TestSameDefaultInterface(t *testing.T) {
	original := control.Interface{Name: "test0", Index: 42, MTU: 1500, Flags: net.FlagUp,
		HardwareAddr: net.HardwareAddr{2, 0, 0, 0, 0, 1},
		Addresses:    []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24"), netip.MustParsePrefix("2001:db8::1/64")}}
	for _, tc := range []struct {
		name   string
		mutate func(*control.Interface)
		want   bool
	}{
		{"unchanged", func(*control.Interface) {}, true},
		{"address order", func(i *control.Interface) { i.Addresses = []netip.Prefix{original.Addresses[1], original.Addresses[0]} }, true},
		{"name", func(i *control.Interface) { i.Name = "test1" }, false},
		{"index", func(i *control.Interface) { i.Index++ }, false},
		{"MTU", func(i *control.Interface) { i.MTU-- }, false},
		{"flags", func(i *control.Interface) { i.Flags = 0 }, false},
		{"hardware", func(i *control.Interface) { i.HardwareAddr = nil }, false},
		{"removed address", func(i *control.Interface) { i.Addresses = nil }, false},
		{"changed address", func(i *control.Interface) {
			i.Addresses = []netip.Prefix{original.Addresses[0], netip.MustParsePrefix("2001:db8::2/64")}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := original
			tc.mutate(&next)
			if got := sameDefaultInterface(&original, &next); got != tc.want {
				t.Fatalf("same interface = %v, want %v", got, tc.want)
			}
		})
	}
	if sameDefaultInterface(nil, &original) {
		t.Fatal("initial interface must be an update")
	}
}
