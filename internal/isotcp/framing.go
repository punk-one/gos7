// Package isotcp implements the bounded RFC 1006 TPKT and COTP subset used by
// classic S7comm. It contains no retry, reconnect, logging, or session policy.
package isotcp

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	TPKTHeaderSize     = 4
	DataTPDUHeaderSize = 3
	DataHeaderSize     = TPKTHeaderSize + DataTPDUHeaderSize
	MinTPDUSize        = 128
	MaxTPDUSize        = 8192
	DefaultTPDUSize    = 1024
	maxTPKTLength      = 65535
)

var ErrCOTPFragmented = errors.New("isotcp: fragmented COTP DT is unsupported")

// ReadFrame reads exactly one bounded TPKT frame.
func ReadFrame(reader io.Reader, maxFrameBytes int) ([]byte, error) {
	if maxFrameBytes < DataHeaderSize || maxFrameBytes > maxTPKTLength {
		return nil, fmt.Errorf("isotcp: invalid frame limit %d", maxFrameBytes)
	}
	header := make([]byte, TPKTHeaderSize)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, fmt.Errorf("isotcp: read TPKT header: %w", err)
	}
	if header[0] != 0x03 || header[1] != 0x00 {
		return nil, fmt.Errorf("isotcp: invalid TPKT version or reserved byte")
	}
	length := int(binary.BigEndian.Uint16(header[2:4]))
	if length < DataHeaderSize || length > maxFrameBytes {
		return nil, fmt.Errorf("isotcp: TPKT length %d outside 7..%d", length, maxFrameBytes)
	}
	frame := make([]byte, length)
	copy(frame, header)
	if _, err := io.ReadFull(reader, frame[TPKTHeaderSize:]); err != nil {
		return nil, fmt.Errorf("isotcp: read TPKT body: %w", err)
	}
	return frame, nil
}

// WriteFull writes all bytes or returns the number accepted before failure.
func WriteFull(writer io.Writer, frame []byte) (int, error) {
	written := 0
	for written < len(frame) {
		n, err := writer.Write(frame[written:])
		if n < 0 || n > len(frame)-written {
			return written, fmt.Errorf("isotcp: invalid write count %d", n)
		}
		written += n
		if err != nil {
			return written, fmt.Errorf("isotcp: write frame: %w", err)
		}
		if n == 0 {
			return written, io.ErrNoProgress
		}
	}
	return written, nil
}

// TPDUSizeForS7PDU returns the smallest supported TPDU that can contain one
// complete S7 PDU and the three-byte COTP DT header. A 1024-byte minimum keeps
// the classic Siemens/Snap7 connection profile for ordinary 240..960 PDUs.
func TPDUSizeForS7PDU(pduBytes int) (int, error) {
	if pduBytes < 1 || pduBytes > MaxTPDUSize-DataTPDUHeaderSize {
		return 0, fmt.Errorf("isotcp: S7 PDU size must be between 1 and %d", MaxTPDUSize-DataTPDUHeaderSize)
	}
	required := pduBytes + DataTPDUHeaderSize
	for size := DefaultTPDUSize; size <= MaxTPDUSize; size *= 2 {
		if required <= size {
			return size, nil
		}
	}
	return 0, fmt.Errorf("isotcp: S7 PDU does not fit the maximum TPDU")
}

func tpduSizeCode(size int) (byte, error) {
	if size < MinTPDUSize || size > MaxTPDUSize || size&(size-1) != 0 {
		return 0, fmt.Errorf("isotcp: TPDU size must be a power of two between 128 and 8192")
	}
	code := byte(0)
	for value := size; value > 1; value >>= 1 {
		code++
	}
	return code, nil
}

func tpduSizeFromCode(code byte) (int, error) {
	if code < 7 || code > 13 {
		return 0, fmt.Errorf("isotcp: unsupported TPDU-size code 0x%02x", code)
	}
	return 1 << code, nil
}

// BuildConnectionRequest constructs a COTP CR with explicit TSAP values and
// an explicit maximum TPDU size.
func BuildConnectionRequest(localTSAP, remoteTSAP uint16, tpduSize int) ([]byte, error) {
	tpduCode, err := tpduSizeCode(tpduSize)
	if err != nil {
		return nil, err
	}
	frame := []byte{
		0x03, 0x00, 0x00, 0x16,
		0x11, 0xe0, 0x00, 0x00, 0x00, 0x01, 0x00,
		0xc0, 0x01, 0x00,
		0xc1, 0x02, 0x00, 0x00,
		0xc2, 0x02, 0x00, 0x00,
	}
	frame[13] = tpduCode
	binary.BigEndian.PutUint16(frame[16:18], localTSAP)
	binary.BigEndian.PutUint16(frame[20:22], remoteTSAP)
	return frame, nil
}

// ConnectionConfirm contains the bounded transport result of a COTP CC.
type ConnectionConfirm struct {
	TPDUSize int
}

