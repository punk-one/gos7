package gos7

import (
	"errors"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultPort                = 102
	DefaultRequestedPDU        = 480
	DefaultMaxFrameBytes       = 4096
	DefaultMaxItemsPerCall     = 4096
	DefaultMaxFragmentsPerCall = 65536
	DefaultMaxItemBytes        = 1 << 20
	DefaultMaxBatchBytes       = 32 << 20
	DefaultConnectTimeout      = 10 * time.Second
	DefaultExchangeTimeout     = 10 * time.Second
	DefaultKeepAlive           = 30 * time.Second

	minRequestedPDU = 240
	maxRequestedPDU = 960
	maxTPKTFrame    = 65535
)

// AddressingMode chooses exactly one COTP TSAP addressing scheme.
type AddressingMode uint8

const (
	AddressByRackSlot AddressingMode = iota + 1
	AddressByTSAP
)

// ConnectionRole is the classic rack/slot TSAP connection-resource role. It
// is not a controller type or an authorization level.
type ConnectionRole uint8

const (
	ConnectionPG    ConnectionRole = 1
	ConnectionOP    ConnectionRole = 2
	ConnectionBasic ConnectionRole = 3
)

// Addressing configures either rack/slot addressing or explicit TSAP values.
type Addressing struct {
	Mode       AddressingMode
	Rack       uint8
	Slot       uint8
	Role       ConnectionRole
	LocalTSAP  uint16
	RemoteTSAP uint16
}

// Config contains connection, timeout, and allocation limits. Runtime policy
// such as retry, reconnect, pooling, polling, and idle close is intentionally
// absent.
type Config struct {
	Endpoint     string
	Addressing   Addressing
	LocalAddress string
	LocalPort    uint16

	// RequestedPDU is the S7 Setup Communication proposal in the supported
	// 240..960 range. The TPDU negotiation may reduce the actual proposal.
	RequestedPDU  uint16
	MaxFrameBytes int

	// MaxItemsPerCall limits logical API input; each wire PDU is independently
	// capped at the 20-item compatibility limit.
	MaxItemsPerCall int
	// MaxFragmentsPerCall bounds large-read and WriteArea expansion.
	MaxFragmentsPerCall int
	MaxItemBytes        int
	MaxBatchBytes       int

	ConnectTimeout time.Duration
	// ExchangeTimeout applies to one request/response exchange. The context
	// passed to Read, Write, ReadArea, or WriteArea bounds the whole operation.
	ExchangeTimeout time.Duration
	KeepAlive       time.Duration
}

type normalizedConfig struct {
	Config
	endpoint   string
	localAddr  *net.TCPAddr
	localTSAP  uint16
	remoteTSAP uint16
}

func normalizeConfig(input Config) (normalizedConfig, error) {
	config := input
	endpoint, err := normalizeEndpoint(config.Endpoint)
	if err != nil {
		return normalizedConfig{}, invalidError("config.endpoint", err.Error())
	}
	if config.RequestedPDU == 0 {
		config.RequestedPDU = DefaultRequestedPDU
	}
	if config.MaxFrameBytes == 0 {
		config.MaxFrameBytes = DefaultMaxFrameBytes
	}
	if config.MaxItemsPerCall == 0 {
		config.MaxItemsPerCall = DefaultMaxItemsPerCall
	}
	if config.MaxFragmentsPerCall == 0 {
		config.MaxFragmentsPerCall = DefaultMaxFragmentsPerCall
	}
	if config.MaxItemBytes == 0 {
		config.MaxItemBytes = DefaultMaxItemBytes
	}
	if config.MaxBatchBytes == 0 {
		config.MaxBatchBytes = DefaultMaxBatchBytes
	}
	if config.ConnectTimeout == 0 {
		config.ConnectTimeout = DefaultConnectTimeout
	}
	if config.ExchangeTimeout == 0 {
		config.ExchangeTimeout = DefaultExchangeTimeout
	}
	if config.KeepAlive == 0 {
		config.KeepAlive = DefaultKeepAlive
	}

	if config.MaxFrameBytes < 32 || config.MaxFrameBytes > maxTPKTFrame {
		return normalizedConfig{}, invalidError("config.max_frame_bytes", "must be between 32 and 65535")
	}
	if config.RequestedPDU < minRequestedPDU || config.RequestedPDU > maxRequestedPDU ||
		int(config.RequestedPDU)+7 > config.MaxFrameBytes {
		return normalizedConfig{}, invalidError("config.requested_pdu", "must be between 240 and 960 and fit the configured frame limit")
	}
	if config.MaxItemsPerCall < 1 || config.MaxItemsPerCall > 1<<20 {
		return normalizedConfig{}, invalidError("config.max_items_per_call", "must be between 1 and 1048576")
	}
	if config.MaxFragmentsPerCall < 1 || config.MaxFragmentsPerCall > 1<<20 {
		return normalizedConfig{}, invalidError("config.max_fragments_per_call", "must be between 1 and 1048576")
	}
	if config.MaxItemBytes < 1 || config.MaxItemBytes > 1<<30 {
		return normalizedConfig{}, invalidError("config.max_item_bytes", "must be between 1 and 1073741824")
	}
	if config.MaxBatchBytes < config.MaxItemBytes || config.MaxBatchBytes > (1<<31)-1 {
		return normalizedConfig{}, invalidError("config.max_batch_bytes", "must be at least max_item_bytes and no more than 2147483647")
	}
	if config.ConnectTimeout < 0 || config.ExchangeTimeout < 0 || config.KeepAlive < 0 {
		return normalizedConfig{}, invalidError("config.timeout", "durations must not be negative")
	}

	localTSAP, remoteTSAP, err := validateAddressing(config.Addressing)
	if err != nil {
		return normalizedConfig{}, err
	}
	localAddr, err := normalizeLocalAddress(config.LocalAddress, config.LocalPort)
	if err != nil {
		return normalizedConfig{}, invalidError("config.local_address", err.Error())
	}

	return normalizedConfig{
		Config:     config,
		endpoint:   endpoint,
		localAddr:  localAddr,
		localTSAP:  localTSAP,
		remoteTSAP: remoteTSAP,
	}, nil
}

