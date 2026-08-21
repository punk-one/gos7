package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/punk-one/gos7"
	"github.com/punk-one/gos7/codec"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	endpoint := flag.String("endpoint", "", "PLC host or host:port")
	rack := flag.Uint("rack", 0, "PLC rack (0..7)")
	slot := flag.Uint("slot", 2, "PLC slot (0..31)")
	realAddressText := flag.String("real", "DB1.DBD4", "absolute address of one REAL")
	bitAddressText := flag.String("bit", "DB1.DBX8.0", "absolute address of one BOOL")
	timeout := flag.Duration("timeout", 5*time.Second, "connect/read timeout")
	flag.Parse()

	if *endpoint == "" {
		return fmt.Errorf("-endpoint is required")
	}
	if *rack > 7 || *slot > 31 {
		return fmt.Errorf("rack must be 0..7 and slot must be 0..31")
	}
	if *timeout <= 0 {
		return fmt.Errorf("-timeout must be positive")
	}

	realAddress, _, err := gos7.ParseAddress(*realAddressText)
	if err != nil {
		return fmt.Errorf("parse REAL address: %w", err)
	}
	bitAddress, bitTransport, err := gos7.ParseAddress(*bitAddressText)
	if err != nil {
		return fmt.Errorf("parse BOOL address: %w", err)
	}
	if bitTransport != gos7.TransportBit {
		return fmt.Errorf("-bit must be an X/bit address")
	}

	dialCtx, cancelDial := context.WithTimeout(context.Background(), *timeout)
	client, err := gos7.Dial(dialCtx, gos7.Config{
		Endpoint: *endpoint,
		Addressing: gos7.Addressing{
			Mode: gos7.AddressByRackSlot,
			Rack: uint8(*rack),
			Slot: uint8(*slot),
			Role: gos7.ConnectionPG,
		},
	})
	cancelDial()
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer client.Close()

	readCtx, cancelRead := context.WithTimeout(context.Background(), *timeout)
	results, batchErr := client.Read(readCtx, []gos7.ReadItem{
		{Address: realAddress, Transport: gos7.TransportReal, Count: 1},
		{Address: bitAddress, Transport: gos7.TransportBit, Count: 1},
	})
	cancelRead()
	if len(results) != 2 {
		if batchErr != nil {
			return fmt.Errorf("read returned %d results: %w", len(results), batchErr)
		}
		return fmt.Errorf("read returned %d results, want 2", len(results))
	}

	if results[0].Err != nil {
		fmt.Printf("REAL error: %v\n", results[0].Err)
	} else {
		value, err := codec.Float32(results[0].Data)
		if err != nil {
			return fmt.Errorf("decode REAL: %w", err)
		}
		fmt.Printf("REAL %s = %v\n", *realAddressText, value)
	}
	if results[1].Err != nil {
		fmt.Printf("BOOL error: %v\n", results[1].Err)
	} else {
		value, err := codec.Bool(results[1].Data)
		if err != nil {
			return fmt.Errorf("decode BOOL: %w", err)
		}
		fmt.Printf("BOOL %s = %t\n", *bitAddressText, value)
	}
	if batchErr != nil {
		return fmt.Errorf("batch read: %w", batchErr)
	}

	diagnostic := client.Diagnostics()
	if diagnostic.LimitsValid {
		fmt.Printf("session generation=%d negotiated-pdu=%d\n",
			diagnostic.SessionGeneration, diagnostic.Limits.NegotiatedPDU)
	}
	return nil
}
