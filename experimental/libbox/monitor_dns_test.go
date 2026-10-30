//go:build darwin && cgo

package libbox

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/control"
	"github.com/sagernet/sing/service"
)

// This fixture runs the real libbox monitor and NetworkManager without a TUN,
// GUI, remote server, or changes to the host's DNS or routing configuration.
func TestDNSOnlyChangePreservesTrackedConnection(t *testing.T) {
	loopback, err := net.InterfaceByName("lo0")
	if err != nil {
		t.Fatal(err)
	}
	platform := &dnsMonitorTestPlatform{loopback: loopback}
	wrapper := &platformInterfaceWrapper{iif: platform}
	ctx := service.ContextWithDefaultRegistry(baseContext(nil))
	ctx = service.ContextWith[adapter.PlatformInterface](ctx, wrapper)
	probe := &dnsResetProbe{resets: make(chan struct{}, 8)}
	registry := service.FromContext[adapter.DNSTransportRegistry](ctx).(*dns.TransportRegistry)
	dns.RegisterTransport[option.LocalDNSServerOptions](registry, "local", func(context.Context, log.ContextLogger, string, option.LocalDNSServerOptions) (adapter.DNSTransport, error) {
		return probe, nil
	})
	options, err := parseConfig(ctx, `{"dns":{"servers":[{"type":"local","tag":"probe"}]}}`)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := box.New(box.Options{Context: ctx, Options: options})
	if err != nil {
		t.Fatal(err)
	}
	if err = instance.Start(); err != nil {
		instance.Close()
		t.Fatal(err)
	}
	defer instance.Close()
	manager := service.FromContext[adapter.ConnectionManager](ctx)
	monitor := instance.Network().InterfaceMonitor().(*platformDefaultInterfaceMonitor)

	// Observe completion of an actual initial interface reset before testing DNS.
	first, firstPeer := net.Pipe()
	first = manager.TrackConn(first)
	defer first.Close()
	defer firstPeer.Close()
	monitor.UpdateDefaultInterface("lo0", int32(loopback.Index), false, false)
	firstPeer.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err = firstPeer.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("initial interface update did not close tracked connection: %v", err)
	}

	waitDNSReset(t, probe)

	fullUpdates := make(chan struct{}, 8)
	monitor.RegisterCallback(func(*control.Interface, int) { fullUpdates <- struct{}{} })
	for _, testCase := range []struct {
		name             string
		platformCallback bool
		addressChange    bool
		dnsChange        bool
	}{
		{name: "DNS watcher", dnsChange: true},
		{name: "platform callback", platformCallback: true, dnsChange: true},
		{name: "address change with DNS change", platformCallback: true, addressChange: true, dnsChange: true},
		{name: "address change without DNS change", platformCallback: true, addressChange: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			conn, peer := net.Pipe()
			conn = manager.TrackConn(conn)
			defer conn.Close()
			defer peer.Close()
			// Simulate a stale DNS snapshot without changing system configuration.
			monitor.defaultInterfaceAccess.Lock()
			if testCase.dnsChange {
				monitor.defaultDNSServers = []string{"previous-configuration"}
			}
			if testCase.addressChange {
				previous := *monitor.defaultInterface
				previous.Addresses = nil
				monitor.defaultInterface = &previous
			}
			monitor.defaultInterfaceAccess.Unlock()
			if testCase.platformCallback {
				monitor.UpdateDefaultInterface("lo0", int32(loopback.Index), false, false)
			} else {
				monitor.updateDNSServers()
			}
			// Reset is the last operation in the real NetworkManager update path.
			// Waiting here prevents a successful read from racing ahead of CloseAll.
			if testCase.dnsChange {
				waitDNSReset(t, probe)
			}
			select {
			case <-fullUpdates:
				if !(testCase.addressChange && testCase.dnsChange) {
					t.Fatal("unexpected full interface update")
				}
			default:
				if testCase.addressChange && testCase.dnsChange {
					t.Fatal("missing full interface update")
				}
			}
			peer.SetReadDeadline(time.Now().Add(5 * time.Second))
			if testCase.addressChange && testCase.dnsChange {
				if _, err := peer.Read(make([]byte, 1)); err != io.EOF {
					t.Fatalf("real interface change must still close the connection: %v", err)
				}
				return
			}
			writeDone := make(chan error, 1)
			go func() { _, writeErr := conn.Write([]byte{1}); writeDone <- writeErr }()
			data := make([]byte, 1)
			if _, err := io.ReadFull(peer, data); err != nil {
				t.Fatalf("DNS-only update closed an established connection: %v", err)
			}
			if err := <-writeDone; err != nil {
				t.Fatal(err)
			}
			if data[0] != 1 {
				t.Fatal("unexpected payload")
			}
		})
	}

}

type dnsMonitorTestPlatform struct {
	PlatformInterface
	loopback *net.Interface
}

func (p *dnsMonitorTestPlatform) StartDefaultInterfaceMonitor(InterfaceUpdateListener) error {
	return nil
}
func (p *dnsMonitorTestPlatform) CloseDefaultInterfaceMonitor(InterfaceUpdateListener) error {
	return nil
}
func (p *dnsMonitorTestPlatform) UsePlatformAutoDetectInterfaceControl() bool       { return false }
func (p *dnsMonitorTestPlatform) UnderNetworkExtension() bool                       { return false }
func (p *dnsMonitorTestPlatform) IncludeAllNetworks() bool                          { return false }
func (p *dnsMonitorTestPlatform) UsePlatformShell() bool                            { return false }
func (p *dnsMonitorTestPlatform) UsePlatformBridge() bool                           { return false }
func (p *dnsMonitorTestPlatform) UsePlatformAutoRedirect() bool                     { return false }
func (p *dnsMonitorTestPlatform) ReadWIFIState() *WIFIState                         { return nil }
func (p *dnsMonitorTestPlatform) ClearDNSCache()                                    {}
func (p *dnsMonitorTestPlatform) StartNeighborMonitor(NeighborUpdateListener) error { return nil }
func (p *dnsMonitorTestPlatform) CloseNeighborMonitor(NeighborUpdateListener) error { return nil }
func (p *dnsMonitorTestPlatform) GetInterfaces() (NetworkInterfaceIterator, error) {
	return newIterator([]*NetworkInterface{{
		Name: "lo0", Index: int32(p.loopback.Index), MTU: int32(p.loopback.MTU),
		Flags:     int32(net.FlagUp | net.FlagLoopback),
		Type:      InterfaceTypeOther,
		Addresses: newIterator([]string{"127.0.0.1/8"}),
	}}), nil
}

func waitDNSReset(t *testing.T, probe *dnsResetProbe) {
	t.Helper()
	select {
	case <-probe.resets:
	case <-time.After(5 * time.Second):
		t.Fatal("DNS transport was not refreshed")
	}
}

type dnsResetProbe struct {
	adapter.DNSTransport
	resets chan struct{}
}

func (p *dnsResetProbe) Type() string                                   { return "local" }
func (p *dnsResetProbe) Tag() string                                    { return "probe" }
func (p *dnsResetProbe) Dependencies() []string                         { return nil }
func (p *dnsResetProbe) Start(adapter.StartStage, *adapter.Scope) error { return nil }
func (p *dnsResetProbe) Reset()                                         { p.resets <- struct{}{} }
