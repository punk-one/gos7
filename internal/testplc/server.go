// Package testplc provides a small, pure-Go scripted classic S7 server for
// offline tests. It is not imported by production packages.
package testplc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/punk-one/gos7/internal/isotcp"
)

type memoryKey struct {
	area   byte
	db     uint16
	offset uint32
}

// Request records one accepted S7 data-plane request.
type Request struct {
	Function  byte
	ItemCount int
	PDUBytes  int
}

// Server is a deterministic in-memory PLC used only by tests.
type Server struct {
	listener  net.Listener
	pdu       uint16
	closed    chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup

	mu                    sync.Mutex
	connections           map[net.Conn]struct{}
	memory                map[memoryKey]byte
	requests              []Request
	dropNextWriteResponse bool
	delayNextResponse     time.Duration
	rejectNextItem        byte
	rejectAfterRequests   int
	firstError            error
}

// Start listens on an ephemeral loopback port and negotiates the requested
// PDU (bounded by serverPDU).
func Start(serverPDU uint16) (*Server, error) {
	if serverPDU < 64 {
		return nil, errors.New("testplc: PDU must be at least 64")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	server := &Server{
		listener:    listener,
		pdu:         serverPDU,
		closed:      make(chan struct{}),
		connections: make(map[net.Conn]struct{}),
		memory:      make(map[memoryKey]byte),
	}
	server.wg.Add(1)
	go server.acceptLoop()
	return server, nil
}

func (server *Server) Endpoint() string { return server.listener.Addr().String() }

// Close stops the listener and all active scripted sessions.
func (server *Server) Close() error {
	var closeErr error
	server.closeOnce.Do(func() {
		close(server.closed)
		closeErr = server.listener.Close()
		server.mu.Lock()
		for connection := range server.connections {
			_ = connection.Close()
		}
		server.mu.Unlock()
		server.wg.Wait()
	})
	if errors.Is(closeErr, net.ErrClosed) {
		return nil
	}
	return closeErr
}

// Err returns the first unexpected server-side protocol error.
func (server *Server) Err() error {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.firstError
}

// SetBytes initializes byte-addressed DB/I/Q/M memory.
func (server *Server) SetBytes(area byte, db uint16, offset uint32, value []byte) {
	server.mu.Lock()
	defer server.mu.Unlock()
	for index, current := range value {
		server.memory[memoryKey{area: area, db: db, offset: offset + uint32(index)}] = current
	}
}

// Bytes snapshots byte-addressed memory.
func (server *Server) Bytes(area byte, db uint16, offset uint32, length int) []byte {
	server.mu.Lock()
	defer server.mu.Unlock()
	result := make([]byte, length)
	for index := range result {
		result[index] = server.memory[memoryKey{area: area, db: db, offset: offset + uint32(index)}]
	}
	return result
}

func (server *Server) Requests() []Request {
	server.mu.Lock()
	defer server.mu.Unlock()
	return append([]Request(nil), server.requests...)
}

// DropNextWriteResponse applies the next write and then closes its connection
// without returning an AckData PDU.
func (server *Server) DropNextWriteResponse() {
	server.mu.Lock()
	server.dropNextWriteResponse = true
	server.mu.Unlock()
}

// DelayNextResponse delays the next data-plane response once.
func (server *Server) DelayNextResponse(delay time.Duration) {
	server.mu.Lock()
	server.delayNextResponse = delay
	server.mu.Unlock()
}

// RejectNextItem makes the first item of the next data-plane request return
// the supplied PLC return code.
func (server *Server) RejectNextItem(returnCode byte) {
	server.mu.Lock()
	server.rejectNextItem = returnCode
	server.rejectAfterRequests = 0
	server.mu.Unlock()
}

// RejectItemAfter rejects the first item after skipRequests additional
// data-plane requests have been accepted.
func (server *Server) RejectItemAfter(skipRequests int, returnCode byte) {
	server.mu.Lock()
	server.rejectAfterRequests = skipRequests
	server.rejectNextItem = returnCode
	server.mu.Unlock()
}

func (server *Server) acceptLoop() {
	defer server.wg.Done()
	for {
		connection, err := server.listener.Accept()
		if err != nil {
			select {
			case <-server.closed:
				return
			default:
				server.recordError(err)
				return
			}
		}
		server.mu.Lock()
		server.connections[connection] = struct{}{}
		server.mu.Unlock()
		server.wg.Add(1)
		go func() {
			defer server.wg.Done()
			defer connection.Close()
			defer func() {
				server.mu.Lock()
				delete(server.connections, connection)
				server.mu.Unlock()
			}()
			if err := server.handleConnection(connection); err != nil && !isExpectedClose(err) {
				server.recordError(err)
			}
		}()
	}
}

func (server *Server) handleConnection(connection net.Conn) error {
	request, err := isotcp.ReadFrame(connection, 65535)
	if err != nil {
		return err
	}
	if len(request) != 22 || request[5] != 0xe0 || request[14] != 0xc1 || request[18] != 0xc2 {
		return errors.New("testplc: malformed COTP connection request")
	}
	localTSAP := binary.BigEndian.Uint16(request[16:18])
	remoteTSAP := binary.BigEndian.Uint16(request[20:22])
	confirm := buildConnectionConfirm(localTSAP, remoteTSAP, request[13])
	if _, err := isotcp.WriteFull(connection, confirm); err != nil {
		return err
	}

	setupFrame, err := isotcp.ReadFrame(connection, 65535)
	if err != nil {
		return err
	}
	setupPayload, err := isotcp.UnwrapData(setupFrame)
	if err != nil {
		return err
	}
	reference, parameters, data, err := parseJob(setupPayload)
	if err != nil {
		return err
	}
	if len(parameters) != 8 || len(data) != 0 || parameters[0] != 0xf0 {
		return errors.New("testplc: malformed setup request")
	}
	requestedPDU := binary.BigEndian.Uint16(parameters[6:8])
	negotiated := requestedPDU
	if server.pdu < negotiated {
		negotiated = server.pdu
	}
	setupParameters := []byte{0xf0, 0x00, 0x00, 0x01, 0x00, 0x01, byte(negotiated >> 8), byte(negotiated)}
	if err := writeAck(connection, reference, setupParameters, nil, 0); err != nil {
		return err
	}

	for {
		frame, err := isotcp.ReadFrame(connection, 65535)
		if err != nil {
			return err
		}
		payload, err := isotcp.UnwrapData(frame)
		if err != nil {
			return err
		}
		reference, parameters, data, err := parseJob(payload)
		if err != nil {
			return err
		}
		if len(parameters) < 2 {
			return errors.New("testplc: short variable parameters")
		}
		function := parameters[0]
		itemCount := int(parameters[1])
		server.recordRequest(Request{Function: function, ItemCount: itemCount, PDUBytes: len(payload)})
		switch function {
		case 0x04:
			responseParameters, responseData, err := server.handleRead(parameters, data)
			if err != nil {
				return err
			}
			server.delayResponse()
			if err := writeAck(connection, reference, responseParameters, responseData, 0); err != nil {
				return err
			}
		case 0x05:
			responseParameters, responseData, err := server.handleWrite(parameters, data)
			if err != nil {
				return err
			}
			server.delayResponse()
			if server.consumeDropWriteResponse() {
				return nil
			}
			if err := writeAck(connection, reference, responseParameters, responseData, 0); err != nil {
				return err
			}
		default:
			return fmt.Errorf("testplc: unsupported function 0x%02x", function)
		}
	}
}

type variableItem struct {
	wordLength byte
	amount     uint16
	db         uint16
	area       byte
	address    uint32
	dataBytes  int
	transport  byte
	length     uint16
}

func parseVariableItems(parameters []byte) ([]variableItem, error) {
	if len(parameters) < 2 {
		return nil, errors.New("testplc: short variable parameters")
	}
	count := int(parameters[1])
	if count == 0 || len(parameters) != 2+12*count {
		return nil, errors.New("testplc: variable parameter length mismatch")
	}
	items := make([]variableItem, count)
	for index := range items {
		spec := parameters[2+12*index : 2+12*(index+1)]
		if spec[0] != 0x12 || spec[1] != 0x0a || spec[2] != 0x10 {
			return nil, errors.New("testplc: invalid variable specification")
		}
		item := variableItem{
			wordLength: spec[3],
			amount:     binary.BigEndian.Uint16(spec[4:6]),
			db:         binary.BigEndian.Uint16(spec[6:8]),
			area:       spec[8],
			address:    uint32(spec[9])<<16 | uint32(spec[10])<<8 | uint32(spec[11]),
		}
		width := 0
		switch item.wordLength {
		case 0x01:
			if item.amount != 1 {
				return nil, errors.New("testplc: only one bit is supported")
			}
			width, item.transport, item.length = 1, 0x03, 1
		case 0x02:
			width, item.transport = 1, 0x04
		case 0x04:
			width, item.transport = 2, 0x04
		case 0x06:
			width, item.transport = 4, 0x04
		case 0x08:
			width, item.transport = 4, 0x07
		case 0x1c, 0x1d:
			width, item.transport = 2, 0x09
		default:
			return nil, errors.New("testplc: unsupported word length")
		}
		item.dataBytes = int(item.amount) * width
		if item.wordLength != 0x01 {
			length := item.dataBytes
			if item.transport == 0x04 {
				length *= 8
			}
			if length > 65535 {
				return nil, errors.New("testplc: item data length overflow")
			}
			item.length = uint16(length)
		}
		items[index] = item
	}
	return items, nil
}

func (server *Server) handleRead(parameters, requestData []byte) ([]byte, []byte, error) {
	if len(requestData) != 0 {
		return nil, nil, errors.New("testplc: ReadVar request contains data")
	}
	items, err := parseVariableItems(parameters)
	if err != nil {
		return nil, nil, err
	}
	response := make([]byte, 0)
	for index, item := range items {
		returnCode := byte(0xff)
		if index == 0 {
			returnCode = server.consumeRejectItem(returnCode)
		}
		if returnCode != 0xff {
			// Some mainstream S7 stacks encode failed items with a zero
			// transport and a length field of four, but no payload.
			response = append(response, returnCode, 0, 0, 4)
			continue
		}
		value := server.readItem(item)
		response = append(response, 0xff, item.transport, byte(item.length>>8), byte(item.length))
		response = append(response, value...)
		if index != len(items)-1 && len(value)%2 != 0 {
			response = append(response, 0)
		}
	}
	return []byte{0x04, byte(len(items))}, response, nil
}

func (server *Server) handleWrite(parameters, requestData []byte) ([]byte, []byte, error) {
	items, err := parseVariableItems(parameters)
	if err != nil {
		return nil, nil, err
	}
	offset := 0
	returnCodes := make([]byte, len(items))
	for index, item := range items {
		if len(requestData)-offset < 4 {
			return nil, nil, errors.New("testplc: short write data header")
		}
		if requestData[offset] != 0 || requestData[offset+1] != item.transport ||
			binary.BigEndian.Uint16(requestData[offset+2:offset+4]) != item.length {
			return nil, nil, errors.New("testplc: write transport or length mismatch")
		}
		offset += 4
		if item.dataBytes > len(requestData)-offset {
			return nil, nil, errors.New("testplc: short write data")
		}
		value := requestData[offset : offset+item.dataBytes]
		offset += item.dataBytes
		returnCode := byte(0xff)
		if index == 0 {
			returnCode = server.consumeRejectItem(returnCode)
		}
		returnCodes[index] = returnCode
		if returnCode == 0xff {
			server.writeItem(item, value)
		}
		if index != len(items)-1 && item.dataBytes%2 != 0 {
			if offset >= len(requestData) || requestData[offset] != 0 {
				return nil, nil, errors.New("testplc: missing write padding")
			}
			offset++
		}
	}
	if offset != len(requestData) {
		return nil, nil, errors.New("testplc: trailing write data")
	}
	return []byte{0x05, byte(len(items))}, returnCodes, nil
}

func (server *Server) readItem(item variableItem) []byte {
	server.mu.Lock()
	defer server.mu.Unlock()
	if item.wordLength == 0x01 {
		byteOffset := item.address / 8
		bit := uint(item.address % 8)
		value := server.memory[memoryKey{area: item.area, db: item.db, offset: byteOffset}]
		if value&(1<<bit) != 0 {
			return []byte{1}
		}
		return []byte{0}
	}
	start := item.address / 8
	if item.wordLength == 0x1c || item.wordLength == 0x1d {
		start = item.address * 2
	}
	value := make([]byte, item.dataBytes)
	for index := range value {
		value[index] = server.memory[memoryKey{area: item.area, db: item.db, offset: start + uint32(index)}]
	}
	return value
}

func (server *Server) writeItem(item variableItem, value []byte) {
	server.mu.Lock()
	defer server.mu.Unlock()
	if item.wordLength == 0x01 {
		key := memoryKey{area: item.area, db: item.db, offset: item.address / 8}
		current := server.memory[key]
		mask := byte(1 << uint(item.address%8))
		if value[0] == 1 {
			current |= mask
		} else {
			current &^= mask
		}
		server.memory[key] = current
		return
	}
	start := item.address / 8
	if item.wordLength == 0x1c || item.wordLength == 0x1d {
		start = item.address * 2
	}
	for index, current := range value {
		server.memory[memoryKey{area: item.area, db: item.db, offset: start + uint32(index)}] = current
	}
}

func parseJob(payload []byte) (uint16, []byte, []byte, error) {
	if len(payload) < 10 || payload[0] != 0x32 || payload[1] != 0x01 || payload[2] != 0 || payload[3] != 0 {
		return 0, nil, nil, errors.New("testplc: malformed S7 job header")
	}
	reference := binary.BigEndian.Uint16(payload[4:6])
	parameterLength := int(binary.BigEndian.Uint16(payload[6:8]))
	dataLength := int(binary.BigEndian.Uint16(payload[8:10]))
	if reference == 0 || parameterLength > len(payload)-10 || dataLength != len(payload)-10-parameterLength {
		return 0, nil, nil, errors.New("testplc: S7 job length mismatch")
	}
	return reference, payload[10 : 10+parameterLength], payload[10+parameterLength:], nil
}

func writeAck(connection net.Conn, reference uint16, parameters, data []byte, globalCode uint16) error {
	payload := make([]byte, 12+len(parameters)+len(data))
	payload[0] = 0x32
	payload[1] = 0x03
	binary.BigEndian.PutUint16(payload[4:6], reference)
	binary.BigEndian.PutUint16(payload[6:8], uint16(len(parameters)))
	binary.BigEndian.PutUint16(payload[8:10], uint16(len(data)))
	payload[10] = byte(globalCode >> 8)
	payload[11] = byte(globalCode)
	copy(payload[12:], parameters)
	copy(payload[12+len(parameters):], data)
	frame, err := isotcp.WrapData(payload)
	if err != nil {
		return err
	}
	_, err = isotcp.WriteFull(connection, frame)
	return err
}

func buildConnectionConfirm(localTSAP, remoteTSAP uint16, tpduCode byte) []byte {
	frame := []byte{
		0x03, 0x00, 0x00, 0x16,
		0x11, 0xd0, 0x00, 0x01, 0x00, 0x01, 0x00,
		0xc0, 0x01, tpduCode,
		0xc1, 0x02, 0x00, 0x00,
		0xc2, 0x02, 0x00, 0x00,
	}
	binary.BigEndian.PutUint16(frame[16:18], remoteTSAP)
	binary.BigEndian.PutUint16(frame[20:22], localTSAP)
	return frame
}

func (server *Server) recordRequest(request Request) {
	server.mu.Lock()
	server.requests = append(server.requests, request)
	server.mu.Unlock()
}

func (server *Server) recordError(err error) {
	server.mu.Lock()
	if server.firstError == nil {
		server.firstError = err
	}
	server.mu.Unlock()
}

func (server *Server) consumeRejectItem(fallback byte) byte {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.rejectNextItem == 0 {
		return fallback
	}
	if server.rejectAfterRequests > 0 {
		server.rejectAfterRequests--
		return fallback
	}
	value := server.rejectNextItem
	server.rejectNextItem = 0
	return value
}

func (server *Server) consumeDropWriteResponse() bool {
	server.mu.Lock()
	defer server.mu.Unlock()
	value := server.dropNextWriteResponse
	server.dropNextWriteResponse = false
	return value
}

func (server *Server) delayResponse() {
	server.mu.Lock()
	delay := server.delayNextResponse
	server.delayNextResponse = 0
	server.mu.Unlock()
	if delay <= 0 {
		return
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-server.closed:
	}
}

func isExpectedClose(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed)
}
