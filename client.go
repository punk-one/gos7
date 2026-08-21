package gos7

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"sync"
	"time"

	"github.com/punk-one/gos7/internal/isotcp"
	"github.com/punk-one/gos7/internal/protocol"
)

const (
	maxAmQCaller = 1
	maxAmQCallee = 1
)

// Client owns exactly one physical S7 session. It is safe for concurrent
// callers; complete logical operations are serialized on that session.
type Client struct {
	config  normalizedConfig
	gate    chan struct{}
	lifeCtx context.Context
	cancel  context.CancelFunc

	mu                     sync.RWMutex
	state                  State
	conn                   net.Conn
	reference              uint16
	sessionGeneration      uint64
	limits                 SessionLimits
	limitsValid            bool
	localAddress           string
	remoteAddress          string
	lastActivity           time.Time
	lastSessionFailureKind ErrorKind
	closeOnce              sync.Once
}

// New validates configuration without opening a network connection.
func New(config Config) (*Client, error) {
	normalized, err := normalizeConfig(config)
	if err != nil {
		return nil, err
	}
	lifeCtx, cancel := context.WithCancel(context.Background())
	return &Client{
		config:  normalized,
		gate:    make(chan struct{}, 1),
		lifeCtx: lifeCtx,
		cancel:  cancel,
		state:   StateNew,
	}, nil
}

