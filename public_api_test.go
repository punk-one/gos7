package gos7_test

import (
	"context"
	"errors"
	"testing"

	"github.com/punk-one/gos7"
)

// Keep a root-level black-box test so ordinary package tooling exercises the
// exported constructor and lifecycle without relying only on ./test.
func TestPublicConstructorLifecycle(t *testing.T) {
	client, err := gos7.New(gos7.Config{
		Endpoint: "192.0.2.1",
		Addressing: gos7.Addressing{
			Mode: gos7.AddressByRackSlot, Rack: 0, Slot: 2, Role: gos7.ConnectionPG,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.State() != gos7.StateNew {
		t.Fatalf("state=%v, want new", client.State())
	}
	if _, err := client.Read(context.Background(), []gos7.ReadItem{{
		Address: gos7.Address{Area: gos7.AreaDB, DBNumber: 1}, Transport: gos7.TransportByte, Count: 1,
	}}); !errors.Is(err, gos7.ErrNotConnected) {
		t.Fatalf("unconnected read returned %v", err)
	}
	if err := client.Close(); err != nil || client.State() != gos7.StateClosed {
		t.Fatalf("close: state=%v err=%v", client.State(), err)
	}
}