func validateAddressing(addressing Addressing) (uint16, uint16, error) {
	switch addressing.Mode {
	case AddressByRackSlot:
		if addressing.LocalTSAP != 0 || addressing.RemoteTSAP != 0 {
			return 0, 0, invalidError("config.addressing", "rack/slot mode cannot include explicit TSAP values")
		}
		if addressing.Rack > 7 {
			return 0, 0, invalidError("config.addressing.rack", "rack must be between 0 and 7")
		}
		if addressing.Slot > 31 {
			return 0, 0, invalidError("config.addressing.slot", "slot must be between 0 and 31")
		}
		if addressing.Role < ConnectionPG || addressing.Role > ConnectionBasic {
			return 0, 0, invalidError("config.addressing.role", "role must be PG, OP, or Basic")
		}
		remote := uint16(addressing.Role)<<8 | uint16(addressing.Rack)<<5 | uint16(addressing.Slot)
		return 0x0100, remote, nil
	case AddressByTSAP:
		if addressing.Rack != 0 || addressing.Slot != 0 || addressing.Role != 0 {
			return 0, 0, invalidError("config.addressing", "TSAP mode cannot include rack, slot, or role")
		}
		if addressing.LocalTSAP == 0 || addressing.RemoteTSAP == 0 {
			return 0, 0, invalidError("config.addressing", "local and remote TSAP must be non-zero")
		}
		return addressing.LocalTSAP, addressing.RemoteTSAP, nil
	default:
		return 0, 0, invalidError("config.addressing.mode", "addressing mode is required")
	}
}

func normalizeEndpoint(input string) (string, error) {
	value := strings.TrimSpace(input)
	if value == "" {
		return "", errors.New("endpoint is required")
	}
	// Validate before SplitHostPort so an explicitly supplied port cannot
	// bypass hostname checks (notably the Windows net NUL-byte boundary).
	if strings.ContainsAny(value, "/\\\x00") {
		return "", errors.New("endpoint host is malformed")
	}
	if host, port, err := net.SplitHostPort(value); err == nil {
		if host == "" {
			return "", errors.New("endpoint host is required")
		}
		portNumber, parseErr := strconv.ParseUint(port, 10, 16)
		if parseErr != nil || portNumber == 0 {
			return "", errors.New("endpoint port must be between 1 and 65535")
		}
		return net.JoinHostPort(host, port), nil
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		value = strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
	}
	ipValue := value
	if zoneIndex := strings.LastIndexByte(ipValue, '%'); zoneIndex >= 0 {
		if zoneIndex == len(ipValue)-1 {
			return "", errors.New("IPv6 zone must not be empty")
		}
		ipValue = ipValue[:zoneIndex]
	}
	if ip := net.ParseIP(ipValue); ip != nil {
		if strings.Contains(value, "%") && ip.To4() != nil {
			return "", errors.New("IPv4 endpoint cannot include a zone")
		}
		return net.JoinHostPort(value, strconv.Itoa(DefaultPort)), nil
	}
	if strings.Contains(value, ":") {
		return "", errors.New("IPv6 endpoints with a port must use brackets")
	}
	return net.JoinHostPort(value, strconv.Itoa(DefaultPort)), nil
}

func normalizeLocalAddress(input string, port uint16) (*net.TCPAddr, error) {
	value := strings.TrimSpace(input)
	if value == "" {
		if port != 0 {
			return nil, errors.New("local port requires a local IP address")
		}
		return nil, nil
	}
	zone := ""
	if index := strings.LastIndexByte(value, '%'); index >= 0 {
		zone = value[index+1:]
		value = value[:index]
		if zone == "" {
			return nil, errors.New("IPv6 zone must not be empty")
		}
	}
	ip := net.ParseIP(value)
	if ip == nil {
		return nil, errors.New("local address must be an IP literal")
	}
	if zone != "" && ip.To4() != nil {
		return nil, errors.New("IPv4 local address cannot include a zone")
	}
	return &net.TCPAddr{IP: ip, Port: int(port), Zone: zone}, nil
}