// Dial is equivalent to New followed by Connect.
func Dial(ctx context.Context, config Config) (*Client, error) {
	client, err := New(config)
	if err != nil {
		return nil, err
	}
	if err := client.Connect(ctx); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

// Connect establishes TCP, COTP, and S7 Setup Communication. It does not
// perform controller detection, SZL reads, reconnect, or retry.
func (client *Client) Connect(ctx context.Context) error {
	if client == nil {
		return invalidError("connect", "nil client")
	}
	if ctx == nil {
		return invalidError("connect", "nil context")
	}
	if err := client.acquire(ctx); err != nil {
		return err
	}
	defer client.release()

	state := client.State()
	if state == StateClosed || state == StateClosing {
		return newError("connect", ErrorClosed, SessionUnchanged, nil)
	}
	if state == StateReady {
		return nil
	}

	operationCtx, cleanup := client.operationContext(ctx, client.config.ConnectTimeout)
	defer cleanup()
	if err := operationCtx.Err(); err != nil {
		return client.classifyContextError("connect", operationCtx, SessionUnchanged, err)
	}

	client.mu.Lock()
	if client.state == StateClosed || client.state == StateClosing {
		client.mu.Unlock()
		return newError("connect", ErrorClosed, SessionUnchanged, nil)
	}
	client.state = StateConnecting
	client.mu.Unlock()

	dialer := net.Dialer{
		LocalAddr: client.config.localAddr,
		KeepAlive: client.config.KeepAlive,
	}
	connection, err := dialer.DialContext(operationCtx, "tcp", client.config.endpoint)
	if err != nil {
		failure := client.classifyContextError("connect.tcp", operationCtx, SessionUnchanged, err)
		client.failConnect(failure.Kind)
		return failure
	}
	if tcpConnection, ok := connection.(*net.TCPConn); ok {
		if err := tcpConnection.SetNoDelay(true); err != nil {
			_ = connection.Close()
			client.failConnect(ErrorTransport)
			return newError("connect.tcp_no_delay", ErrorTransport, SessionUnchanged, err)
		}
		if client.config.KeepAlive > 0 {
			if err := tcpConnection.SetKeepAlive(true); err != nil {
				_ = connection.Close()
				client.failConnect(ErrorTransport)
				return newError("connect.keepalive", ErrorTransport, SessionUnchanged, err)
			}
			if err := tcpConnection.SetKeepAlivePeriod(client.config.KeepAlive); err != nil {
				_ = connection.Close()
				client.failConnect(ErrorTransport)
				return newError("connect.keepalive_period", ErrorTransport, SessionUnchanged, err)
			}
		}
	}

	client.mu.Lock()
	if client.state == StateClosed || client.state == StateClosing {
		client.mu.Unlock()
		_ = connection.Close()
		return newError("connect", ErrorClosed, SessionUnchanged, nil)
	}
	client.conn = connection
	client.mu.Unlock()

	stopDeadline, err := armConnection(operationCtx, connection)
	if err != nil {
		client.failConnect(ErrorTransport)
		return newError("connect.deadline", ErrorTransport, SessionUnchanged, err)
	}
	defer stopDeadline()

	offeredTPDU, err := isotcp.TPDUSizeForS7PDU(int(client.config.RequestedPDU))
	if err != nil {
		client.failConnect(ErrorProtocol)
		return invariantError("connect.cotp_limits", err)
	}
	connectionRequest, err := isotcp.BuildConnectionRequest(client.config.localTSAP, client.config.remoteTSAP, offeredTPDU)
	if err != nil {
		client.failConnect(ErrorProtocol)
		return invariantError("connect.cotp_encode", err)
	}
	if _, err = isotcp.WriteFull(connection, connectionRequest); err != nil {
		failure := client.classifyContextError("connect.cotp_write", operationCtx, SessionUnchanged, err)
		client.failConnect(failure.Kind)
		return failure
	}
	connectionConfirm, err := isotcp.ReadFrame(connection, client.config.MaxFrameBytes)
	if err != nil {
		failure := client.classifyContextError("connect.cotp_read", operationCtx, SessionUnchanged, err)
		client.failConnect(failure.Kind)
		return failure
	}
	confirmResult, err := isotcp.ParseConnectionConfirm(connectionConfirm, client.config.localTSAP, client.config.remoteTSAP, offeredTPDU)
	if err != nil {
		client.failConnect(ErrorProtocol)
		return protocolError("connect.cotp_confirm", err)
	}
	tpduPayloadLimit := confirmResult.TPDUSize - isotcp.DataTPDUHeaderSize
	if tpduPayloadLimit < minRequestedPDU {
		client.failConnect(ErrorProtocol)
		return protocolError("connect.cotp_limits", errors.New("negotiated TPDU cannot carry the minimum S7 PDU"))
	}
	sessionRequestedPDU := smallerInt(int(client.config.RequestedPDU), tpduPayloadLimit)

	setupReference := uint16(1)
	setup, err := protocol.BuildSetupRequest(setupReference, maxAmQCaller, maxAmQCallee, uint16(sessionRequestedPDU))
	if err != nil {
		client.failConnect(ErrorProtocol)
		return invariantError("connect.setup_encode", err)
	}
	setupFrame, err := isotcp.WrapData(setup)
	if err != nil {
		client.failConnect(ErrorProtocol)
		return invariantError("connect.setup_frame", err)
	}
	if len(setupFrame)-isotcp.TPKTHeaderSize > confirmResult.TPDUSize {
		client.failConnect(ErrorProtocol)
		return invariantError("connect.setup_frame", errors.New("setup request exceeds negotiated TPDU"))
	}
	if _, err = isotcp.WriteFull(connection, setupFrame); err != nil {
		failure := client.classifyContextError("connect.setup_write", operationCtx, SessionUnchanged, err)
		client.failConnect(failure.Kind)
		return failure
	}
	responseFrame, err := isotcp.ReadFrame(connection, client.config.MaxFrameBytes)
	if err != nil {
		failure := client.classifyContextError("connect.setup_read", operationCtx, SessionUnchanged, err)
		client.failConnect(failure.Kind)
		return failure
	}
	if len(responseFrame)-isotcp.TPKTHeaderSize > confirmResult.TPDUSize {
		client.failConnect(ErrorProtocol)
		return protocolError("connect.setup_frame", errors.New("setup response exceeds negotiated TPDU"))
	}
	responsePayload, err := isotcp.UnwrapData(responseFrame)
	if err != nil {
		client.failConnect(ErrorProtocol)
		return protocolError("connect.setup_cotp", err)
	}
	if len(responsePayload) > sessionRequestedPDU {
		client.failConnect(ErrorProtocol)
		return protocolError("connect.setup_frame", errors.New("setup response exceeds requested S7 PDU"))
	}
	setupResult, err := protocol.ParseSetupResponse(responsePayload, setupReference)
	if err != nil {
		client.failConnect(ErrorProtocol)
		return protocolError("connect.setup_decode", err)
	}
	if setupResult.GlobalCode != 0 {
		client.failConnect(ErrorPLC)
		return plcError("connect.setup", setupResult.GlobalCode)
	}
	if err := operationCtx.Err(); err != nil {
		failure := client.classifyContextError("connect", operationCtx, SessionUnchanged, err)
		client.failConnect(failure.Kind)
		return failure
	}
	if setupResult.MaxAmQCaller == 0 || setupResult.MaxAmQCallee == 0 || setupResult.PDU == 0 ||
		setupResult.PDU < minRequestedPDU ||
		setupResult.MaxAmQCaller > maxAmQCaller || setupResult.MaxAmQCallee > maxAmQCallee ||
		int(setupResult.PDU) > sessionRequestedPDU ||
		int(setupResult.PDU)+isotcp.DataTPDUHeaderSize > confirmResult.TPDUSize ||
		int(setupResult.PDU)+isotcp.DataHeaderSize > client.config.MaxFrameBytes {
		client.failConnect(ErrorProtocol)
		return protocolError("connect.setup_limits", errors.New("invalid negotiated limits"))
	}

	localAddress := connection.LocalAddr().String()
	remoteAddress := connection.RemoteAddr().String()
	now := time.Now()
	client.mu.Lock()
	if client.state == StateClosed || client.state == StateClosing {
		client.mu.Unlock()
		_ = connection.Close()
		return newError("connect", ErrorClosed, SessionUnchanged, nil)
	}
	client.state = StateReady
	client.reference = setupReference
	client.sessionGeneration++
	client.limits = SessionLimits{
		RequestedPDU:           uint16(sessionRequestedPDU),
		NegotiatedPDU:          setupResult.PDU,
		NegotiatedTPDU:         uint16(confirmResult.TPDUSize),
		NegotiatedMaxAmQCaller: setupResult.MaxAmQCaller,
		NegotiatedMaxAmQCallee: setupResult.MaxAmQCallee,
	}
	client.limitsValid = true
	client.localAddress = localAddress
	client.remoteAddress = remoteAddress
	client.lastActivity = now
	client.lastSessionFailureKind = ""
	client.mu.Unlock()
	return nil
}

// Close is idempotent and terminal. It interrupts in-flight socket I/O and
// causes queued callers to return ErrClosed.
func (client *Client) Close() error {
	if client == nil {
		return nil
	}
	var closeErr error
	client.closeOnce.Do(func() {
		client.mu.Lock()
		client.state = StateClosing
		connection := client.conn
		client.conn = nil
		client.limitsValid = false
		client.mu.Unlock()

		if client.cancel != nil {
			client.cancel()
		}
		if connection != nil {
			closeErr = connection.Close()
		}

		client.mu.Lock()
		client.state = StateClosed
		client.mu.Unlock()
	})
	return closeErr
}

// State returns the local lifecycle state.
func (client *Client) State() State {
	if client == nil {
		return StateClosed
	}
	client.mu.RLock()
	defer client.mu.RUnlock()
	return client.state
}

// Limits returns the active session limits only while the client is Ready.
func (client *Client) Limits() (SessionLimits, bool) {
	if client == nil {
		return SessionLimits{}, false
	}
	client.mu.RLock()
	defer client.mu.RUnlock()
	if client.state != StateReady || !client.limitsValid {
		return SessionLimits{}, false
	}
	return client.limits, true
}

// Diagnostics returns a local snapshot and never performs network I/O.
func (client *Client) Diagnostics() Diagnostics {
	if client == nil {
		return Diagnostics{State: StateClosed}
	}
	client.mu.RLock()
	defer client.mu.RUnlock()
	return Diagnostics{
		State:                  client.state,
		SessionGeneration:      client.sessionGeneration,
		Limits:                 client.limits,
		LimitsValid:            client.state == StateReady && client.limitsValid,
		LocalAddress:           client.localAddress,
		RemoteAddress:          client.remoteAddress,
		LastActivity:           client.lastActivity,
		LastSessionFailureKind: client.lastSessionFailureKind,
	}
}

// Read validates and executes a logical batch. Results always correspond to
// input order. Per-item PLC errors are returned in ReadResult.Err.
func (client *Client) Read(ctx context.Context, input []ReadItem) ([]ReadResult, error) {
	if client == nil {
		return nil, invalidError("read", "nil client")
	}
	if ctx == nil {
		return nil, invalidError("read", "nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, client.classifyContextError("read", ctx, SessionUnchanged, err)
	}
	items, lengths, err := client.prepareReads(input)
	if err != nil {
		if len(input) > client.config.MaxItemsPerCall {
			return nil, err
		}
		return makeReadFailureResults(len(input), err), err
	}
	if err := client.acquire(ctx); err != nil {
		return makeReadFailureResults(len(items), err), err
	}
	defer client.release()
	operationCtx, cleanup := client.operationContext(ctx, 0)
	defer cleanup()
	results, _, err := client.readLocked(operationCtx, items, lengths, nil)
	return results, err
}

// Write validates and clones the complete logical batch before any I/O. A
// single item is never split across PDUs.
func (client *Client) Write(ctx context.Context, input []WriteItem) ([]WriteResult, error) {
	if client == nil {
		return nil, invalidError("write", "nil client")
	}
	if ctx == nil {
		return nil, invalidError("write", "nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, client.classifyContextError("write", ctx, SessionUnchanged, err)
	}
	items, err := client.prepareWrites(input)
	if err != nil {
		if len(input) > client.config.MaxItemsPerCall {
			return nil, err
		}
		return makeWriteFailureResults(len(input), err), err
	}
	if err := client.acquire(ctx); err != nil {
		return makeWriteFailureResults(len(items), err), err
	}
	defer client.release()
	operationCtx, cleanup := client.operationContext(ctx, 0)
	defer cleanup()
	return client.writeLocked(operationCtx, items, false)
}

// ReadArea reads a continuous byte range. n is the confirmed contiguous
// prefix copied into dst when an error interrupts a multi-PDU operation.
func (client *Client) ReadArea(ctx context.Context, area Area, dbNumber uint16, start uint32, dst []byte) (int, error) {
	if client == nil {
		return 0, invalidError("read_area", "nil client")
	}
	if ctx == nil {
		return 0, invalidError("read_area", "nil context")
	}
	if err := ctx.Err(); err != nil {
		return 0, client.classifyContextError("read_area", ctx, SessionUnchanged, err)
	}
	if len(dst) == 0 {
		return 0, invalidError("read_area", "destination must not be empty")
	}
	if len(dst) > client.config.MaxItemBytes || len(dst) > client.config.MaxBatchBytes || uint64(len(dst)) > math.MaxUint32 {
		return 0, limitError("read_area", "destination exceeds configured or wire limits")
	}
	item := ReadItem{
		Address:   Address{Area: area, DBNumber: dbNumber, Offset: start},
		Transport: TransportByte,
		Count:     uint32(len(dst)),
	}
	items, lengths, err := client.prepareReads([]ReadItem{item})
	if err != nil {
		return 0, err
	}
	if err := client.acquire(ctx); err != nil {
		return 0, err
	}
	defer client.release()
	operationCtx, cleanup := client.operationContext(ctx, 0)
	defer cleanup()
	results, progress, err := client.readLocked(operationCtx, items, lengths, [][]byte{dst})
	if err == nil && results[0].Err != nil {
		err = results[0].Err
	}
	return progress[0], err
}

// WriteArea explicitly permits a continuous byte range to be split across
// PDUs. n counts the contiguous prefix acknowledged by the PLC.
func (client *Client) WriteArea(ctx context.Context, area Area, dbNumber uint16, start uint32, src []byte) (int, error) {
	if client == nil {
		return 0, invalidError("write_area", "nil client")
	}
	if ctx == nil {
		return 0, invalidError("write_area", "nil context")
	}
	if err := ctx.Err(); err != nil {
		return 0, client.classifyContextError("write_area", ctx, SessionUnchanged, err)
	}
	if len(src) == 0 {
		return 0, invalidError("write_area", "source must not be empty")
	}
	if len(src) > client.config.MaxBatchBytes {
		return 0, limitError("write_area", "source exceeds max_batch_bytes")
	}
	if uint64(len(src)) > math.MaxUint32 {
		return 0, limitError("write_area", "source exceeds the public count field")
	}
	if _, err := validateReadItem(ReadItem{
		Address:   Address{Area: area, DBNumber: dbNumber, Offset: start},
		Transport: TransportByte,
		Count:     uint32(len(src)),
	}, client.config.MaxBatchBytes); err != nil {
		return 0, err
	}
	if err := client.acquire(ctx); err != nil {
		return 0, err
	}
	defer client.release()
	operationCtx, cleanup := client.operationContext(ctx, 0)
	defer cleanup()
	pdu, err := client.readyPDU("write_area")
	if err != nil {
		return 0, err
	}
	maxChunk := pdu - 28
	if maxChunk > math.MaxUint16/8 {
		maxChunk = math.MaxUint16 / 8
	}
	if maxChunk > client.config.MaxItemBytes {
		maxChunk = client.config.MaxItemBytes
	}
	if maxChunk < 1 {
		return 0, limitError("write_area", "negotiated PDU cannot carry a byte item")
	}
	itemCount := (len(src) + maxChunk - 1) / maxChunk
	if itemCount > client.config.MaxFragmentsPerCall {
		return 0, limitError("write_area", "chunk count exceeds max_fragments_per_call")
	}
	confirmed := 0
	for offset := 0; offset < len(src); {
		length := smallerInt(maxChunk, len(src)-offset)
		prepared, err := client.prepareWrites([]WriteItem{{
			Address:   Address{Area: area, DBNumber: dbNumber, Offset: start + uint32(offset)},
			Transport: TransportByte,
			Count:     uint32(length),
			Data:      src[offset : offset+length],
		}})
		if err != nil {
			return confirmed, err
		}
		results, fatalErr := client.writeLocked(operationCtx, prepared, true)
		if len(results) != 1 {
			return confirmed, invariantError("write_area", errors.New("single chunk returned an invalid result count"))
		}
		if results[0].Outcome != WriteAcknowledged {
			if fatalErr != nil {
				return confirmed, fatalErr
			}
			if results[0].Err != nil {
				return confirmed, results[0].Err
			}
			return confirmed, invariantError("write_area", errors.New("single chunk was not acknowledged"))
		}
		if fatalErr != nil {
			return confirmed, fatalErr
		}
		confirmed += length
		offset += length
	}
	return confirmed, nil
}

type readFragment struct {
	original int
	offset   int
	item     protocol.Item
}

type preparedWrite struct {
	original int
	item     protocol.WriteItem
}

func (client *Client) prepareReads(input []ReadItem) ([]ReadItem, []int, error) {
	if len(input) == 0 {
		return nil, nil, invalidError("read", "items must not be empty")
	}
	if len(input) > client.config.MaxItemsPerCall {
		return nil, nil, limitError("read", "item count exceeds max_items_per_call")
	}
	items := append([]ReadItem(nil), input...)
	lengths := make([]int, len(items))
	total := uint64(0)
	for index, item := range items {
		length, err := validateReadItem(item, client.config.MaxItemBytes)
		if err != nil {
			return nil, nil, fmt.Errorf("read item %d: %w", index, err)
		}
		lengths[index] = length
		total += uint64(length)
		if total > uint64(client.config.MaxBatchBytes) {
			return nil, nil, limitError("read", "batch exceeds max_batch_bytes")
		}
	}
	return items, lengths, nil
}

func (client *Client) prepareWrites(input []WriteItem) ([]WriteItem, error) {
	if len(input) == 0 {
		return nil, invalidError("write", "items must not be empty")
	}
	if len(input) > client.config.MaxItemsPerCall {
		return nil, limitError("write", "item count exceeds max_items_per_call")
	}
	total := uint64(0)
	for index, item := range input {
		length, err := validateWriteItem(item, client.config.MaxItemBytes)
		if err != nil {
			return nil, fmt.Errorf("write item %d: %w", index, err)
		}
		total += uint64(length)
		if total > uint64(client.config.MaxBatchBytes) {
			return nil, limitError("write", "batch exceeds max_batch_bytes")
		}
	}
	items := make([]WriteItem, len(input))
	for index, item := range input {
		items[index] = item
		items[index].Data = append([]byte(nil), item.Data...)
	}
	return items, nil
}

func (client *Client) readLocked(ctx context.Context, items []ReadItem, lengths []int, destinations [][]byte) ([]ReadResult, []int, error) {
	results := make([]ReadResult, len(items))
	progress := make([]int, len(items))
	pdu, err := client.readyPDU("read")
	if err != nil {
		markReadIncomplete(results, progress, lengths, err)
		return results, progress, err
	}
	fragmentCount, err := countReadFragments(items, lengths, pdu)
	if err != nil {
		markReadIncomplete(results, progress, lengths, err)
		return results, progress, err
	}
	if fragmentCount > client.config.MaxFragmentsPerCall {
		err = limitError("read", "fragment count exceeds max_fragments_per_call")
		markReadIncomplete(results, progress, lengths, err)
		return results, progress, err
	}
	if err := ctx.Err(); err != nil {
		failure := client.classifyContextError("read", ctx, SessionUnchanged, err)
		markReadIncomplete(results, progress, lengths, failure)
		return results, progress, failure
	}
	if destinations != nil && len(destinations) != len(items) {
		err = invariantError("read.destination", errors.New("destination count does not match item count"))
		markReadIncomplete(results, progress, lengths, err)
		return results, progress, err
	}
	for index, length := range lengths {
		if destinations == nil {
			results[index].Data = make([]byte, length)
			continue
		}
		if len(destinations[index]) != length {
			err = invariantError("read.destination", fmt.Errorf("destination %d length mismatch", index))
			markReadIncomplete(results, progress, lengths, err)
			return results, progress, err
		}
		results[index].Data = destinations[index]
	}
	iterator := readFragmentIterator{items: items, lengths: lengths, pdu: pdu}
	for {
		batch, ok, batchErr := nextReadBatch(&iterator, pdu, results)
		if batchErr != nil {
			markReadIncomplete(results, progress, lengths, batchErr)
			return results, progress, batchErr
		}
		if !ok {
			break
		}
		if err := ctx.Err(); err != nil {
			failure := client.classifyContextError("read", ctx, SessionUnchanged, err)
			markReadIncomplete(results, progress, lengths, failure)
			return results, progress, failure
		}
		wireItems := make([]protocol.Item, len(batch))
		for index := range batch {
			wireItems[index] = batch[index].item
		}
		reference := client.nextReference()
		request, err := protocol.BuildReadRequest(reference, wireItems)
		if err != nil {
			failure := invariantError("read.encode", err)
			markReadIncomplete(results, progress, lengths, failure)
			return results, progress, failure
		}
		payload, _, err := client.exchange(ctx, "read", request)
		if err != nil {
			markReadIncomplete(results, progress, lengths, err)
			return results, progress, err
		}
		wireResults, globalCode, err := protocol.ParseReadResponse(payload, reference, wireItems)
		if err != nil {
			client.breakSession(ErrorProtocol)
			failure := protocolError("read.decode", err)
			markReadIncomplete(results, progress, lengths, failure)
			return results, progress, failure
		}
		if globalCode != 0 {
			failure := plcError("read", globalCode)
			for _, fragment := range batch {
				if results[fragment.original].Err == nil {
					results[fragment.original].Err = failure
				}
			}
			markReadIncomplete(results, progress, lengths, failure)
			return results, progress, failure
		}
		for index, wireResult := range wireResults {
			fragment := batch[index]
			result := &results[fragment.original]
			if wireResult.ReturnCode != 0xff {
				if result.Err == nil {
					result.Err = plcItemError("read.item", wireResult.ReturnCode)
				}
				continue
			}
			if result.Err != nil || fragment.offset != progress[fragment.original] {
				continue
			}
			copy(result.Data[fragment.offset:], wireResult.Data)
			progress[fragment.original] += len(wireResult.Data)
		}
	}
	var incompleteErr error
	for index := range results {
		if results[index].Err != nil || progress[index] != lengths[index] {
			if results[index].Err == nil {
				if incompleteErr == nil {
					incompleteErr = invariantError("read", errors.New("incomplete logical item"))
				}
				results[index].Err = incompleteErr
			}
			results[index].Data = results[index].Data[:progress[index]]
		}
	}
	return results, progress, incompleteErr
}

func (client *Client) writeLocked(ctx context.Context, items []WriteItem, stopAfterFailure bool) ([]WriteResult, error) {
	results := make([]WriteResult, len(items))
	pdu, err := client.readyPDU("write")
	if err != nil {
		for index := range results {
			results[index].Err = err
		}
		return results, err
	}
	if err := ctx.Err(); err != nil {
		failure := client.classifyContextError("write", ctx, SessionUnchanged, err)
		for index := range results {
			results[index].Err = failure
		}
		return results, failure
	}
	prepared := make([]preparedWrite, len(items))
	for index, item := range items {
		wire, err := makeProtocolItem(item.Address, item.Transport, item.Count)
		if err != nil {
			failure := invariantError("write.item", err)
			for resultIndex := range results {
				results[resultIndex].Err = failure
			}
			return results, failure
		}
		prepared[index] = preparedWrite{
			original: index,
			item:     protocol.WriteItem{Item: wire, Data: item.Data},
		}
	}
	// Preflight every item against the active session before the first write.
	// Streaming batch planning must never discover a local size error only
	// after earlier items have already changed PLC memory.
	for _, item := range prepared {
		if protocol.WriteRequestSize([]protocol.WriteItem{item.item}) > pdu || protocol.WriteResponseSize(1) > pdu {
			failure := limitError("write", "one item exceeds negotiated PDU")
			for index := range results {
				results[index].Err = failure
			}
			return results, failure
		}
	}
	for next := 0; next < len(prepared); {
		batch, following, batchErr := nextWriteBatch(prepared, next, pdu, stopAfterFailure)
		if batchErr != nil {
			for index := range results {
				if results[index].Err == nil {
					results[index].Err = batchErr
				}
			}
			return results, batchErr
		}
		next = following
		if err := ctx.Err(); err != nil {
			failure := client.classifyContextError("write", ctx, SessionUnchanged, err)
			markUnattemptedWrites(results, failure)
			return results, failure
		}
		wireItems := make([]protocol.WriteItem, len(batch))
		for index := range batch {
			wireItems[index] = batch[index].item
		}
		reference := client.nextReference()
		request, err := protocol.BuildWriteRequest(reference, wireItems)
		if err != nil {
			failure := invariantError("write.encode", err)
			for _, item := range batch {
				results[item.original].Err = failure
			}
			markUnattemptedWrites(results, failure)
			return results, failure
		}
		payload, sent, err := client.exchange(ctx, "write", request)
		if err != nil {
			for _, item := range batch {
				result := &results[item.original]
				if sent {
					result.Outcome = WriteUnknown
				} else {
					result.Outcome = WriteNotAttempted
				}
				result.Err = err
			}
			markUnattemptedWrites(results, err)
			return results, err
		}
		returnCodes, globalCode, err := protocol.ParseWriteResponse(payload, reference, len(batch))
		if err != nil {
			client.breakSession(ErrorProtocol)
			failure := protocolError("write.decode", err)
			for _, item := range batch {
				results[item.original] = WriteResult{Outcome: WriteUnknown, Err: failure}
			}
			markUnattemptedWrites(results, failure)
			return results, failure
		}
		if globalCode != 0 {
			failure := plcError("write", globalCode)
			for _, item := range batch {
				results[item.original] = WriteResult{Outcome: WriteRejected, Err: failure}
			}
			markUnattemptedWrites(results, failure)
			return results, failure
		}
		batchFailed := false
		for index, returnCode := range returnCodes {
			original := batch[index].original
			if returnCode == 0xff {
				results[original] = WriteResult{Outcome: WriteAcknowledged}
			} else {
				batchFailed = true
				results[original] = WriteResult{
					Outcome: WriteRejected,
					Err:     plcItemError("write.item", returnCode),
				}
			}
		}
		if stopAfterFailure && batchFailed {
			failure := firstWriteError(results)
			markUnattemptedWrites(results, failure)
			return results, failure
		}
	}
	return results, nil
}

func maxReadFragmentData(transport TransportType, pdu int) (int, error) {
	if pdu < protocol.ReadRequestSize(1) || pdu < protocol.ReadResponseSize([]protocol.Item{{DataBytes: 1}}) {
		return 0, limitError("read", "negotiated PDU is too small")
	}
	layout, ok := layoutFor(transport)
	if !ok {
		return 0, invariantError("read.fragment", errors.New("unsupported transport"))
	}
	maxData := pdu - 18
	if layout.lengthInBits && maxData > math.MaxUint16/8 {
		maxData = math.MaxUint16 / 8
	}
	if !layout.lengthInBits && maxData > math.MaxUint16 {
		maxData = math.MaxUint16
	}
	maxData -= maxData % int(layout.width)
	if maxData < int(layout.width) {
		return 0, limitError("read", "negotiated PDU cannot carry an item element")
	}
	if transport == TransportBit {
		return 1, nil
	}
	return maxData, nil
}

func countReadFragments(items []ReadItem, lengths []int, pdu int) (int, error) {
	if len(items) != len(lengths) {
		return 0, invariantError("read.fragment", errors.New("item and length counts differ"))
	}
	total := uint64(0)
	for index, item := range items {
		maxData, err := maxReadFragmentData(item.Transport, pdu)
		if err != nil {
			return 0, err
		}
		total += uint64((lengths[index] + maxData - 1) / maxData)
		if total > uint64(math.MaxInt) {
			return 0, limitError("read", "fragment count overflows int")
		}
	}
	return int(total), nil
}

type readFragmentIterator struct {
	items      []ReadItem
	lengths    []int
	pdu        int
	original   int
	offset     int
	pending    readFragment
	hasPending bool
}

func (iterator *readFragmentIterator) next() (readFragment, bool, error) {
	if iterator.hasPending {
		iterator.hasPending = false
		return iterator.pending, true, nil
	}
	for iterator.original < len(iterator.items) {
		if iterator.offset >= iterator.lengths[iterator.original] {
			iterator.original++
			iterator.offset = 0
			continue
		}
		item := iterator.items[iterator.original]
		layout, ok := layoutFor(item.Transport)
		if !ok {
			return readFragment{}, false, invariantError("read.fragment", errors.New("unsupported transport"))
		}
		maxData, err := maxReadFragmentData(item.Transport, iterator.pdu)
		if err != nil {
			return readFragment{}, false, err
		}
		length := smallerInt(maxData, iterator.lengths[iterator.original]-iterator.offset)
		count := uint32(length) / layout.width
		fragmentAddress := item.Address
		if item.Address.Area == AreaTimer || item.Address.Area == AreaCounter {
			fragmentAddress.Offset += uint32(iterator.offset) / layout.width
		} else if item.Transport != TransportBit {
			fragmentAddress.Offset += uint32(iterator.offset)
		}
		wire, err := makeProtocolItem(fragmentAddress, item.Transport, count)
		if err != nil {
			return readFragment{}, false, invariantError("read.fragment", err)
		}
		fragment := readFragment{original: iterator.original, offset: iterator.offset, item: wire}
		iterator.offset += length
		return fragment, true, nil
	}
	return readFragment{}, false, nil
}

func (iterator *readFragmentIterator) putBack(fragment readFragment) {
	iterator.pending = fragment
	iterator.hasPending = true
}

func nextReadBatch(iterator *readFragmentIterator, pdu int, results []ReadResult) ([]readFragment, bool, error) {
	current := make([]readFragment, 0, protocol.CompatibilityMaxItemsPerPDU)
	requestSize := 12
	responseSize := 14
	for {
		fragment, ok, err := iterator.next()
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return current, len(current) > 0, nil
		}
		if results[fragment.original].Err != nil {
			continue
		}
		candidateRequest := requestSize + 12
		candidateResponse := responseSize + 4 + fragment.item.DataBytes
		if len(current) > 0 && current[len(current)-1].item.DataBytes%2 != 0 {
			candidateResponse++
		}
		if len(current) == protocol.CompatibilityMaxItemsPerPDU || candidateRequest > pdu || candidateResponse > pdu {
			if len(current) == 0 {
				return nil, false, limitError("read", "one fragment exceeds negotiated PDU")
			}
			iterator.putBack(fragment)
			return current, true, nil
		}
		current = append(current, fragment)
		requestSize = candidateRequest
		responseSize = candidateResponse
	}
}

func nextWriteBatch(items []preparedWrite, start, pdu int, single bool) ([]preparedWrite, int, error) {
	if start < 0 || start >= len(items) {
		return nil, start, invariantError("write.plan", errors.New("batch start is outside item range"))
	}
	requestSize := 12
	responseSize := 14
	end := start
	for end < len(items) {
		item := items[end]
		candidateRequest := requestSize + 12 + 4 + len(item.item.Data)
		if end > start && len(items[end-1].item.Data)%2 != 0 {
			candidateRequest++
		}
		candidateResponse := responseSize + 1
		if end-start == protocol.CompatibilityMaxItemsPerPDU || candidateRequest > pdu || candidateResponse > pdu {
			if end == start {
				return nil, start, limitError("write", "one item exceeds negotiated PDU")
			}
			break
		}
		requestSize = candidateRequest
		responseSize = candidateResponse
		end++
		if single {
			break
		}
	}
	return items[start:end], end, nil
}

func makeProtocolItem(address Address, transport TransportType, count uint32) (protocol.Item, error) {
	if count == 0 || count > math.MaxUint16 {
		return protocol.Item{}, fmt.Errorf("count exceeds one wire descriptor")
	}
	layout, ok := layoutFor(transport)
	if !ok {
		return protocol.Item{}, fmt.Errorf("unsupported transport")
	}
	dataBytes64 := uint64(count) * uint64(layout.width)
	if dataBytes64 > math.MaxInt {
		return protocol.Item{}, fmt.Errorf("data length overflow")
	}
	area, ok := wireArea(address.Area)
	if !ok {
		return protocol.Item{}, fmt.Errorf("unsupported area")
	}
	wireAddress := address.Offset
	if address.Area != AreaTimer && address.Area != AreaCounter {
		wireAddress = address.Offset*8 + uint32(address.Bit)
	}
	return protocol.Item{
		WordLength:        layout.wordLength,
		ResponseTransport: layout.responseTransport,
		Amount:            uint16(count),
		DBNumber:          address.DBNumber,
		Area:              area,
		Address:           wireAddress,
		DataBytes:         int(dataBytes64),
		LengthInBits:      layout.lengthInBits,
	}, nil
}

func wireArea(area Area) (byte, bool) {
	switch area {
	case AreaInput:
		return 0x81, true
	case AreaOutput:
		return 0x82, true
	case AreaMarker:
		return 0x83, true
	case AreaDB:
		return 0x84, true
	case AreaCounter:
		return 0x1c, true
	case AreaTimer:
		return 0x1d, true
	default:
		return 0, false
	}
}

func (client *Client) exchange(ctx context.Context, operation string, request []byte) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, client.classifyContextError(operation, ctx, SessionUnchanged, err)
	}
	exchangeCtx := ctx
	cancelExchange := func() {}
	if client.config.ExchangeTimeout > 0 {
		exchangeCtx, cancelExchange = context.WithTimeout(ctx, client.config.ExchangeTimeout)
	}
	defer cancelExchange()
	client.mu.RLock()
	connection := client.conn
	state := client.state
	limits := client.limits
	limitsValid := client.limitsValid
	client.mu.RUnlock()
	if state != StateReady || connection == nil || !limitsValid {
		return nil, false, client.stateError(operation, state)
	}
	if len(request) > int(limits.NegotiatedPDU) {
		return nil, false, invariantError(operation+".pdu", errors.New("request exceeds negotiated S7 PDU"))
	}
	frame, err := isotcp.WrapData(request)
	if err != nil {
		return nil, false, invariantError(operation+".frame", err)
	}
	if len(frame)-isotcp.TPKTHeaderSize > int(limits.NegotiatedTPDU) {
		return nil, false, invariantError(operation+".frame", errors.New("request exceeds negotiated TPDU"))
	}
	stopDeadline, err := armConnection(exchangeCtx, connection)
	if err != nil {
		client.breakSession(ErrorTransport)
		return nil, false, newError(operation+".deadline", ErrorTransport, SessionBroken, err)
	}
	defer stopDeadline()
	written, err := isotcp.WriteFull(connection, frame)
	if err != nil {
		failure := client.classifyContextError(operation+".write", exchangeCtx, SessionBroken, err)
		client.breakSession(failure.Kind)
		return nil, written > 0, failure
	}
	responseFrame, err := isotcp.ReadFrame(connection, client.config.MaxFrameBytes)
	if err != nil {
		failure := client.classifyContextError(operation+".read", exchangeCtx, SessionBroken, err)
		client.breakSession(failure.Kind)
		return nil, true, failure
	}
	if len(responseFrame)-isotcp.TPKTHeaderSize > int(limits.NegotiatedTPDU) {
		client.breakSession(ErrorProtocol)
		return nil, true, protocolError(operation+".frame", errors.New("response exceeds negotiated TPDU"))
	}
	payload, err := isotcp.UnwrapData(responseFrame)
	if err != nil {
		client.breakSession(ErrorProtocol)
		return nil, true, protocolError(operation+".cotp", err)
	}
	if len(payload) > int(limits.NegotiatedPDU) {
		client.breakSession(ErrorProtocol)
		return nil, true, protocolError(operation+".pdu", errors.New("response exceeds negotiated S7 PDU"))
	}
	client.mu.Lock()
	client.lastActivity = time.Now()
	client.mu.Unlock()
	return payload, true, nil
}

