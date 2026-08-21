package gos7

import (
	"errors"
	"fmt"
	"strconv"
)

// ErrorKind is a stable, machine-readable error category.
type ErrorKind string

const (
	ErrorInvalidArgument ErrorKind = "invalid_argument"
	ErrorNotConnected    ErrorKind = "not_connected"
	ErrorTimeout         ErrorKind = "timeout"
	ErrorCanceled        ErrorKind = "canceled"
	ErrorTransport       ErrorKind = "transport"
	ErrorProtocol        ErrorKind = "protocol"
	ErrorPLC             ErrorKind = "plc"
	ErrorLimit           ErrorKind = "limit"
	ErrorClosed          ErrorKind = "closed"
)

type kindSentinel ErrorKind

func (e kindSentinel) Error() string { return string(e) }

var (
	ErrInvalidArgument = kindSentinel(ErrorInvalidArgument)
	ErrNotConnected    = kindSentinel(ErrorNotConnected)
	ErrTimeout         = kindSentinel(ErrorTimeout)
	ErrCanceled        = kindSentinel(ErrorCanceled)
	ErrTransport       = kindSentinel(ErrorTransport)
	ErrProtocol        = kindSentinel(ErrorProtocol)
	ErrPLC             = kindSentinel(ErrorPLC)
	ErrLimit           = kindSentinel(ErrorLimit)
	ErrClosed          = kindSentinel(ErrorClosed)
)

// SessionImpact tells a caller whether the current session can still be used.
type SessionImpact uint8

const (
	SessionUnchanged SessionImpact = iota
	SessionBroken
)

// Error contains protocol-safe error details. It never contains PLC payload.
type Error struct {
	Op         string
	Kind       ErrorKind
	Code       uint32
	ReturnCode byte
	Temporary  bool
	Impact     SessionImpact
	Cause      error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	text := e.Op + ": " + string(e.Kind)
	if e.Code != 0 {
		text += " code=" + strconv.FormatUint(uint64(e.Code), 10)
	}
	if e.ReturnCode != 0 {
		text += fmt.Sprintf(" return_code=0x%02x", e.ReturnCode)
	}
	if e.Cause != nil {
		cause := e.Cause.Error()
		if len(cause) > 256 {
			cause = cause[:256] + "..."
		}
		text += ": " + cause
	}
	return text
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *Error) Is(target error) bool {
	if e == nil || target == nil {
		return false
	}
	if sentinel, ok := target.(kindSentinel); ok {
		return e.Kind == ErrorKind(sentinel)
	}
	var other *Error
	return errors.As(target, &other) && other != nil && e.Kind == other.Kind
}

func newError(op string, kind ErrorKind, impact SessionImpact, cause error) *Error {
	return &Error{Op: op, Kind: kind, Impact: impact, Cause: cause}
}

func invalidError(op, message string) *Error {
	return newError(op, ErrorInvalidArgument, SessionUnchanged, errors.New(message))
}

func limitError(op, message string) *Error {
	return newError(op, ErrorLimit, SessionUnchanged, errors.New(message))
}

func protocolError(op string, cause error) *Error {
	return newError(op, ErrorProtocol, SessionBroken, cause)
}

// invariantError reports a local encode/planner invariant failure. Unlike a
// malformed peer response, it does not invalidate an otherwise usable session.
func invariantError(op string, cause error) *Error {
	return newError(op, ErrorProtocol, SessionUnchanged, cause)
}

func plcError(op string, code uint16) *Error {
	return &Error{
		Op:     op,
		Kind:   ErrorPLC,
		Code:   uint32(code),
		Impact: SessionUnchanged,
	}
}

func plcItemError(op string, returnCode byte) *Error {
	return &Error{
		Op:         op,
		Kind:       ErrorPLC,
		Code:       uint32(returnCode),
		ReturnCode: returnCode,
		Impact:     SessionUnchanged,
	}
}
