package isotcp

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

type shortWriter struct {
	buffer bytes.Buffer
	limit  int
}

func (writer *shortWriter) Write(data []byte) (int, error) {
	if len(data) > writer.limit {
		data = data[:writer.limit]
	}
	return writer.buffer.Write(data)
}

func TestTPKTRoundTripWithShortReadsAndWrites(t *testing.T) {
	payload := []byte{0x32, 0x01, 0, 0, 0, 1}
	frame, err := WrapData(payload)
	if err != nil {
		t.Fatal(err)
	}
	writer := &shortWriter{limit: 2}
	if written, err := WriteFull(writer, frame); err != nil || written != len(frame) {
		t.Fatalf("write: n=%d err=%v", written, err)
	}
	readFrame, err := ReadFrame(io.LimitReader(bytes.NewReader(writer.buffer.Bytes()), int64(len(frame))), 128)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := UnwrapData(readFrame)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, payload) {
		t.Fatalf("got %x, want %x", actual, payload)
	}
}

func TestReadFrameRejectsDeclaredLengthBeforeAllocation(t *testing.T) {
	header := []byte{3, 0, 0xff, 0xff}
	if _, err := ReadFrame(bytes.NewReader(header), 4096); err == nil {
		t.Fatal("expected frame-limit error")
	}
	for _, frame := range [][]byte{{2, 0, 0, 7, 2, 0xf0, 0x80}, {3, 1, 0, 7, 2, 0xf0, 0x80}, {3, 0, 0, 6}} {
		if _, err := ReadFrame(bytes.NewReader(frame), 4096); err == nil {
			t.Fatalf("expected invalid TPKT error for %x", frame)
		}
	}
}

func TestConnectionRequestAndConfirm(t *testing.T) {
	request, err := BuildConnectionRequest(0x0100, 0x0203, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if binary.BigEndian.Uint16(request[16:18]) != 0x0100 || binary.BigEndian.Uint16(request[20:22]) != 0x0203 {
		t.Fatalf("wrong TSAP request: %x", request)
	}
	confirm := append([]byte(nil), request...)
	confirm[5] = 0xd0
	binary.BigEndian.PutUint16(confirm[6:8], 1)
	binary.BigEndian.PutUint16(confirm[16:18], 0x0203)
	binary.BigEndian.PutUint16(confirm[20:22], 0x0100)
	result, err := ParseConnectionConfirm(confirm, 0x0100, 0x0203, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if result.TPDUSize != 1024 {
		t.Fatalf("got TPDU %d, want 1024", result.TPDUSize)
	}
	confirm[20] = 0xff
	if _, err := ParseConnectionConfirm(confirm, 0x0100, 0x0203, 1024); err == nil {
		t.Fatal("expected TSAP mismatch")
	}
}

func TestConnectionConfirmAcceptsLegacyEchoedTSAPOrder(t *testing.T) {
	request, err := BuildConnectionRequest(0x0100, 0x0101, 1024)
	if err != nil {
		t.Fatal(err)
	}
	confirm := append([]byte(nil), request...)
	confirm[5] = 0xd0
	binary.BigEndian.PutUint16(confirm[6:8], 1)

	if _, err := ParseConnectionConfirm(confirm, 0x0100, 0x0101, 1024); err != nil {
		t.Fatalf("legacy peer echoed the requested TSAP order: %v", err)
	}
	confirm[20] = 0xff
	if _, err := ParseConnectionConfirm(confirm, 0x0100, 0x0101, 1024); err == nil {
		t.Fatal("expected unrelated TSAP pair to be rejected")
	}
}

func TestTPDUSizeSelectionAndPeerReduction(t *testing.T) {
	for _, test := range []struct {
		pdu  int
		want int
	}{{240, 1024}, {960, 1024}, {1022, 2048}, {4096, 8192}} {
		got, err := TPDUSizeForS7PDU(test.pdu)
		if err != nil || got != test.want {
			t.Fatalf("PDU %d: got %d, %v; want %d", test.pdu, got, err, test.want)
		}
	}
	request, err := BuildConnectionRequest(0x0100, 0x0203, 2048)
	if err != nil {
		t.Fatal(err)
	}
	request[5] = 0xd0
	binary.BigEndian.PutUint16(request[6:8], 1)
	request[13] = 0x0a
	binary.BigEndian.PutUint16(request[16:18], 0x0203)
	binary.BigEndian.PutUint16(request[20:22], 0x0100)
	result, err := ParseConnectionConfirm(request, 0x0100, 0x0203, 2048)
	if err != nil || result.TPDUSize != 1024 {
		t.Fatalf("got %+v, %v; want TPDU 1024", result, err)
	}
	if _, err := TPDUSizeForS7PDU(int(^uint(0) >> 1)); err == nil {
		t.Fatal("expected oversized S7 PDU error")
	}
}

func TestUnwrapRejectsFragment(t *testing.T) {
	frame, err := WrapData([]byte{1})
	if err != nil {
		t.Fatal(err)
	}
	frame[6] = 0
	if _, err := UnwrapData(frame); err != ErrCOTPFragmented {
		t.Fatalf("got %v, want ErrCOTPFragmented", err)
	}
}

func TestWrapDataRejectsPayloadBeyondTPKT(t *testing.T) {
	if _, err := WrapData(make([]byte, 65535-DataHeaderSize+1)); err == nil {
		t.Fatal("expected TPKT payload limit error")
	}
}

func FuzzDecodeTPKT(f *testing.F) {
	valid, _ := WrapData([]byte{0x32})
	f.Add(valid)
	f.Add([]byte{3, 0, 0, 7, 2, 0xf0, 0x80})
	f.Fuzz(func(t *testing.T, data []byte) {
		frame, err := ReadFrame(bytes.NewReader(data), 4096)
		if err == nil {
			_, _ = UnwrapData(frame)
		}
	})
}

func FuzzDecodeCOTP(f *testing.F) {
	valid, _ := WrapData([]byte{0x32})
	f.Add(valid)
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = UnwrapData(data)
		_, _ = ParseConnectionConfirm(data, 0x0100, 0x0102, 1024)
	})
}