func (client *Client) readyPDU(operation string) (int, error) {
	client.mu.RLock()
	defer client.mu.RUnlock()
	if client.state != StateReady || client.conn == nil || !client.limitsValid {
		return 0, client.stateError(operation, client.state)
	}
	return int(client.limits.NegotiatedPDU), nil
}

func (client *Client) nextReference() uint16 {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.reference++
	if client.reference == 0 {
		client.reference = 1
	}
	return client.reference
}

func (client *Client) acquire(ctx context.Context) error {
	if client.gate == nil || client.lifeCtx == nil {
		return invalidError("session.queue", "client must be created with New")
	}
	select {
	case <-ctx.Done():
		return client.classifyContextError("session.queue", ctx, SessionUnchanged, ctx.Err())
	case <-client.lifeCtx.Done():
		return newError("session.queue", ErrorClosed, SessionUnchanged, nil)
	case client.gate <- struct{}{}:
		select {
		case <-client.lifeCtx.Done():
			client.release()
			return newError("session.queue", ErrorClosed, SessionUnchanged, nil)
		default:
			return nil
		}
	}
}

func (client *Client) release() {
	<-client.gate
}

func (client *Client) operationContext(parent context.Context, timeout time.Duration) (context.Context, func()) {
	base, cancelBase := contextWithShutdown(parent, client.lifeCtx)
	ctx := base
	cancelTimeout := func() {}
	if timeout > 0 {
		ctx, cancelTimeout = context.WithTimeout(base, timeout)
	}
	return ctx, func() {
		cancelTimeout()
		cancelBase()
	}
}

