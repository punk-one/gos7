// Package protocol implements the bounded classic S7comm data-plane PDUs used
// by gos7. It does not know about sockets, retry, reconnect, or application
// value types.
package protocol

import (
	"encoding/binary"
	"fmt"
	"math"
)

const (
	protocolID    = 0x32
	rosctrJob     = 0x01
	rosctrAckData = 0x03

	functionReadVar  = 0x04
	functionWriteVar = 0x05
	functionSetup    = 0xf0

	jobHeaderSize      = 10
	ackHeaderSize      = 12
	itemSpecSize       = 12
	itemDataHeaderSize = 4

	// WireMaxItemsPerPDU is the width of the one-byte item-count field.
	WireMaxItemsPerPDU = 255
	// CompatibilityMaxItemsPerPDU follows the broadly supported S7 MultiVars
	// limit used by Snap7 and avoids PLC-specific behavior above 20 items.
	CompatibilityMaxItemsPerPDU = 20
)

// Item is a fully validated wire-level variable descriptor.
type Item struct {
	WordLength        byte
	ResponseTransport byte
	Amount            uint16
	DBNumber          uint16
	Area              byte
	Address           uint32
	DataBytes         int
	LengthInBits      bool
}

// WriteItem adds immutable raw data to a wire descriptor.
type WriteItem struct {
	Item Item
	Data []byte
}

// ItemResult is one validated PLC data-item response. Data aliases the input
// response payload and is valid while that payload remains reachable.
type ItemResult struct {
	ReturnCode byte
	Data       []byte
}

// SetupResult contains a validated Setup Communication response.
type SetupResult struct {
	MaxAmQCaller uint16
	MaxAmQCallee uint16
	PDU          uint16
	GlobalCode   uint16
}

// ReadRequestSize returns the S7 PDU size, excluding TPKT/COTP.
func ReadRequestSize(itemCount int) int {
	return jobHeaderSize + 2 + itemSpecSize*itemCount
}

// ReadResponseSize returns the expected S7 AckData PDU size.
func ReadResponseSize(items []Item) int {
	size := ackHeaderSize + 2
	for index, item := range items {
		size += itemDataHeaderSize + item.DataBytes
		if index != len(items)-1 && item.DataBytes%2 != 0 {
			size++
		}
	}
	return size
}

// WriteRequestSize returns the S7 PDU size, excluding TPKT/COTP.
func WriteRequestSize(items []WriteItem) int {
	size := jobHeaderSize + 2 + itemSpecSize*len(items)
	for index, item := range items {
		size += itemDataHeaderSize + len(item.Data)
		if index != len(items)-1 && len(item.Data)%2 != 0 {
			size++
		}
	}
	return size
}

// WriteResponseSize returns the expected S7 AckData PDU size.
func WriteResponseSize(itemCount int) int {
	return ackHeaderSize + 2 + itemCount
}

// BuildSetupRequest builds a Setup Communication job PDU.
func BuildSetupRequest(reference, maxAmQCaller, maxAmQCallee, requestedPDU uint16) ([]byte, error) {
	if reference == 0 || maxAmQCaller == 0 || maxAmQCallee == 0 || requestedPDU == 0 {
		return nil, fmt.Errorf("protocol: setup fields must be non-zero")
	}
	parameters := make([]byte, 8)
	parameters[0] = functionSetup
	binary.BigEndian.PutUint16(parameters[2:4], maxAmQCaller)
	binary.BigEndian.PutUint16(parameters[4:6], maxAmQCallee)
	binary.BigEndian.PutUint16(parameters[6:8], requestedPDU)
	return buildJob(reference, parameters, nil)
}

// ParseSetupResponse validates a matching Setup Communication AckData PDU.
func ParseSetupResponse(payload []byte, reference uint16) (SetupResult, error) {
	parameters, data, globalCode, err := parseAckData(payload, reference)
	if err != nil {
		return SetupResult{}, err
	}
	if globalCode != 0 {
		return SetupResult{GlobalCode: globalCode}, nil
	}
	if len(parameters) != 8 || len(data) != 0 || parameters[0] != functionSetup || parameters[1] != 0 {
		return SetupResult{}, fmt.Errorf("protocol: malformed setup response")
	}
	return SetupResult{
		MaxAmQCaller: binary.BigEndian.Uint16(parameters[2:4]),
		MaxAmQCallee: binary.BigEndian.Uint16(parameters[4:6]),
		PDU:          binary.BigEndian.Uint16(parameters[6:8]),
		GlobalCode:   globalCode,
	}, nil
}

