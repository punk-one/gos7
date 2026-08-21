package gos7

import (
	"fmt"
	"math"
)

// Area identifies an S7 absolute-address memory area.
type Area uint8

const (
	AreaInput Area = iota + 1
	AreaOutput
	AreaMarker
	AreaDB
	AreaTimer
	AreaCounter
)

func (area Area) String() string {
	switch area {
	case AreaInput:
		return "input"
	case AreaOutput:
		return "output"
	case AreaMarker:
		return "marker"
	case AreaDB:
		return "db"
	case AreaTimer:
		return "timer"
	case AreaCounter:
		return "counter"
	default:
		return fmt.Sprintf("area(%d)", area)
	}
}

// TransportType describes the classic S7 ReadVar/WriteVar wire width. Signed
// interpretation, LREAL, STRING, scaling, and units belong to codec or caller
// policy and are not encoded in this enum.
type TransportType uint8

const (
	TransportBit TransportType = iota + 1
	TransportByte
	TransportWord
	TransportDWord
	TransportReal
	TransportTimer
	TransportCounter
)

func (transport TransportType) String() string {
	switch transport {
	case TransportBit:
		return "bit"
	case TransportByte:
		return "byte"
	case TransportWord:
		return "word"
	case TransportDWord:
		return "dword"
	case TransportReal:
		return "real"
	case TransportTimer:
		return "timer"
	case TransportCounter:
		return "counter"
	default:
		return fmt.Sprintf("transport(%d)", transport)
	}
}

// Address is an absolute PLC address. Offset is a byte offset for DB/I/Q/M
// and an element index for timer/counter areas.
type Address struct {
	Area     Area
	DBNumber uint16
	Offset   uint32
	Bit      uint8
}

// ReadItem describes one logical read. Large byte/word/dword/real items may be
// split over multiple PDUs and are reassembled before success is returned.
type ReadItem struct {
	Address   Address
	Transport TransportType
	Count     uint32
}

// WriteItem describes one logical write. A single item is never split by
// Write; use WriteArea for an explicitly chunkable continuous byte write.
type WriteItem struct {
	Address   Address
	Transport TransportType
	Count     uint32
	Data      []byte
}

// ReadResult corresponds to the input item at the same index.
type ReadResult struct {
	Data []byte
	Err  error
}

// WriteOutcome distinguishes a definite result from an ambiguous transport
// failure. WriteUnknown must never be automatically replayed.
type WriteOutcome uint8

const (
	WriteNotAttempted WriteOutcome = iota
	WriteAcknowledged
	WriteRejected
	WriteUnknown
)

// WriteResult corresponds to the input item at the same index.
type WriteResult struct {
	Outcome WriteOutcome
	Err     error
}

type itemLayout struct {
	wordLength        byte
	responseTransport byte
	width             uint32
	lengthInBits      bool
}

func layoutFor(transport TransportType) (itemLayout, bool) {
	switch transport {
	case TransportBit:
		return itemLayout{wordLength: 0x01, responseTransport: 0x03, width: 1}, true
	case TransportByte:
		return itemLayout{wordLength: 0x02, responseTransport: 0x04, width: 1, lengthInBits: true}, true
	case TransportWord:
		return itemLayout{wordLength: 0x04, responseTransport: 0x04, width: 2, lengthInBits: true}, true
	case TransportDWord:
		return itemLayout{wordLength: 0x06, responseTransport: 0x04, width: 4, lengthInBits: true}, true
	case TransportReal:
		return itemLayout{wordLength: 0x08, responseTransport: 0x07, width: 4}, true
	case TransportTimer:
		return itemLayout{wordLength: 0x1d, responseTransport: 0x09, width: 2}, true
	case TransportCounter:
		return itemLayout{wordLength: 0x1c, responseTransport: 0x09, width: 2}, true
	default:
		return itemLayout{}, false
	}
}

