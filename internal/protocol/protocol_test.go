package protocol

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func testItem(dataBytes int) Item {
	return Item{
		WordLength:        0x02,
		ResponseTransport: 0x04,
		Amount:            uint16(dataBytes),
		DBNumber:          1,
		Area:              0x84,
		Address:           32,
		DataBytes:         dataBytes,
		LengthInBits:      true,
	}
}

func ack(reference uint16, parameters, data []byte, global uint16) []byte {
	payload := make([]byte, 12+len(parameters)+len(data))
	payload[0] = 0x32
	payload[1] = 0x03
	binary.BigEndian.PutUint16(payload[4:6], reference)
	binary.BigEndian.PutUint16(payload[6:8], uint16(len(parameters)))
	binary.BigEndian.PutUint16(payload[8:10], uint16(len(data)))
	binary.BigEndian.PutUint16(payload[10:12], global)
	copy(payload[12:], parameters)
	copy(payload[12+len(parameters):], data)
	return payload
}

func TestSetupRoundTrip(t *testing.T) {
	request, err := BuildSetupRequest(1, 1, 1, 480)
	if err != nil {
		t.Fatal(err)
	}
	if len(request) != 18 || request[10] != 0xf0 || binary.BigEndian.Uint16(request[16:18]) != 480 {
		t.Fatalf("unexpected setup request %x", request)
	}
	response := ack(1, []byte{0xf0, 0, 0, 1, 0, 1, 0, 240}, nil, 0)
	result, err := ParseSetupResponse(response, 1)
	if err != nil {
		t.Fatal(err)
	}
	if result.PDU != 240 || result.MaxAmQCaller != 1 || result.MaxAmQCallee != 1 {
		t.Fatalf("unexpected setup result %+v", result)
	}
}

func TestReadAndWriteGoldenRequests(t *testing.T) {
	readItem := testItem(2)
	read, err := BuildReadRequest(2, []Item{readItem})
	if err != nil {
		t.Fatal(err)
	}
	wantRead := []byte{
		0x32, 0x01, 0x00, 0x00, 0x00, 0x02, 0x00, 0x0e, 0x00, 0x00,
		0x04, 0x01,
		0x12, 0x0a, 0x10, 0x02, 0x00, 0x02, 0x00, 0x01, 0x84, 0x00, 0x00, 0x20,
	}
	if !bytes.Equal(read, wantRead) {
		t.Fatalf("read request\n got %x\nwant %x", read, wantRead)
	}

	write, err := BuildWriteRequest(3, []WriteItem{{Item: readItem, Data: []byte{0xaa, 0x55}}})
	if err != nil {
		t.Fatal(err)
	}
	wantWrite := []byte{
		0x32, 0x01, 0x00, 0x00, 0x00, 0x03, 0x00, 0x0e, 0x00, 0x06,
		0x05, 0x01,
		0x12, 0x0a, 0x10, 0x02, 0x00, 0x02, 0x00, 0x01, 0x84, 0x00, 0x00, 0x20,
		0x00, 0x04, 0x00, 0x10, 0xaa, 0x55,
	}
	if !bytes.Equal(write, wantWrite) {
		t.Fatalf("write request\n got %x\nwant %x", write, wantWrite)
	}
}

func TestWireItemLimitRemainsIndependentFromCompatibilityLimit(t *testing.T) {
	items := make([]Item, CompatibilityMaxItemsPerPDU+1)
	for index := range items {
		items[index] = testItem(1)
	}
	if _, err := BuildReadRequest(1, items); err != nil {
		t.Fatalf("wire encoder rejected %d items: %v", len(items), err)
	}
	if CompatibilityMaxItemsPerPDU != 20 || WireMaxItemsPerPDU != 255 {
		t.Fatalf("unexpected item limits: compatibility=%d wire=%d", CompatibilityMaxItemsPerPDU, WireMaxItemsPerPDU)
	}
}

