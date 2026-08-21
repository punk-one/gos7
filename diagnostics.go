package gos7

import "time"

// State is the local session lifecycle state. Ready does not promise that the
// remote PLC is currently reachable; the next bounded operation confirms it.
type State uint8

const (
	StateNew State = iota
	StateConnecting
	StateReady
	StateBroken
	StateDisconnected
	StateClosing
	StateClosed
)

func (state State) String() string {
	switch state {
	case StateNew:
		return "new"
	case StateConnecting:
		return "connecting"
	case StateReady:
		return "ready"
	case StateBroken:
		return "broken"
	case StateDisconnected:
		return "disconnected"
	case StateClosing:
		return "closing"
	case StateClosed:
		return "closed"
	default:
		return "unknown"
	}
}

// SessionLimits is the immutable limit snapshot negotiated for one session.
// RequestedPDU is the actual Setup Communication proposal after applying the
// negotiated TPDU limit, and can therefore be lower than Config.RequestedPDU.
type SessionLimits struct {
	RequestedPDU           uint16
	NegotiatedPDU          uint16
	NegotiatedTPDU         uint16
	NegotiatedMaxAmQCaller uint16
	NegotiatedMaxAmQCallee uint16
}

// Diagnostics is a local snapshot and never performs network I/O.
type Diagnostics struct {
	State                  State
	SessionGeneration      uint64
	Limits                 SessionLimits
	LimitsValid            bool
	LocalAddress           string
	RemoteAddress          string
	LastActivity           time.Time
	LastSessionFailureKind ErrorKind
}