func validateItemAddress(address Address, transport TransportType) error {
	layout, ok := layoutFor(transport)
	if !ok {
		return invalidError("item.transport", "unsupported transport type")
	}
	_ = layout
	switch address.Area {
	case AreaDB:
		if address.DBNumber == 0 {
			return invalidError("item.address.db_number", "DB area requires a non-zero DB number")
		}
	case AreaInput, AreaOutput, AreaMarker:
		if address.DBNumber != 0 {
			return invalidError("item.address.db_number", "non-DB area cannot include a DB number")
		}
	case AreaTimer:
		if address.DBNumber != 0 || address.Bit != 0 || transport != TransportTimer {
			return invalidError("item.address", "timer area requires timer transport without DB or bit")
		}
	case AreaCounter:
		if address.DBNumber != 0 || address.Bit != 0 || transport != TransportCounter {
			return invalidError("item.address", "counter area requires counter transport without DB or bit")
		}
	default:
		return invalidError("item.address.area", "unsupported area")
	}
	if address.Area != AreaTimer && address.Area != AreaCounter {
		if transport == TransportTimer || transport == TransportCounter {
			return invalidError("item.transport", "timer/counter transport requires its matching area")
		}
		if transport == TransportBit {
			if address.Bit > 7 {
				return invalidError("item.address.bit", "bit must be between 0 and 7")
			}
		} else if address.Bit != 0 {
			return invalidError("item.address.bit", "non-bit transport requires bit zero")
		}
		bitAddress := uint64(address.Offset)*8 + uint64(address.Bit)
		if bitAddress > 0xFFFFFF {
			return limitError("item.address", "address exceeds the S7 24-bit field")
		}
	} else if address.Offset > 0xFFFFFF {
		return limitError("item.address", "timer/counter index exceeds the S7 24-bit field")
	}
	return nil
}

func itemDataLength(transport TransportType, count uint32) (int, error) {
	layout, ok := layoutFor(transport)
	if !ok {
		return 0, invalidError("item.transport", "unsupported transport type")
	}
	if count == 0 {
		return 0, invalidError("item.count", "count must be greater than zero")
	}
	if transport == TransportBit && count != 1 {
		return 0, invalidError("item.count", "v0.1 supports exactly one bit per bit item")
	}
	bytes := uint64(count) * uint64(layout.width)
	if bytes > math.MaxInt {
		return 0, limitError("item.count", "encoded data length overflows int")
	}
	return int(bytes), nil
}

func validateReadItem(item ReadItem, maxItemBytes int) (int, error) {
	if err := validateItemAddress(item.Address, item.Transport); err != nil {
		return 0, err
	}
	length, err := itemDataLength(item.Transport, item.Count)
	if err != nil {
		return 0, err
	}
	if length > maxItemBytes {
		return 0, limitError("read.item", "item exceeds max_item_bytes")
	}
	if item.Address.Area != AreaTimer && item.Address.Area != AreaCounter {
		end := uint64(item.Address.Offset) + uint64(length) - 1
		if item.Transport == TransportBit {
			end = uint64(item.Address.Offset)
		}
		if end*8 > 0xFFFFFF {
			return 0, limitError("read.item", "item span exceeds the S7 24-bit address field")
		}
	} else {
		end := uint64(item.Address.Offset) + uint64(item.Count) - 1
		if end > 0xFFFFFF {
			return 0, limitError("read.item", "timer/counter span exceeds the S7 24-bit address field")
		}
	}
	return length, nil
}

func validateWriteItem(item WriteItem, maxItemBytes int) (int, error) {
	length, err := validateReadItem(ReadItem{
		Address: item.Address, Transport: item.Transport, Count: item.Count,
	}, maxItemBytes)
	if err != nil {
		return 0, err
	}
	if item.Count > math.MaxUint16 {
		return 0, limitError("write.item", "one WriteItem cannot exceed the S7 element-count field")
	}
	layout, _ := layoutFor(item.Transport)
	encodedLength := uint64(length)
	if layout.lengthInBits {
		encodedLength *= 8
	}
	if item.Transport == TransportBit {
		encodedLength = uint64(item.Count)
	}
	if encodedLength > math.MaxUint16 {
		return 0, limitError("write.item", "encoded data length exceeds the S7 uint16 field")
	}
	if len(item.Data) != length {
		return 0, invalidError("write.item.data", "data length does not match transport and count")
	}
	if item.Transport == TransportBit && item.Data[0] > 1 {
		return 0, invalidError("write.item.data", "bit data must be zero or one")
	}
	return length, nil
}
