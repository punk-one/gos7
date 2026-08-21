package gos7_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/punk-one/gos7"
)

func TestParseAddressCorpus(t *testing.T) {
	tests := []struct {
		input     string
		address   gos7.Address
		transport gos7.TransportType
		canonical string
	}{
		{"DB1.DBX0.7", gos7.Address{Area: gos7.AreaDB, DBNumber: 1, Offset: 0, Bit: 7}, gos7.TransportBit, "DB1.DBX0.7"},
		{"DB2000.DBB18", gos7.Address{Area: gos7.AreaDB, DBNumber: 2000, Offset: 18}, gos7.TransportByte, "DB2000.DBB18"},
		{"DB2000.DBW18", gos7.Address{Area: gos7.AreaDB, DBNumber: 2000, Offset: 18}, gos7.TransportWord, "DB2000.DBW18"},
		{"DB2000.DBD18", gos7.Address{Area: gos7.AreaDB, DBNumber: 2000, Offset: 18}, gos7.TransportDWord, "DB2000.DBD18"},
		{"%EX12.3", gos7.Address{Area: gos7.AreaInput, Offset: 12, Bit: 3}, gos7.TransportBit, "IX12.3"},
		{"E0.0", gos7.Address{Area: gos7.AreaInput}, gos7.TransportBit, "IX0.0"},
		{"AX2.1", gos7.Address{Area: gos7.AreaOutput, Offset: 2, Bit: 1}, gos7.TransportBit, "QX2.1"},
		{"Q0.0", gos7.Address{Area: gos7.AreaOutput}, gos7.TransportBit, "QX0.0"},
		{"M0.0", gos7.Address{Area: gos7.AreaMarker}, gos7.TransportBit, "MX0.0"},
		{"MD4", gos7.Address{Area: gos7.AreaMarker, Offset: 4}, gos7.TransportDWord, "MD4"},
		{"TM3", gos7.Address{Area: gos7.AreaTimer, Offset: 3}, gos7.TransportTimer, "T3"},
		{"Z9", gos7.Address{Area: gos7.AreaCounter, Offset: 9}, gos7.TransportCounter, "C9"},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			address, transport, err := gos7.ParseAddress(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if address != test.address || transport != test.transport {
				t.Fatalf("got %+v/%v, want %+v/%v", address, transport, test.address, test.transport)
			}
			formatted, err := gos7.FormatAddress(address, transport)
			if err != nil {
				t.Fatal(err)
			}
			if formatted != test.canonical {
				t.Fatalf("got canonical %q, want %q", formatted, test.canonical)
			}
		})
	}
}

func TestAddressRejectsMalformedAndOutOfRange(t *testing.T) {
	for _, input := range []string{"", "DB0.DBB0", "DB1.DBX0.8", "DB1.DBB0.1", "I0", "DB1.foo", "DB1.DBB 0"} {
		if _, _, err := gos7.ParseAddress(input); !errors.Is(err, gos7.ErrInvalidArgument) && !errors.Is(err, gos7.ErrLimit) {
			t.Fatalf("%q: expected validation error, got %v", input, err)
		}
	}
}

func TestAddressAndValueTypeAreIndependent(t *testing.T) {
	address, _, err := gos7.ParseAddress("DB1.DBD4")
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := gos7.FormatAddress(address, gos7.TransportReal)
	if err != nil {
		t.Fatal(err)
	}
	if formatted != "DB1.DBD4" {
		t.Fatalf("REAL changed location: %s", formatted)
	}
	server, client := startClient(t, 240)
	want := []byte{0x40, 0x5e, 0xdd, 0x2f, 0x1a, 0x9f, 0xbe, 0x77}
	server.SetBytes(wireAreaDB, 1, 4, want)
	results, err := client.Read(context.Background(), []gos7.ReadItem{{
		Address: address, Transport: gos7.TransportByte, Count: 8,
	}})
	if err != nil || len(results) != 1 || results[0].Err != nil || !bytes.Equal(results[0].Data, want) {
		t.Fatalf("LREAL raw span failed: results=%v err=%v", results, err)
	}
}

func FuzzAddressParser(f *testing.F) {
	for _, seed := range []string{"DB1.DBX0.0", "EX1.2", "QW4", "MD8", "T1", "C2", "", "%DB1.DBD4"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		address, transport, err := gos7.ParseAddress(input)
		if err == nil {
			if _, err := gos7.FormatAddress(address, transport); err != nil {
				t.Fatalf("valid parse did not format: %v", err)
			}
		}
	})
}
