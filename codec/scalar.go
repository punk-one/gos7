// Package codec provides bounds-checked, byte-oriented Siemens value codecs.
// It contains no network, scaling, unit, quality, or address policy.
package codec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"
)

func exact(data []byte, length int, name string) error {
	if len(data) != length {
		return fmt.Errorf("codec: %s requires exactly %d bytes, got %d", name, length, len(data))
	}
	return nil
}

// Bool decodes a native bit payload represented as exactly one 0/1 byte.
func Bool(data []byte) (bool, error) {
	if err := exact(data, 1, "bool"); err != nil {
		return false, err
	}
	switch data[0] {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, errors.New("codec: bool byte must be zero or one")
	}
}

// PutBool writes a native bit payload represented as one byte.
func PutBool(destination []byte, value bool) error {
	if err := exact(destination, 1, "bool"); err != nil {
		return err
	}
	destination[0] = 0
	if value {
		destination[0] = 1
	}
	return nil
}

// Bit extracts one bit from a byte slice without modifying it.
func Bit(data []byte, bitOffset uint32) (bool, error) {
	byteOffset := uint64(bitOffset) / 8
	if byteOffset >= uint64(len(data)) {
		return false, errors.New("codec: bit offset outside buffer")
	}
	bit := uint(bitOffset % 8)
	return data[byteOffset]&(1<<bit) != 0, nil
}

// SetBit updates one bit in a byte slice without changing neighboring bits.
func SetBit(data []byte, bitOffset uint32, value bool) error {
	byteOffset := uint64(bitOffset) / 8
	if byteOffset >= uint64(len(data)) {
		return errors.New("codec: bit offset outside buffer")
	}
	mask := byte(1 << uint(bitOffset%8))
	if value {
		data[byteOffset] |= mask
	} else {
		data[byteOffset] &^= mask
	}
	return nil
}

func Int8(data []byte) (int8, error) {
	if err := exact(data, 1, "int8"); err != nil {
		return 0, err
	}
	return int8(data[0]), nil
}

func Uint8(data []byte) (uint8, error) {
	if err := exact(data, 1, "uint8"); err != nil {
		return 0, err
	}
	return data[0], nil
}

func PutInt8(destination []byte, value int8) error {
	if err := exact(destination, 1, "int8"); err != nil {
		return err
	}
	destination[0] = byte(value)
	return nil
}

func PutUint8(destination []byte, value uint8) error {
	if err := exact(destination, 1, "uint8"); err != nil {
		return err
	}
	destination[0] = value
	return nil
}

func Int16(data []byte) (int16, error) {
	value, err := Uint16(data)
	return int16(value), err
}

func Uint16(data []byte) (uint16, error) {
	if err := exact(data, 2, "uint16"); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(data), nil
}

func PutInt16(destination []byte, value int16) error {
	return PutUint16(destination, uint16(value))
}

func PutUint16(destination []byte, value uint16) error {
	if err := exact(destination, 2, "uint16"); err != nil {
		return err
	}
	binary.BigEndian.PutUint16(destination, value)
	return nil
}

func Int32(data []byte) (int32, error) {
	value, err := Uint32(data)
	return int32(value), err
}

func Uint32(data []byte) (uint32, error) {
	if err := exact(data, 4, "uint32"); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(data), nil
}

func PutInt32(destination []byte, value int32) error {
	return PutUint32(destination, uint32(value))
}

func PutUint32(destination []byte, value uint32) error {
	if err := exact(destination, 4, "uint32"); err != nil {
		return err
	}
	binary.BigEndian.PutUint32(destination, value)
	return nil
}

func Int64(data []byte) (int64, error) {
	value, err := Uint64(data)
	return int64(value), err
}

func Uint64(data []byte) (uint64, error) {
	if err := exact(data, 8, "uint64"); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(data), nil
}

func PutInt64(destination []byte, value int64) error {
	return PutUint64(destination, uint64(value))
}

func PutUint64(destination []byte, value uint64) error {
	if err := exact(destination, 8, "uint64"); err != nil {
		return err
	}
	binary.BigEndian.PutUint64(destination, value)
	return nil
}

// Float32 decodes a Siemens REAL in network byte order.
func Float32(data []byte) (float32, error) {
	bits, err := Uint32(data)
	if err != nil {
		return 0, err
	}
	return math.Float32frombits(bits), nil
}

// PutFloat32 encodes a Siemens REAL in network byte order.
func PutFloat32(destination []byte, value float32) error {
	return PutUint32(destination, math.Float32bits(value))
}