// BuildReadRequest builds one ReadVar job PDU.
func BuildReadRequest(reference uint16, items []Item) ([]byte, error) {
	if err := validateItems(reference, items); err != nil {
		return nil, err
	}
	parameters := make([]byte, 2+itemSpecSize*len(items))
	parameters[0] = functionReadVar
	parameters[1] = byte(len(items))
	for index, item := range items {
		writeItemSpec(parameters[2+index*itemSpecSize:], item)
	}
	return buildJob(reference, parameters, nil)
}

// ParseReadResponse validates a matching ReadVar AckData PDU.
func ParseReadResponse(payload []byte, reference uint16, expected []Item) ([]ItemResult, uint16, error) {
	parameters, data, globalCode, err := parseAckData(payload, reference)
	if err != nil {
		return nil, 0, err
	}
	if globalCode != 0 {
		return make([]ItemResult, len(expected)), globalCode, nil
	}
	if len(parameters) != 2 || parameters[0] != functionReadVar || int(parameters[1]) != len(expected) {
		return nil, 0, fmt.Errorf("protocol: read response parameter mismatch")
	}
	results := make([]ItemResult, len(expected))
	offset := 0
	for index, item := range expected {
		if len(data)-offset < itemDataHeaderSize {
			return nil, 0, fmt.Errorf("protocol: truncated read item %d header", index)
		}
		returnCode := data[offset]
		transport := data[offset+1]
		lengthField := binary.BigEndian.Uint16(data[offset+2 : offset+4])
		offset += itemDataHeaderSize
		results[index].ReturnCode = returnCode
		if returnCode != 0xff {
			if transport != 0 || (lengthField != 0 && lengthField != 4) {
				return nil, 0, fmt.Errorf("protocol: failed read item %d includes data", index)
			}
			continue
		}
		expectedLength, err := encodedLength(item)
		if err != nil {
			return nil, 0, err
		}
		if transport != item.ResponseTransport || lengthField != expectedLength {
			return nil, 0, fmt.Errorf("protocol: read item %d transport or length mismatch", index)
		}
		if item.DataBytes > len(data)-offset {
			return nil, 0, fmt.Errorf("protocol: truncated read item %d data", index)
		}
		results[index].Data = data[offset : offset+item.DataBytes]
		offset += item.DataBytes
		if index != len(expected)-1 && item.DataBytes%2 != 0 {
			if offset >= len(data) || data[offset] != 0 {
				return nil, 0, fmt.Errorf("protocol: missing read item %d padding", index)
			}
			offset++
		}
	}
	if offset != len(data) {
		return nil, 0, fmt.Errorf("protocol: trailing read response data")
	}
	return results, globalCode, nil
}

// BuildWriteRequest builds one WriteVar job PDU.
func BuildWriteRequest(reference uint16, items []WriteItem) ([]byte, error) {
	if len(items) == 0 || len(items) > WireMaxItemsPerPDU {
		return nil, fmt.Errorf("protocol: write item count outside 1..255")
	}
	descriptors := make([]Item, len(items))
	for index, item := range items {
		descriptors[index] = item.Item
		if len(item.Data) != item.Item.DataBytes {
			return nil, fmt.Errorf("protocol: write item %d data length mismatch", index)
		}
	}
	if err := validateItems(reference, descriptors); err != nil {
		return nil, err
	}
	parameters := make([]byte, 2+itemSpecSize*len(items))
	parameters[0] = functionWriteVar
	parameters[1] = byte(len(items))
	for index, item := range items {
		writeItemSpec(parameters[2+index*itemSpecSize:], item.Item)
	}
	data := make([]byte, 0, WriteRequestSize(items)-jobHeaderSize-len(parameters))
	for index, item := range items {
		length, err := encodedLength(item.Item)
		if err != nil {
			return nil, err
		}
		data = append(data, 0x00, item.Item.ResponseTransport, byte(length>>8), byte(length))
		data = append(data, item.Data...)
		if index != len(items)-1 && len(item.Data)%2 != 0 {
			data = append(data, 0x00)
		}
	}
	return buildJob(reference, parameters, data)
}

