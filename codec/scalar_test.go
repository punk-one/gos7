package codec

import (
	"bytes"
	"math"
	"testing"
	"time"
)

func TestScalarRoundTrips(t *testing.T) {
	buffer := make([]byte, 8)
	if err := PutInt16(buffer[:2], -1234); err != nil {
		t.Fatal(err)
	}
	if value, err := Int16(buffer[:2]); err != nil || value != -1234 {
		t.Fatalf("int16: value=%d err=%v", value, err)
	}
	if err := PutUint32(buffer[:4], 0xfedcba98); err != nil {
		t.Fatal(err)
	}
	if value, err := Uint32(buffer[:4]); err != nil || value != 0xfedcba98 {
		t.Fatalf("uint32: value=%x err=%v", value, err)
	}
	if err := PutFloat32(buffer[:4], -12.5); err != nil {
		t.Fatal(err)
	}
	if value, err := Float32(buffer[:4]); err != nil || value != -12.5 {
		t.Fatalf("float32: value=%v err=%v", value, err)
	}
	if err := PutFloat64(buffer, math.Pi); err != nil {
		t.Fatal(err)
	}
	if value, err := Float64(buffer); err != nil || value != math.Pi {
		t.Fatalf("float64: value=%v err=%v", value, err)
	}
}

func TestScalarRejectsWrongBufferLength(t *testing.T) {
	if _, err := Uint32(make([]byte, 3)); err == nil {
		t.Fatal("expected short-buffer error")
	}
	if err := PutFloat64(make([]byte, 9), 1); err == nil {
		t.Fatal("expected oversized-buffer error")
	}
}

func TestClassicString(t *testing.T) {
	buffer := bytes.Repeat([]byte{0xaa}, 130)
	value := []byte("pump-01")
	if err := EncodeClassicString(buffer, 128, value, true); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeClassicString(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, value) {
		t.Fatalf("got %q, want %q", decoded, value)
	}
	if !bytes.Equal(buffer[2+len(value):], make([]byte, 128-len(value))) {
		t.Fatal("unused STRING capacity was not cleared")
	}
	buffer[1] = 129
	if _, err := DecodeClassicString(buffer); err == nil {
		t.Fatal("expected invalid current-length error")
	}
}

func TestBitPreservesNeighbors(t *testing.T) {
	data := []byte{0b10100101}
	if err := SetBit(data, 1, true); err != nil {
		t.Fatal(err)
	}
	if data[0] != 0b10100111 {
		t.Fatalf("unexpected byte %08b", data[0])
	}
	value, err := Bit(data, 7)
	if err != nil || !value {
		t.Fatalf("bit: value=%v err=%v", value, err)
	}
}

func TestCounterAndS5Time(t *testing.T) {
	raw, err := EncodeCounter(987)
	if err != nil {
		t.Fatal(err)
	}
	if value, err := DecodeCounter(raw); err != nil || value != 987 {
		t.Fatalf("counter: value=%d err=%v", value, err)
	}
	rawTime, err := EncodeS5Time(12*time.Second + 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if value, err := DecodeS5Time(rawTime); err != nil || value != 12300*time.Millisecond {
		t.Fatalf("S5TIME: value=%v err=%v", value, err)
	}
}

func FuzzClassicString(f *testing.F) {
	f.Add([]byte{3, 2, 'o', 'k', 0})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = DecodeClassicString(data)
	})
}