// ParseConnectionConfirm validates a COTP CC. TSAP echo and TPDU-size
// parameters are optional. When TPDU size is omitted, the offered limit is
// retained; a peer advertising a larger limit cannot raise the local offer.
func ParseConnectionConfirm(frame []byte, localTSAP, remoteTSAP uint16, offeredTPDU int) (ConnectionConfirm, error) {
	if _, err := tpduSizeCode(offeredTPDU); err != nil {
		return ConnectionConfirm{}, err
	}
	if len(frame) < 11 {
		return ConnectionConfirm{}, fmt.Errorf("isotcp: short COTP connection confirm")
	}
	if frame[0] != 0x03 || frame[1] != 0 || int(binary.BigEndian.Uint16(frame[2:4])) != len(frame) {
		return ConnectionConfirm{}, fmt.Errorf("isotcp: invalid connection-confirm TPKT header")
	}
	headerLength := int(frame[4])
	if headerLength < 6 || 5+headerLength != len(frame) {
		return ConnectionConfirm{}, fmt.Errorf("isotcp: invalid COTP connection-confirm length")
	}
	if frame[5] != 0xd0 {
		return ConnectionConfirm{}, fmt.Errorf("isotcp: expected COTP CC, got 0x%02x", frame[5])
	}
	if binary.BigEndian.Uint16(frame[6:8]) != 1 {
		return ConnectionConfirm{}, fmt.Errorf("isotcp: COTP destination reference mismatch")
	}
	if frame[10]&0xf0 != 0 {
		return ConnectionConfirm{}, fmt.Errorf("isotcp: unsupported COTP class")
	}
	result := ConnectionConfirm{TPDUSize: offeredTPDU}
	var sourceTSAP, destinationTSAP *uint16
	for offset := 11; offset < len(frame); {
		if len(frame)-offset < 2 {
			return ConnectionConfirm{}, fmt.Errorf("isotcp: truncated COTP parameter")
		}
		parameter := frame[offset]
		length := int(frame[offset+1])
		offset += 2
		if length > len(frame)-offset {
			return ConnectionConfirm{}, fmt.Errorf("isotcp: truncated COTP parameter value")
		}
		value := frame[offset : offset+length]
		switch parameter {
		case 0xc0:
			if length != 1 {
				return ConnectionConfirm{}, fmt.Errorf("isotcp: invalid TPDU-size parameter")
			}
			peerTPDU, err := tpduSizeFromCode(value[0])
			if err != nil {
				return ConnectionConfirm{}, err
			}
			if peerTPDU < result.TPDUSize {
				result.TPDUSize = peerTPDU
			}
		case 0xc1:
			if length != 2 {
				return ConnectionConfirm{}, fmt.Errorf("isotcp: invalid source TSAP parameter")
			}
			got := binary.BigEndian.Uint16(value)
			sourceTSAP = &got
		case 0xc2:
			if length != 2 {
				return ConnectionConfirm{}, fmt.Errorf("isotcp: invalid destination TSAP parameter")
			}
			got := binary.BigEndian.Uint16(value)
			destinationTSAP = &got
		}
		offset += length
	}
	if sourceTSAP != nil && destinationTSAP != nil {
		standardOrder := *sourceTSAP == remoteTSAP && *destinationTSAP == localTSAP
		echoOrder := *sourceTSAP == localTSAP && *destinationTSAP == remoteTSAP
		if !standardOrder && !echoOrder {
			return ConnectionConfirm{}, fmt.Errorf("isotcp: TSAP pair mismatch")
		}
	} else {
		if sourceTSAP != nil && *sourceTSAP != remoteTSAP {
			return ConnectionConfirm{}, fmt.Errorf("isotcp: source TSAP mismatch")
		}
		if destinationTSAP != nil && *destinationTSAP != localTSAP {
			return ConnectionConfirm{}, fmt.Errorf("isotcp: destination TSAP mismatch")
		}
	}
	return result, nil
}

// WrapData adds TPKT and COTP DT headers around one complete S7 PDU.
func WrapData(payload []byte) ([]byte, error) {
	if len(payload) > maxTPKTLength-DataHeaderSize {
		return nil, fmt.Errorf("isotcp: payload exceeds TPKT length field")
	}
	length := DataHeaderSize + len(payload)
	frame := make([]byte, length)
	frame[0] = 0x03
	binary.BigEndian.PutUint16(frame[2:4], uint16(length))
	frame[4] = 0x02
	frame[5] = 0xf0
	frame[6] = 0x80
	copy(frame[DataHeaderSize:], payload)
	return frame, nil
}

// UnwrapData validates one complete COTP DT and returns its S7 payload.
func UnwrapData(frame []byte) ([]byte, error) {
	if len(frame) < DataHeaderSize {
		return nil, fmt.Errorf("isotcp: short COTP data frame")
	}
	if frame[0] != 0x03 || frame[1] != 0 {
		return nil, fmt.Errorf("isotcp: invalid TPKT header")
	}
	if int(binary.BigEndian.Uint16(frame[2:4])) != len(frame) {
		return nil, fmt.Errorf("isotcp: TPKT frame length mismatch")
	}
	if frame[4] != 0x02 || frame[5] != 0xf0 {
		return nil, fmt.Errorf("isotcp: invalid COTP DT header")
	}
	if frame[6]&0x80 == 0 {
		return nil, ErrCOTPFragmented
	}
	return frame[DataHeaderSize:], nil
}