// Float64 decodes an eight-byte LREAL in network byte order.
func Float64(data []byte) (float64, error) {
	bits, err := Uint64(data)
	if err != nil {
		return 0, err
	}
	return math.Float64frombits(bits), nil
}

// PutFloat64 encodes an eight-byte LREAL in network byte order.
func PutFloat64(destination []byte, value float64) error {
	return PutUint64(destination, math.Float64bits(value))
}

// DecodeClassicString returns the byte payload of a classic S7 STRING. It is
// byte-preserving: character encoding is an explicit caller/adapter decision.
func DecodeClassicString(data []byte) ([]byte, error) {
	if len(data) < 2 {
		return nil, errors.New("codec: classic STRING header requires two bytes")
	}
	capacity := int(data[0])
	length := int(data[1])
	if capacity > 254 {
		return nil, errors.New("codec: classic STRING capacity exceeds 254")
	}
	if len(data) != 2+capacity {
		return nil, fmt.Errorf("codec: classic STRING capacity requires %d bytes, got %d", 2+capacity, len(data))
	}
	if length > capacity {
		return nil, errors.New("codec: classic STRING length exceeds capacity")
	}
	return append([]byte(nil), data[2:2+length]...), nil
}

// EncodeClassicString encodes bytes into a caller-sized classic S7 STRING.
// No truncation is performed. When clearUnused is true, the unused capacity is
// zeroed to avoid retaining previous PLC data.
func EncodeClassicString(destination []byte, capacity uint8, value []byte, clearUnused bool) error {
	if capacity > 254 {
		return errors.New("codec: classic STRING capacity exceeds 254")
	}
	if len(destination) != 2+int(capacity) {
		return fmt.Errorf("codec: classic STRING capacity requires %d bytes, got %d", 2+int(capacity), len(destination))
	}
	if len(value) > int(capacity) {
		return errors.New("codec: classic STRING value exceeds capacity")
	}
	destination[0] = capacity
	destination[1] = byte(len(value))
	copy(destination[2:], value)
	if clearUnused {
		for index := 2 + len(value); index < len(destination); index++ {
			destination[index] = 0
		}
	}
	return nil
}

// DecodeCounter decodes a three-digit S7 BCD counter value.
func DecodeCounter(raw uint16) (int, error) {
	if raw&0xf000 != 0 {
		return 0, errors.New("codec: counter reserved bits are non-zero")
	}
	hundreds := int((raw >> 8) & 0x0f)
	tens := int((raw >> 4) & 0x0f)
	ones := int(raw & 0x0f)
	if hundreds > 9 || tens > 9 || ones > 9 {
		return 0, errors.New("codec: counter contains invalid BCD")
	}
	return hundreds*100 + tens*10 + ones, nil
}

// EncodeCounter encodes a value in the classic 0..999 S7 counter format.
func EncodeCounter(value int) (uint16, error) {
	if value < 0 || value > 999 {
		return 0, errors.New("codec: counter value must be between 0 and 999")
	}
	return uint16(value/100)<<8 | uint16((value/10)%10)<<4 | uint16(value%10), nil
}

// DecodeS5Time decodes a classic two-byte S5TIME timer value.
func DecodeS5Time(raw uint16) (time.Duration, error) {
	if raw&0xc000 != 0 {
		return 0, errors.New("codec: S5TIME reserved bits are non-zero")
	}
	hundreds := int((raw >> 8) & 0x0f)
	tens := int((raw >> 4) & 0x0f)
	ones := int(raw & 0x0f)
	if hundreds > 9 || tens > 9 || ones > 9 {
		return 0, errors.New("codec: S5TIME contains invalid BCD")
	}
	value := hundreds*100 + tens*10 + ones
	bases := [...]time.Duration{10 * time.Millisecond, 100 * time.Millisecond, time.Second, 10 * time.Second}
	return time.Duration(value) * bases[(raw>>12)&0x03], nil
}

// EncodeS5Time selects the finest exact S5TIME base that can hold value.
func EncodeS5Time(value time.Duration) (uint16, error) {
	if value < 0 {
		return 0, errors.New("codec: S5TIME cannot be negative")
	}
	bases := [...]time.Duration{10 * time.Millisecond, 100 * time.Millisecond, time.Second, 10 * time.Second}
	for index, base := range bases {
		if value%base != 0 {
			continue
		}
		count := int64(value / base)
		if count > 999 {
			continue
		}
		raw := uint16(index)<<12 |
			uint16(count/100)<<8 |
			uint16((count/10)%10)<<4 |
			uint16(count%10)
		return raw, nil
	}
	return 0, errors.New("codec: S5TIME value is not exactly representable")
}