func TestGlobalPLCErrorDoesNotRequireVariableParameters(t *testing.T) {
	readResults, code, err := ParseReadResponse(ack(1, nil, nil, 0x8104), 1, []Item{testItem(1)})
	if err != nil || code != 0x8104 || len(readResults) != 1 {
		t.Fatalf("read global error: results=%v code=%x err=%v", readResults, code, err)
	}
	writeResults, code, err := ParseWriteResponse(ack(2, nil, nil, 0x8104), 2, 1)
	if err != nil || code != 0x8104 || len(writeResults) != 1 {
		t.Fatalf("write global error: results=%v code=%x err=%v", writeResults, code, err)
	}
}

func TestReadResponseMixedItemsAndPadding(t *testing.T) {
	items := []Item{testItem(3), testItem(2), testItem(1)}
	responseData := []byte{
		0xff, 0x04, 0, 24, 1, 2, 3, 0,
		0x05, 0, 0, 0,
		0xff, 0x04, 0, 8, 9,
	}
	response := ack(7, []byte{0x04, 3}, responseData, 0)
	results, global, err := ParseReadResponse(response, 7, items)
	if err != nil {
		t.Fatal(err)
	}
	if global != 0 || results[0].ReturnCode != 0xff || results[1].ReturnCode != 0x05 || results[2].Data[0] != 9 {
		t.Fatalf("unexpected results: global=%d results=%+v", global, results)
	}
}

func TestReadResponseAcceptsFailedItemLengthFourWithoutPayload(t *testing.T) {
	response := ack(7, []byte{0x04, 1}, []byte{0x05, 0, 0, 4}, 0)
	results, global, err := ParseReadResponse(response, 7, []Item{testItem(1)})
	if err != nil {
		t.Fatal(err)
	}
	if global != 0 || len(results) != 1 || results[0].ReturnCode != 0x05 || len(results[0].Data) != 0 {
		t.Fatalf("unexpected result: global=%d results=%+v", global, results)
	}
}

func TestReadResponseRejectsReferenceAndTrailingData(t *testing.T) {
	item := testItem(1)
	response := ack(8, []byte{0x04, 1}, []byte{0xff, 0x04, 0, 8, 1}, 0)
	if _, _, err := ParseReadResponse(response, 7, []Item{item}); err == nil {
		t.Fatal("expected reference mismatch")
	}
	response = append(ack(7, []byte{0x04, 1}, []byte{0xff, 0x04, 0, 8, 1}, 0), 0)
	binary.BigEndian.PutUint16(response[8:10], 6)
	if _, _, err := ParseReadResponse(response, 7, []Item{item}); err == nil {
		t.Fatal("expected trailing-data error")
	}
}

func TestWriteRequestPaddingAndResponse(t *testing.T) {
	items := []WriteItem{
		{Item: testItem(1), Data: []byte{1}},
		{Item: testItem(2), Data: []byte{2, 3}},
	}
	request, err := BuildWriteRequest(9, items)
	if err != nil {
		t.Fatal(err)
	}
	if len(request) != WriteRequestSize(items) {
		t.Fatalf("got size %d, want %d", len(request), WriteRequestSize(items))
	}
	response := ack(9, []byte{0x05, 2}, []byte{0xff, 0x0a}, 0)
	codes, global, err := ParseWriteResponse(response, 9, 2)
	if err != nil {
		t.Fatal(err)
	}
	if global != 0 || codes[0] != 0xff || codes[1] != 0x0a {
		t.Fatalf("unexpected write response %x/%d", codes, global)
	}
}

func FuzzDecodeReadVarResponse(f *testing.F) {
	item := testItem(1)
	f.Add(ack(1, []byte{0x04, 1}, []byte{0xff, 0x04, 0, 8, 1}, 0))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, payload []byte) {
		_, _, _ = ParseReadResponse(payload, 1, []Item{item})
	})
}

func FuzzDecodeWriteVarResponse(f *testing.F) {
	f.Add(ack(1, []byte{0x05, 1}, []byte{0xff}, 0))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, payload []byte) {
		_, _, _ = ParseWriteResponse(payload, 1, 1)
	})
}

func FuzzDecodeSetupCommunication(f *testing.F) {
	f.Add(ack(1, []byte{0xf0, 0, 0, 1, 0, 1, 1, 224}, nil, 0))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, payload []byte) {
		_, _ = ParseSetupResponse(payload, 1)
	})
}
