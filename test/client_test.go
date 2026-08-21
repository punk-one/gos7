package gos7_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/punk-one/gos7"
	"github.com/punk-one/gos7/internal/testplc"
)

const wireAreaDB = 0x84

func startClient(t *testing.T, pdu uint16) (*testplc.Server, *gos7.Client) {
	t.Helper()
	server, err := testplc.Start(pdu)
	if err != nil {
		t.Fatal(err)
	}
	client, err := gos7.Dial(context.Background(), gos7.Config{
		Endpoint: server.Endpoint(),
		Addressing: gos7.Addressing{
			Mode: gos7.AddressByRackSlot,
			Rack: 0,
			Slot: 2,
			Role: gos7.ConnectionPG,
		},
	})
	if err != nil {
		_ = server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	return server, client
}

func TestConnectLimitsDiagnosticsAndClose(t *testing.T) {
	server, client := startClient(t, 240)
	limits, ok := client.Limits()
	if !ok || limits.RequestedPDU != 480 || limits.NegotiatedPDU != 240 ||
		limits.NegotiatedTPDU != 1024 ||
		limits.NegotiatedMaxAmQCaller != 1 || limits.NegotiatedMaxAmQCallee != 1 {
		t.Fatalf("unexpected limits: %+v valid=%v", limits, ok)
	}
	diagnostics := client.Diagnostics()
	if diagnostics.State != gos7.StateReady || diagnostics.SessionGeneration != 1 ||
		!diagnostics.LimitsValid || diagnostics.LocalAddress == "" || diagnostics.RemoteAddress == "" {
		t.Fatalf("unexpected diagnostics: %+v", diagnostics)
	}
	if len(server.Requests()) != 0 {
		t.Fatal("Connect performed an implicit controller probe")
	}
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("idempotent Connect: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("idempotent Close: %v", err)
	}
	if client.State() != gos7.StateClosed {
		t.Fatalf("state=%v", client.State())
	}
	if _, ok := client.Limits(); ok {
		t.Fatal("closed session still exposes active limits")
	}
}

func TestNegotiationMatrixExplicitTSAPAndLocalBind(t *testing.T) {
	for _, pdu := range []uint16{240, 480, 960} {
		t.Run(fmt.Sprintf("pdu_%d", pdu), func(t *testing.T) {
			server, err := testplc.Start(pdu)
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			client, err := gos7.Dial(context.Background(), gos7.Config{
				Endpoint: server.Endpoint(),
				Addressing: gos7.Addressing{
					Mode:       gos7.AddressByTSAP,
					LocalTSAP:  0x0100,
					RemoteTSAP: 0x0200,
				},
				LocalAddress:  "127.0.0.1",
				RequestedPDU:  pdu,
				MaxFrameBytes: int(pdu) + 7,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			limits, ok := client.Limits()
			if !ok || limits.NegotiatedPDU != pdu {
				t.Fatalf("limits=%+v valid=%v", limits, ok)
			}
			if !strings.HasPrefix(client.Diagnostics().LocalAddress, "127.0.0.1:") {
				t.Fatalf("local bind not used: %+v", client.Diagnostics())
			}
		})
	}
}

func TestReadWriteAndNativeBit(t *testing.T) {
	server, client := startClient(t, 480)
	server.SetBytes(wireAreaDB, 1, 0, []byte{0b10100101, 2, 3, 4, 5, 6, 7, 8})

	results, err := client.Read(context.Background(), []gos7.ReadItem{
		{Address: gos7.Address{Area: gos7.AreaDB, DBNumber: 1, Offset: 1}, Transport: gos7.TransportByte, Count: 3},
		{Address: gos7.Address{Area: gos7.AreaDB, DBNumber: 1, Offset: 4}, Transport: gos7.TransportWord, Count: 2},
		{Address: gos7.Address{Area: gos7.AreaDB, DBNumber: 1, Offset: 0, Bit: 7}, Transport: gos7.TransportBit, Count: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(results[0].Data, []byte{2, 3, 4}) || !bytes.Equal(results[1].Data, []byte{5, 6, 7, 8}) ||
		!bytes.Equal(results[2].Data, []byte{1}) {
		t.Fatalf("unexpected read results: %+v", results)
	}

	writes, err := client.Write(context.Background(), []gos7.WriteItem{
		{Address: gos7.Address{Area: gos7.AreaDB, DBNumber: 1, Offset: 2}, Transport: gos7.TransportWord, Count: 1, Data: []byte{9, 10}},
		{Address: gos7.Address{Area: gos7.AreaDB, DBNumber: 1, Offset: 0, Bit: 1}, Transport: gos7.TransportBit, Count: 1, Data: []byte{1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for index, result := range writes {
		if result.Outcome != gos7.WriteAcknowledged || result.Err != nil {
			t.Fatalf("write %d: %+v", index, result)
		}
	}
	if actual := server.Bytes(wireAreaDB, 1, 0, 4); !bytes.Equal(actual, []byte{0b10100111, 2, 9, 10}) {
		t.Fatalf("native bit write changed neighbors: %08b", actual)
	}
}

func TestPerItemPLCFailuresDoNotHideOtherResults(t *testing.T) {
	server, client := startClient(t, 480)
	server.SetBytes(wireAreaDB, 1, 0, []byte{1, 2, 3, 4})
	server.RejectNextItem(0x05)
	reads, err := client.Read(context.Background(), []gos7.ReadItem{
		{Address: gos7.Address{Area: gos7.AreaDB, DBNumber: 1}, Transport: gos7.TransportByte, Count: 1},
		{Address: gos7.Address{Area: gos7.AreaDB, DBNumber: 1, Offset: 1}, Transport: gos7.TransportByte, Count: 2},
	})
	if err != nil {
		t.Fatalf("per-item failure became fatal: %v", err)
	}
	if !errors.Is(reads[0].Err, gos7.ErrPLC) || !bytes.Equal(reads[1].Data, []byte{2, 3}) {
		t.Fatalf("unexpected mixed read results: %+v", reads)
	}

	server.RejectNextItem(0x0a)
	writes, err := client.Write(context.Background(), []gos7.WriteItem{
		{Address: gos7.Address{Area: gos7.AreaDB, DBNumber: 1}, Transport: gos7.TransportByte, Count: 1, Data: []byte{8}},
		{Address: gos7.Address{Area: gos7.AreaDB, DBNumber: 1, Offset: 1}, Transport: gos7.TransportByte, Count: 1, Data: []byte{9}},
	})
	if err != nil {
		t.Fatalf("per-item write failure became fatal: %v", err)
	}
	if writes[0].Outcome != gos7.WriteRejected || !errors.Is(writes[0].Err, gos7.ErrPLC) ||
		writes[1].Outcome != gos7.WriteAcknowledged {
		t.Fatalf("unexpected mixed write results: %+v", writes)
	}
}

func TestFailedLargeReadItemStopsItsRemainingFragments(t *testing.T) {
	server, client := startClient(t, 240)
	server.RejectNextItem(0x05)
	results, err := client.Read(context.Background(), []gos7.ReadItem{{
		Address: gos7.Address{Area: gos7.AreaDB, DBNumber: 1}, Transport: gos7.TransportByte, Count: 600,
	}})
	if err != nil {
		t.Fatalf("per-item failure became fatal: %v", err)
	}
	if len(results) != 1 || !errors.Is(results[0].Err, gos7.ErrPLC) || len(results[0].Data) != 0 {
		t.Fatalf("unexpected failed large read: %+v", results)
	}
	if requests := server.Requests(); len(requests) != 1 {
		t.Fatalf("failed item continued with %d requests: %+v", len(requests), requests)
	}
}

func TestReadFragmentLimitPreventsIO(t *testing.T) {
	server, err := testplc.Start(240)
	if err != nil {
		t.Fatal(err)
	}
	client, err := gos7.Dial(context.Background(), gos7.Config{
		Endpoint: server.Endpoint(),
		Addressing: gos7.Addressing{
			Mode: gos7.AddressByRackSlot, Rack: 0, Slot: 2, Role: gos7.ConnectionPG,
		},
		MaxFragmentsPerCall: 2,
	})
	if err != nil {
		_ = server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	results, err := client.Read(context.Background(), []gos7.ReadItem{{
		Address: gos7.Address{Area: gos7.AreaDB, DBNumber: 1}, Transport: gos7.TransportByte, Count: 500,
	}})
	if !errors.Is(err, gos7.ErrLimit) || len(results) != 1 || !errors.Is(results[0].Err, gos7.ErrLimit) {
		t.Fatalf("fragment limit: results=%+v err=%v", results, err)
	}
	if requests := server.Requests(); len(requests) != 0 {
		t.Fatalf("fragment-limit failure reached PLC: %+v", requests)
	}
}

func TestNegotiatedPDUSplitsReadAndExplicitWriteArea(t *testing.T) {
	server, client := startClient(t, 240)
	want := make([]byte, 600)
	for index := range want {
		want[index] = byte(index)
	}
	server.SetBytes(wireAreaDB, 1, 0, want)

	destination := make([]byte, len(want))
	n, err := client.ReadArea(context.Background(), gos7.AreaDB, 1, 0, destination)
	if err != nil || n != len(want) || !bytes.Equal(destination, want) {
		t.Fatalf("read area: n=%d err=%v equal=%v", n, err, bytes.Equal(destination, want))
	}
	requestsAfterRead := server.Requests()
	if len(requestsAfterRead) < 3 {
		t.Fatalf("expected multiple read PDUs, got %d", len(requestsAfterRead))
	}
	for _, request := range requestsAfterRead {
		if request.PDUBytes > 240 {
			t.Fatalf("request exceeded negotiated PDU: %+v", request)
		}
	}

	tooLarge := gos7.WriteItem{
		Address:   gos7.Address{Area: gos7.AreaDB, DBNumber: 1, Offset: 300},
		Transport: gos7.TransportByte,
		Count:     213,
		Data:      make([]byte, 213),
	}
	results, err := client.Write(context.Background(), []gos7.WriteItem{{
		Address:   gos7.Address{Area: gos7.AreaDB, DBNumber: 1, Offset: 700},
		Transport: gos7.TransportByte,
		Count:     1,
		Data:      []byte{0xff},
	}, tooLarge})
	if !errors.Is(err, gos7.ErrLimit) || len(results) != 2 ||
		results[0].Outcome != gos7.WriteNotAttempted || results[1].Outcome != gos7.WriteNotAttempted {
		t.Fatalf("oversized ordinary write: results=%+v err=%v", results, err)
	}
	var typedErr *gos7.Error
	if !errors.As(err, &typedErr) || typedErr.Impact != gos7.SessionUnchanged || client.State() != gos7.StateReady {
		t.Fatalf("local limit changed session: error=%+v state=%v", typedErr, client.State())
	}
	if len(server.Requests()) != len(requestsAfterRead) {
		t.Fatal("oversized ordinary write reached the PLC")
	}
	if actual := server.Bytes(wireAreaDB, 1, 700, 1); !bytes.Equal(actual, []byte{0}) {
		t.Fatalf("item before oversized write was applied: %x", actual)
	}

	source := bytes.Repeat([]byte{0x5a}, 500)
	n, err = client.WriteArea(context.Background(), gos7.AreaDB, 1, 300, source)
	if err != nil || n != len(source) {
		t.Fatalf("write area: n=%d err=%v", n, err)
	}
	if actual := server.Bytes(wireAreaDB, 1, 300, len(source)); !bytes.Equal(actual, source) {
		t.Fatal("WriteArea data mismatch")
	}
}

func TestWriteAreaReportsConfirmedPrefix(t *testing.T) {
	server, client := startClient(t, 240)
	server.RejectItemAfter(1, 0x05)
	source := bytes.Repeat([]byte{0x33}, 500)
	n, err := client.WriteArea(context.Background(), gos7.AreaDB, 1, 100, source)
	if !errors.Is(err, gos7.ErrPLC) {
		t.Fatalf("expected PLC rejection, n=%d err=%v", n, err)
	}
	if n != 212 {
		t.Fatalf("confirmed prefix=%d, want 212", n)
	}
	if actual := server.Bytes(wireAreaDB, 1, 100, 212); !bytes.Equal(actual, source[:212]) {
		t.Fatal("acknowledged prefix was not written")
	}
	if actual := server.Bytes(wireAreaDB, 1, 312, len(source)-212); !bytes.Equal(actual, make([]byte, len(source)-212)) {
		t.Fatalf("data after first rejection was written: %x", actual)
	}
}

func TestLogicalBatchUsesTwentyItemCompatibilityLimit(t *testing.T) {
	server, client := startClient(t, 480)
	reads := make([]gos7.ReadItem, 21)
	writes := make([]gos7.WriteItem, 21)
	for index := range reads {
		reads[index] = gos7.ReadItem{
			Address:   gos7.Address{Area: gos7.AreaDB, DBNumber: 1, Offset: uint32(index)},
			Transport: gos7.TransportByte,
			Count:     1,
		}
		writes[index] = gos7.WriteItem{
			Address:   gos7.Address{Area: gos7.AreaDB, DBNumber: 1, Offset: uint32(index)},
			Transport: gos7.TransportByte,
			Count:     1,
			Data:      []byte{byte(index)},
		}
	}
	if results, err := client.Read(context.Background(), reads); err != nil || len(results) != len(reads) {
		t.Fatalf("read 21 items: results=%d err=%v", len(results), err)
	}
	if results, err := client.Write(context.Background(), writes); err != nil || len(results) != len(writes) {
		t.Fatalf("write 21 items: results=%d err=%v", len(results), err)
	}
	readRequests, writeRequests := 0, 0
	for _, request := range server.Requests() {
		if request.ItemCount > 20 {
			t.Fatalf("request exceeded compatibility item limit: %+v", request)
		}
		switch request.Function {
		case 0x04:
			readRequests++
		case 0x05:
			writeRequests++
		}
	}
	if readRequests != 2 || writeRequests != 2 {
		t.Fatalf("request counts read=%d write=%d, want 2/2", readRequests, writeRequests)
	}
}

func TestWriteUnknownIsNotRetriedAndSessionCanBeReconnected(t *testing.T) {
	server, client := startClient(t, 480)
	server.DropNextWriteResponse()
	results, err := client.Write(context.Background(), []gos7.WriteItem{{
		Address:   gos7.Address{Area: gos7.AreaDB, DBNumber: 1, Offset: 10},
		Transport: gos7.TransportByte,
		Count:     2,
		Data:      []byte{0xaa, 0x55},
	}})
	if err == nil || results[0].Outcome != gos7.WriteUnknown || client.State() != gos7.StateBroken {
		t.Fatalf("write ambiguity lost: result=%+v err=%v state=%v", results[0], err, client.State())
	}
	if actual := server.Bytes(wireAreaDB, 1, 10, 2); !bytes.Equal(actual, []byte{0xaa, 0x55}) {
		t.Fatalf("scripted PLC did not apply ambiguous write: %x", actual)
	}
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("explicit reconnect failed: %v", err)
	}
	if client.Diagnostics().SessionGeneration != 2 {
		t.Fatalf("session generation did not advance: %+v", client.Diagnostics())
	}
}

func TestCallerTimeoutBreaksSession(t *testing.T) {
	server, client := startClient(t, 480)
	server.DelayNextResponse(200 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	results, err := client.Read(ctx, []gos7.ReadItem{{
		Address:   gos7.Address{Area: gos7.AreaDB, DBNumber: 1},
		Transport: gos7.TransportByte,
		Count:     1,
	}})
	if !errors.Is(err, gos7.ErrTimeout) || !errors.Is(results[0].Err, gos7.ErrTimeout) {
		t.Fatalf("expected timeout, results=%+v err=%v", results, err)
	}
	if client.State() != gos7.StateBroken {
		t.Fatalf("timeout left session reusable: %v", client.State())
	}
	if diagnostics := client.Diagnostics(); diagnostics.LastSessionFailureKind != gos7.ErrorTimeout {
		t.Fatalf("last session failure=%q, want timeout", diagnostics.LastSessionFailureKind)
	}
}

func TestExchangeTimeoutBreaksSession(t *testing.T) {
	server, err := testplc.Start(480)
	if err != nil {
		t.Fatal(err)
	}
	client, err := gos7.Dial(context.Background(), gos7.Config{
		Endpoint: server.Endpoint(),
		Addressing: gos7.Addressing{
			Mode: gos7.AddressByRackSlot, Rack: 0, Slot: 2, Role: gos7.ConnectionPG,
		},
		ExchangeTimeout: 20 * time.Millisecond,
	})
	if err != nil {
		_ = server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	server.DelayNextResponse(200 * time.Millisecond)
	results, err := client.Read(context.Background(), []gos7.ReadItem{{
		Address: gos7.Address{Area: gos7.AreaDB, DBNumber: 1}, Transport: gos7.TransportByte, Count: 1,
	}})
	if !errors.Is(err, gos7.ErrTimeout) || !errors.Is(results[0].Err, gos7.ErrTimeout) || client.State() != gos7.StateBroken {
		t.Fatalf("exchange timeout: results=%+v err=%v state=%v", results, err, client.State())
	}
	if diagnostics := client.Diagnostics(); diagnostics.LastSessionFailureKind != gos7.ErrorTimeout {
		t.Fatalf("last session failure=%q, want timeout", diagnostics.LastSessionFailureKind)
	}
}

func TestQueueCancellationDoesNotStartSecondIO(t *testing.T) {
	server, client := startClient(t, 480)
	server.DelayNextResponse(120 * time.Millisecond)
	firstDone := make(chan error, 1)
	go func() {
		_, err := client.Read(context.Background(), []gos7.ReadItem{{
			Address:   gos7.Address{Area: gos7.AreaDB, DBNumber: 1},
			Transport: gos7.TransportByte,
			Count:     1,
		}})
		firstDone <- err
	}()
	waitForRequests(t, server, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := client.Read(ctx, []gos7.ReadItem{{
		Address:   gos7.Address{Area: gos7.AreaDB, DBNumber: 1, Offset: 1},
		Transport: gos7.TransportByte,
		Count:     1,
	}})
	if !errors.Is(err, gos7.ErrTimeout) {
		t.Fatalf("queued request: %v", err)
	}
	if requestCount := len(server.Requests()); requestCount != 1 {
		t.Fatalf("canceled queued request reached PLC: %d requests", requestCount)
	}
	if err := <-firstDone; err != nil {
		t.Fatalf("first request failed: %v", err)
	}
}

func TestCloseInterruptsInFlightIO(t *testing.T) {
	server, client := startClient(t, 480)
	server.DelayNextResponse(5 * time.Second)
	done := make(chan error, 1)
	go func() {
		_, err := client.Read(context.Background(), []gos7.ReadItem{{
			Address:   gos7.Address{Area: gos7.AreaDB, DBNumber: 1},
			Transport: gos7.TransportByte,
			Count:     1,
		}})
		done <- err
	}()
	waitForRequests(t, server, 1)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, gos7.ErrClosed) {
			t.Fatalf("in-flight close returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not interrupt in-flight I/O")
	}
}

func TestValidationPreventsIO(t *testing.T) {
	server, client := startClient(t, 480)
	before := len(server.Requests())
	results, err := client.Write(context.Background(), []gos7.WriteItem{{
		Address:   gos7.Address{Area: gos7.AreaDB, DBNumber: 1},
		Transport: gos7.TransportWord,
		Count:     1,
		Data:      []byte{1},
	}})
	if !errors.Is(err, gos7.ErrInvalidArgument) || results[0].Outcome != gos7.WriteNotAttempted {
		t.Fatalf("validation result=%+v err=%v", results, err)
	}
	if len(server.Requests()) != before {
		t.Fatal("invalid request reached PLC")
	}

	large := make([]byte, 8192)
	results, err = client.Write(context.Background(), []gos7.WriteItem{{
		Address:   gos7.Address{Area: gos7.AreaDB, DBNumber: 1},
		Transport: gos7.TransportByte,
		Count:     uint32(len(large)),
		Data:      large,
	}})
	if !errors.Is(err, gos7.ErrLimit) || results[0].Outcome != gos7.WriteNotAttempted {
		t.Fatalf("encoded-length validation result=%+v err=%v", results, err)
	}
	if len(server.Requests()) != before {
		t.Fatal("encoded-length overflow reached PLC")
	}

	reads, err := client.Read(context.Background(), []gos7.ReadItem{{
		Address: gos7.Address{Area: gos7.AreaTimer, Offset: 0xFFFFFF}, Transport: gos7.TransportTimer, Count: 2,
	}})
	if !errors.Is(err, gos7.ErrLimit) || len(reads) != 1 || !errors.Is(reads[0].Err, gos7.ErrLimit) {
		t.Fatalf("timer span validation: results=%+v err=%v", reads, err)
	}
	if len(server.Requests()) != before {
		t.Fatal("invalid timer span reached PLC")
	}
}

func waitForRequests(t *testing.T, server *testplc.Server, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(server.Requests()) >= count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("server did not observe %d requests", count)
}
