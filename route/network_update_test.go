package route

import (
	"context"
	"sync"
	"testing"
)

func TestPendingNetworkUpdatePreservesInterfaceReset(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		updates []networkUpdateKind
		want    networkUpdateKind
	}{
		{"DNS before start", []networkUpdateKind{networkUpdateDNS}, networkUpdateDNS},
		{"interface then DNS", []networkUpdateKind{networkUpdateInterface, networkUpdateDNS}, networkUpdateInterface},
		{"DNS then interface", []networkUpdateKind{networkUpdateDNS, networkUpdateInterface}, networkUpdateInterface},
		{"repeated DNS", []networkUpdateKind{networkUpdateDNS, networkUpdateDNS}, networkUpdateDNS},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			manager := new(NetworkManager)
			for _, update := range testCase.updates {
				if update == networkUpdateDNS {
					manager.UpdateDNS()
				} else {
					manager.notifyInterfaceUpdate(nil, 0)
				}
			}
			if manager.networkUpdatePending != testCase.want {
				t.Fatalf("pending update = %d, want %d", manager.networkUpdatePending, testCase.want)
			}
		})
	}
}

func TestCanceledNetworkUpdateRemainsPending(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	manager := &NetworkManager{networkUpdatePending: networkUpdateInterface}
	manager.updateInterface(ctx, nil)
	if manager.networkUpdatePending != networkUpdateInterface {
		t.Fatal("canceled update consumed the pending interface reset")
	}
}

func TestConcurrentDNSCannotDowngradeInterfaceUpdate(t *testing.T) {
	manager := new(NetworkManager)
	var done sync.WaitGroup
	for range 10 {
		done.Add(1)
		go func() { defer done.Done(); manager.UpdateDNS() }()
	}
	done.Add(1)
	go func() { defer done.Done(); manager.notifyInterfaceUpdate(nil, 0) }()
	done.Wait()
	if manager.networkUpdatePending != networkUpdateInterface {
		t.Fatal("DNS update downgraded interface reset")
	}
}
