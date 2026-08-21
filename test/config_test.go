package gos7_test

import (
	"context"
	"errors"
	"testing"

	"github.com/punk-one/gos7"
)

func validConfig() gos7.Config {
	return gos7.Config{
		Endpoint: "192.0.2.1",
		Addressing: gos7.Addressing{
			Mode: gos7.AddressByRackSlot,
			Rack: 0,
			Slot: 2,
			Role: gos7.ConnectionPG,
		},
	}
}

func TestPublicConfigAcceptsDocumentedDefaults(t *testing.T) {
	if gos7.DefaultPort != 102 || gos7.DefaultRequestedPDU != 480 ||
		gos7.DefaultMaxFrameBytes != 4096 || gos7.DefaultMaxItemsPerCall != 4096 ||
		gos7.DefaultMaxFragmentsPerCall != 65536 || gos7.DefaultExchangeTimeout <= 0 {
		t.Fatal("public configuration defaults changed")
	}
	client, err := gos7.New(validConfig())
	if err != nil {
		t.Fatalf("default configuration: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("close unconnected client: %v", err)
	}
}

func TestPublicConfigAcceptsIPv4IPv6AndDNSNames(t *testing.T) {
	for _, endpoint := range []string{
		"192.0.2.1",
		"192.0.2.1:1102",
		"2001:db8::1",
		"[2001:db8::1]",
		"fe80::1%ethernet",
		"[2001:db8::1]:1102",
		"plc.example",
	} {
		t.Run(endpoint, func(t *testing.T) {
			config := validConfig()
			config.Endpoint = endpoint
			client, err := gos7.New(config)
			if err != nil {
				t.Fatalf("New(%q): %v", endpoint, err)
			}
			_ = client.Close()
		})
	}
}

func TestPublicConfigRejectsMalformedEndpoints(t *testing.T) {
	for _, endpoint := range []string{
		"", ":102", "[2001:db8::1]:0", "[2001:db8::1", "bad/host", "bad/host:102", "bad\\host:102", "bad\x00host:102",
	} {
		t.Run(endpoint, func(t *testing.T) {
			config := validConfig()
			config.Endpoint = endpoint
			if _, err := gos7.New(config); !errors.Is(err, gos7.ErrInvalidArgument) {
				t.Fatalf("New(%q): expected invalid argument, got %v", endpoint, err)
			}
		})
	}
}

func TestAddressingModesAreExclusive(t *testing.T) {
	config := validConfig()
	config.Addressing.LocalTSAP = 0x0100
	if _, err := gos7.New(config); !errors.Is(err, gos7.ErrInvalidArgument) {
		t.Fatalf("expected invalid argument, got %v", err)
	}

	config = validConfig()
	config.Addressing = gos7.Addressing{
		Mode: gos7.AddressByTSAP, LocalTSAP: 0x0100, RemoteTSAP: 0x0200,
	}
	client, err := gos7.New(config)
	if err != nil {
		t.Fatal(err)
	}
	_ = client.Close()

	config.Addressing.Role = gos7.ConnectionPG
	if _, err := gos7.New(config); !errors.Is(err, gos7.ErrInvalidArgument) {
		t.Fatalf("expected exclusive-mode error, got %v", err)
	}
}

func TestResourceLimitsRejectInvalidValues(t *testing.T) {
	config := validConfig()
	config.MaxFrameBytes = 100
	config.RequestedPDU = 96
	if _, err := gos7.New(config); !errors.Is(err, gos7.ErrInvalidArgument) {
		t.Fatalf("expected PDU/frame error, got %v", err)
	}

	config = validConfig()
	config.MaxFragmentsPerCall = -1
	if _, err := gos7.New(config); !errors.Is(err, gos7.ErrInvalidArgument) {
		t.Fatalf("expected fragment-limit error, got %v", err)
	}

	config = validConfig()
	config.MaxItemBytes = 1024
	config.MaxBatchBytes = 512
	if _, err := gos7.New(config); !errors.Is(err, gos7.ErrInvalidArgument) {
		t.Fatalf("expected batch-limit error, got %v", err)
	}
}

func TestZeroValueClientFailsSafely(t *testing.T) {
	client := &gos7.Client{}
	if _, err := client.Read(context.Background(), []gos7.ReadItem{{
		Address: gos7.Address{Area: gos7.AreaDB, DBNumber: 1}, Transport: gos7.TransportByte, Count: 1,
	}}); !errors.Is(err, gos7.ErrLimit) && !errors.Is(err, gos7.ErrInvalidArgument) {
		t.Fatalf("zero-value Read returned %v", err)
	}
	if err := client.Connect(context.Background()); !errors.Is(err, gos7.ErrInvalidArgument) {
		t.Fatalf("zero-value Connect returned %v", err)
	}
	if err := client.Close(); err != nil || client.State() != gos7.StateClosed {
		t.Fatalf("zero-value Close: state=%v err=%v", client.State(), err)
	}
}