// contextWithShutdown derives from parent and is also canceled by shutdown.
// The watcher always exits when the returned cancel function is called.
func contextWithShutdown(parent, shutdown context.Context) (context.Context, context.CancelFunc) {
	if shutdown == nil {
		return context.WithCancel(parent)
	}
	ctx, cancel := context.WithCancel(parent)
	go func() {
		select {
		case <-shutdown.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

func armConnection(ctx context.Context, connection net.Conn) (func(), error) {
	deadline := time.Time{}
	if value, ok := ctx.Deadline(); ok {
		deadline = value
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return nil, err
	}
	stop := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			_ = connection.SetDeadline(time.Now())
		case <-stop:
		}
	}()
	return func() {
		close(stop)
		<-stopped
		_ = connection.SetDeadline(time.Time{})
	}, nil
}

func (client *Client) classifyContextError(operation string, ctx context.Context, impact SessionImpact, cause error) *Error {
	if client.State() == StateClosed || client.lifeCtx != nil && errors.Is(client.lifeCtx.Err(), context.Canceled) {
		return newError(operation, ErrorClosed, impact, cause)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(cause, context.DeadlineExceeded) {
		return &Error{Op: operation, Kind: ErrorTimeout, Temporary: true, Impact: impact, Cause: cause}
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(cause, context.Canceled) {
		return newError(operation, ErrorCanceled, impact, cause)
	}
	var networkError net.Error
	if errors.As(cause, &networkError) && networkError.Timeout() {
		return &Error{Op: operation, Kind: ErrorTimeout, Temporary: true, Impact: impact, Cause: cause}
	}
	return &Error{Op: operation, Kind: ErrorTransport, Temporary: true, Impact: impact, Cause: cause}
}

func (client *Client) stateError(operation string, state State) *Error {
	if state == StateClosed || state == StateClosing {
		return newError(operation, ErrorClosed, SessionUnchanged, nil)
	}
	return newError(operation, ErrorNotConnected, SessionUnchanged, nil)
}

func (client *Client) failConnect(kind ErrorKind) {
	client.mu.Lock()
	connection := client.conn
	client.conn = nil
	client.limitsValid = false
	if client.state != StateClosed && client.state != StateClosing {
		client.lastSessionFailureKind = kind
		client.state = StateDisconnected
	}
	client.mu.Unlock()
	if connection != nil {
		_ = connection.Close()
	}
}

func (client *Client) breakSession(kind ErrorKind) {
	client.mu.Lock()
	connection := client.conn
	client.conn = nil
	client.limitsValid = false
	if client.state != StateClosed && client.state != StateClosing {
		client.lastSessionFailureKind = kind
		client.state = StateBroken
	}
	client.mu.Unlock()
	if connection != nil {
		_ = connection.Close()
	}
}

func smallerInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func makeReadFailureResults(count int, err error) []ReadResult {
	results := make([]ReadResult, count)
	for index := range results {
		results[index].Err = err
	}
	return results
}

func markReadIncomplete(results []ReadResult, progress, lengths []int, err error) {
	for index := range results {
		if results[index].Err == nil && index < len(progress) && index < len(lengths) && progress[index] < lengths[index] {
			results[index].Err = err
		}
		if index < len(progress) && progress[index] <= len(results[index].Data) {
			results[index].Data = results[index].Data[:progress[index]]
		}
	}
}

func makeWriteFailureResults(count int, err error) []WriteResult {
	results := make([]WriteResult, count)
	for index := range results {
		results[index] = WriteResult{Outcome: WriteNotAttempted, Err: err}
	}
	return results
}

func markUnattemptedWrites(results []WriteResult, err error) {
	for index := range results {
		if results[index].Outcome == WriteNotAttempted && results[index].Err == nil {
			results[index].Err = err
		}
	}
}

func firstWriteError(results []WriteResult) error {
	for _, result := range results {
		if result.Err != nil {
			return result.Err
		}
	}
	return newError("write_area", ErrorPLC, SessionUnchanged, nil)
}