// ParseWriteResponse validates a matching WriteVar AckData PDU.
func ParseWriteResponse(payload []byte, reference uint16, itemCount int) ([]byte, uint16, error) {
	if itemCount < 1 || itemCount > WireMaxItemsPerPDU {
		return nil, 0, fmt.Errorf("protocol: write item count outside 1..255")
	}
	parameters, data, globalCode, err := parseAckData(payload, reference)
	if err != nil {
		return nil, 0, err
	}
	if globalCode != 0 {
		return make([]byte, itemCount), globalCode, nil
	}
	if len(parameters) != 2 ||
		parameters[0] != functionWriteVar || int(parameters[1]) != itemCount {
		return nil, 0, fmt.Errorf("protocol: write response parameter mismatch")
	}
	if len(data) != itemCount {
		return nil, 0, fmt.Errorf("protocol: write response item-count mismatch")
	}
	return data, globalCode, nil
}

func validateItems(reference uint16, items []Item) error {
	if reference == 0 {
		return fmt.Errorf("protocol: zero PDU reference")
	}
	if len(items) == 0 || len(items) > WireMaxItemsPerPDU {
		return fmt.Errorf("protocol: item count outside 1..255")
	}
	for index, item := range items {
		if item.WordLength == 0 || item.ResponseTransport == 0 || item.Amount == 0 || item.Area == 0 ||
			item.Address > 0xFFFFFF || item.DataBytes < 1 {
			return fmt.Errorf("protocol: invalid item %d", index)
		}
		if _, err := encodedLength(item); err != nil {
			return fmt.Errorf("protocol: item %d: %w", index, err)
		}
	}
	return nil
}

func encodedLength(item Item) (uint16, error) {
	length := uint64(item.DataBytes)
	if item.LengthInBits {
		length *= 8
	}
	if item.WordLength == 0x01 {
		length = uint64(item.Amount)
	}
	if length > math.MaxUint16 {
		return 0, fmt.Errorf("encoded data length exceeds uint16")
	}
	return uint16(length), nil
}

func writeItemSpec(destination []byte, item Item) {
	destination[0] = 0x12
	destination[1] = 0x0a
	destination[2] = 0x10
	destination[3] = item.WordLength
	binary.BigEndian.PutUint16(destination[4:6], item.Amount)
	binary.BigEndian.PutUint16(destination[6:8], item.DBNumber)
	destination[8] = item.Area
	destination[9] = byte(item.Address >> 16)
	destination[10] = byte(item.Address >> 8)
	destination[11] = byte(item.Address)
}

func buildJob(reference uint16, parameters, data []byte) ([]byte, error) {
	if reference == 0 || len(parameters) > math.MaxUint16 || len(data) > math.MaxUint16 {
		return nil, fmt.Errorf("protocol: job fields exceed wire limits")
	}
	payload := make([]byte, jobHeaderSize+len(parameters)+len(data))
	payload[0] = protocolID
	payload[1] = rosctrJob
	binary.BigEndian.PutUint16(payload[4:6], reference)
	binary.BigEndian.PutUint16(payload[6:8], uint16(len(parameters)))
	binary.BigEndian.PutUint16(payload[8:10], uint16(len(data)))
	copy(payload[jobHeaderSize:], parameters)
	copy(payload[jobHeaderSize+len(parameters):], data)
	return payload, nil
}

func parseAckData(payload []byte, reference uint16) ([]byte, []byte, uint16, error) {
	if reference == 0 || len(payload) < ackHeaderSize {
		return nil, nil, 0, fmt.Errorf("protocol: short AckData PDU")
	}
	if payload[0] != protocolID || payload[1] != rosctrAckData {
		return nil, nil, 0, fmt.Errorf("protocol: invalid protocol ID or ROSCTR")
	}
	if payload[2] != 0 || payload[3] != 0 {
		return nil, nil, 0, fmt.Errorf("protocol: non-zero redundancy identification")
	}
	if binary.BigEndian.Uint16(payload[4:6]) != reference {
		return nil, nil, 0, fmt.Errorf("protocol: PDU reference mismatch")
	}
	parameterLength := int(binary.BigEndian.Uint16(payload[6:8]))
	dataLength := int(binary.BigEndian.Uint16(payload[8:10]))
	if parameterLength > len(payload)-ackHeaderSize || dataLength != len(payload)-ackHeaderSize-parameterLength {
		return nil, nil, 0, fmt.Errorf("protocol: AckData length mismatch")
	}
	globalCode := uint16(payload[10])<<8 | uint16(payload[11])
	parameters := payload[ackHeaderSize : ackHeaderSize+parameterLength]
	data := payload[ackHeaderSize+parameterLength:]
	return parameters, data, globalCode, nil
}
