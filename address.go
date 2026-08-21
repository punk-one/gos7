package gos7

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var (
	dbAddressPattern      = regexp.MustCompile(`^DB([0-9]+)\.(DBX|DBB|DBW|DBD)([0-9]+)(?:\.([0-9]+))?$`)
	memoryAddressPattern  = regexp.MustCompile(`^([IEQAM])(X|B|W|D)?([0-9]+)(?:\.([0-9]+))?$`)
	timerAddressPattern   = regexp.MustCompile(`^(?:T|TM)([0-9]+)$`)
	counterAddressPattern = regexp.MustCompile(`^(?:C|CT|Z)([0-9]+)$`)
)

// ParseAddress parses a finite absolute-address grammar and returns a wire
// width hint. The caller remains responsible for mapping its value type; for
// example, DB1.DBD4 may be read as DWord, DInt, REAL, or the start of LREAL.
func ParseAddress(input string) (Address, TransportType, error) {
	value := strings.ToUpper(strings.TrimSpace(input))
	value = strings.TrimPrefix(value, "%")
	if value == "" || strings.IndexFunc(value, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\r' || r == '\n'
	}) >= 0 {
		return Address{}, 0, invalidError("address.parse", "address is empty or contains whitespace")
	}

	if match := dbAddressPattern.FindStringSubmatch(value); match != nil {
		db, err := parseUint(match[1], math.MaxUint16, "DB number")
		if err != nil || db == 0 {
			if err == nil {
				err = fmt.Errorf("DB number must be non-zero")
			}
			return Address{}, 0, invalidError("address.parse", err.Error())
		}
		offset, err := parseUint(match[3], math.MaxUint32, "offset")
		if err != nil {
			return Address{}, 0, invalidError("address.parse", err.Error())
		}
		address := Address{Area: AreaDB, DBNumber: uint16(db), Offset: uint32(offset)}
		transport, err := parseWidth(match[2], match[4], &address)
		if err != nil {
			return Address{}, 0, invalidError("address.parse", err.Error())
		}
		if err := validateItemAddress(address, transport); err != nil {
			return Address{}, 0, err
		}
		return address, transport, nil
	}

	if match := memoryAddressPattern.FindStringSubmatch(value); match != nil {
		area := AreaMarker
		switch match[1] {
		case "I", "E":
			area = AreaInput
		case "Q", "A":
			area = AreaOutput
		case "M":
			area = AreaMarker
		}
		offset, err := parseUint(match[3], math.MaxUint32, "offset")
		if err != nil {
			return Address{}, 0, invalidError("address.parse", err.Error())
		}
		address := Address{Area: area, Offset: uint32(offset)}
		width := match[2]
		if width == "" && match[4] != "" {
			width = "X"
		}
		transport, err := parseWidth(width, match[4], &address)
		if err != nil {
			return Address{}, 0, invalidError("address.parse", err.Error())
		}
		if err := validateItemAddress(address, transport); err != nil {
			return Address{}, 0, err
		}
		return address, transport, nil
	}

	if match := timerAddressPattern.FindStringSubmatch(value); match != nil {
		index, err := parseUint(match[1], 0xFFFFFF, "timer index")
		if err != nil {
			return Address{}, 0, invalidError("address.parse", err.Error())
		}
		return Address{Area: AreaTimer, Offset: uint32(index)}, TransportTimer, nil
	}
	if match := counterAddressPattern.FindStringSubmatch(value); match != nil {
		index, err := parseUint(match[1], 0xFFFFFF, "counter index")
		if err != nil {
			return Address{}, 0, invalidError("address.parse", err.Error())
		}
		return Address{Area: AreaCounter, Offset: uint32(index)}, TransportCounter, nil
	}
	return Address{}, 0, invalidError("address.parse", "unsupported absolute-address syntax")
}

func parseWidth(width, bitText string, address *Address) (TransportType, error) {
	if strings.HasSuffix(width, "X") || width == "X" {
		if bitText == "" {
			return 0, fmt.Errorf("bit address requires a .0 through .7 suffix")
		}
		bit, err := parseUint(bitText, 7, "bit")
		if err != nil {
			return 0, err
		}
		address.Bit = uint8(bit)
		return TransportBit, nil
	}
	if bitText != "" {
		return 0, fmt.Errorf("only bit addresses may include a bit suffix")
	}
	switch {
	case strings.HasSuffix(width, "B"):
		return TransportByte, nil
	case strings.HasSuffix(width, "W"):
		return TransportWord, nil
	case strings.HasSuffix(width, "D"):
		return TransportDWord, nil
	default:
		return 0, fmt.Errorf("address width is required")
	}
}

func parseUint(value string, max uint64, label string) (uint64, error) {
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed > max {
		return 0, fmt.Errorf("%s is out of range", label)
	}
	return parsed, nil
}

// FormatAddress returns the single canonical spelling for an address and wire
// width. Aliases accepted by ParseAddress are never emitted.
func FormatAddress(address Address, transport TransportType) (string, error) {
	if err := validateItemAddress(address, transport); err != nil {
		return "", err
	}
	if address.Area == AreaTimer {
		return fmt.Sprintf("T%d", address.Offset), nil
	}
	if address.Area == AreaCounter {
		return fmt.Sprintf("C%d", address.Offset), nil
	}
	prefix := ""
	if address.Area == AreaDB {
		prefix = fmt.Sprintf("DB%d.DB", address.DBNumber)
	} else {
		switch address.Area {
		case AreaInput:
			prefix = "I"
		case AreaOutput:
			prefix = "Q"
		case AreaMarker:
			prefix = "M"
		}
	}
	switch transport {
	case TransportBit:
		return fmt.Sprintf("%sX%d.%d", prefix, address.Offset, address.Bit), nil
	case TransportByte:
		return fmt.Sprintf("%sB%d", prefix, address.Offset), nil
	case TransportWord:
		return fmt.Sprintf("%sW%d", prefix, address.Offset), nil
	case TransportDWord, TransportReal:
		return fmt.Sprintf("%sD%d", prefix, address.Offset), nil
	default:
		return "", invalidError("address.format", "transport has no byte-area text form")
	}
}
