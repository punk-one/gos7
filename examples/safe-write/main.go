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

const writeConsent = "WRITE-TO-DEDICATED-ADDRESS"

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
	addressText := flag.String("address", "", "dedicated WORD address, for example DB1.DBW200")
	value := flag.Int64("value", 0, "signed 16-bit value")
	confirm := flag.String("confirm", "", "required write consent")
	timeout := flag.Duration("timeout", 5*time.Second, "connect/write timeout")
	flag.Parse()

	if *endpoint == "" || *addressText == "" {
		return fmt.Errorf("-endpoint and -address are required")
	}
	if *confirm != writeConsent {
		return fmt.Errorf("refusing PLC write: set -confirm %s", writeConsent)
	}
	if *rack > 7 || *slot > 31 || *timeout <= 0 {
		return fmt.Errorf("invalid rack, slot, or timeout")
	}
	if *value < -32768 || *value > 32767 {
		return fmt.Errorf("-value must fit int16")
	}

	address, transport, err := gos7.ParseAddress(*addressText)
	if err != nil {
		return fmt.Errorf("parse address: %w", err)
	}
	if transport != gos7.TransportWord {
		return fmt.Errorf("-address must be a WORD address such as DB1.DBW200")
	}
	data := make([]byte, 2)
	if err := codec.PutInt16(data, int16(*value)); err != nil {
		return err
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

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), *timeout)
	results, batchErr := client.Write(writeCtx, []gos7.WriteItem{{
		Address: address, Transport: gos7.TransportWord, Count: 1, Data: data,
	}})
	cancelWrite()
	if len(results) != 1 {
		if batchErr != nil {
			return fmt.Errorf("write returned %d results: %w", len(results), batchErr)
		}
		return fmt.Errorf("write returned %d results, want 1", len(results))
	}

	result := results[0]
	switch result.Outcome {
	case gos7.WriteAcknowledged:
		if batchErr != nil {
			return fmt.Errorf("acknowledged write had a batch error: %w", batchErr)
		}
		fmt.Printf("PLC acknowledged %s = %d\n", *addressText, *value)
		return nil
	case gos7.WriteRejected:
		return fmt.Errorf("PLC rejected the write: %w", result.Err)
	case gos7.WriteNotAttempted:
		return fmt.Errorf("write was not attempted: %w", result.Err)
	case gos7.WriteUnknown:
		return fmt.Errorf("write may have reached the PLC; reconcile before any retry: %w", result.Err)
	default:
		return fmt.Errorf("unexpected write outcome %d", result.Outcome)
	}
}
