//go:build s7integration

package qualification_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/punk-one/gos7"
)

const (
	integrationConfigEnvironment = "GOS7_INTEGRATION_CONFIG"
	integrationWriteEnvironment  = "GOS7_INTEGRATION_WRITE"
	integrationWriteConsent      = "ECHO-WRITE-TO-ALLOWLIST"
	maxIntegrationConfigBytes    = 64 << 10
	minRequestedPDU              = 240
)

type integrationConfiguration struct {
	Endpoint          string            `json:"endpoint"`
	Routing           string            `json:"routing"`
	Rack              uint8             `json:"rack,omitempty"`
	Slot              uint8             `json:"slot,omitempty"`
	Role              string            `json:"role,omitempty"`
	LocalTSAP         uint16            `json:"localTsap,omitempty"`
	RemoteTSAP        uint16            `json:"remoteTsap,omitempty"`
	LocalAddress      string            `json:"localAddress,omitempty"`
	LocalPort         uint16            `json:"localPort,omitempty"`
	RequestedPDU      uint16            `json:"requestedPdu,omitempty"`
	ConnectTimeoutMS  uint32            `json:"connectTimeoutMillis,omitempty"`
	ExchangeTimeoutMS uint32            `json:"exchangeTimeoutMillis,omitempty"`
	Reads             []integrationItem `json:"reads"`
	EchoWrites        []integrationItem `json:"echoWrites,omitempty"`
	WriteAllowlist    []string          `json:"writeAllowlist,omitempty"`
}

type integrationItem struct {
	Name      string `json:"name"`
	Address   string `json:"address"`
	Transport string `json:"transport,omitempty"`
	Count     uint32 `json:"count,omitempty"`
}

type preparedIntegrationItem struct {
	name   string
	target string
	read   gos7.ReadItem
}

func TestIntegrationConfigurationIsExternalStrictAndCanonical(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "qualification.json")
	raw := []byte(`{
  "endpoint":"192.0.2.10:102",
  "routing":"rack-slot",
  "rack":0,
  "slot":2,
  "role":"pg",
  "reads":[{"name":"lreal","address":"DB1.DBD4","transport":"byte","count":8}]
}`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(integrationConfigEnvironment, path)
	configuration := loadIntegrationConfiguration(t)
	prepared := prepareIntegrationItems(t, configuration.Reads)
	if len(prepared) != 1 || prepared[0].target != "DB1.DBD4/byte/8" ||
		prepared[0].read.Transport != gos7.TransportByte || prepared[0].read.Count != 8 {
		t.Fatalf("prepared integration target = %#v", prepared)
	}
	client, err := gos7.New(configuration.clientConfig())
	if err != nil {
		t.Fatalf("validated integration client config: %v", err)
	}
	_ = client.Close()
}

func TestS7HardwareQualification(t *testing.T) {
	configuration := loadIntegrationConfiguration(t)
	client, err := gos7.Dial(context.Background(), configuration.clientConfig())
	if err != nil {
		t.Fatalf("connect to qualified PLC: %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Errorf("close qualified PLC session: %v", err)
		}
	}()

	diagnostic := client.Diagnostics()
	if diagnostic.State != gos7.StateReady || !diagnostic.LimitsValid || diagnostic.Limits.NegotiatedPDU < minRequestedPDU {
		t.Fatalf("invalid negotiated session diagnostics: %#v", diagnostic)
	}
	t.Logf("connected: negotiated_pdu=%d session_generation=%d", diagnostic.Limits.NegotiatedPDU, diagnostic.SessionGeneration)

	readQualified := t.Run("read-only", func(t *testing.T) {
		items := prepareIntegrationItems(t, configuration.Reads)
		if len(items) == 0 {
			t.Fatal("hardware configuration must contain at least one read target")
		}
		wire := make([]gos7.ReadItem, len(items))
		for index := range items {
			wire[index] = items[index].read
		}
		ctx, cancel := context.WithTimeout(context.Background(), configuration.operationWindow(len(items)))
		defer cancel()
		results, err := client.Read(ctx, wire)
		if err != nil {
			t.Fatalf("qualified batch read: %v", err)
		}
		if len(results) != len(items) {
			t.Fatalf("qualified batch read returned %d results for %d items", len(results), len(items))
		}
		for index, result := range results {
			if result.Err != nil {
				t.Errorf("read %q (%s): %v", items[index].name, items[index].target, result.Err)
				continue
			}
			t.Logf("read %q (%s): %d bytes", items[index].name, items[index].target, len(result.Data))
		}
	})

	if !readQualified || len(configuration.EchoWrites) == 0 {
		return
	}
	t.Run("explicit-echo-write", func(t *testing.T) {
		if os.Getenv(integrationWriteEnvironment) != integrationWriteConsent {
			t.Skipf("set %s=%s to enable allowlisted echo-write qualification", integrationWriteEnvironment, integrationWriteConsent)
		}
		allowlist := make(map[string]struct{}, len(configuration.WriteAllowlist))
		for _, value := range configuration.WriteAllowlist {
			if value == "" || strings.TrimSpace(value) != value {
				t.Fatal("writeAllowlist entries must be non-empty and trimmed")
			}
			allowlist[value] = struct{}{}
		}
		items := prepareIntegrationItems(t, configuration.EchoWrites)
		for _, item := range items {
			if _, allowed := allowlist[item.target]; !allowed {
				t.Fatalf("echo-write target %q is not in writeAllowlist", item.target)
			}
			if !t.Run(item.name, func(t *testing.T) {
				echoWrite(t, client, item, configuration.operationWindow(1))
			}) {
				return
			}
		}
	})
}

func loadIntegrationConfiguration(t *testing.T) integrationConfiguration {
	t.Helper()
	path := os.Getenv(integrationConfigEnvironment)
	if path == "" {
		t.Skipf("set %s to an external hardware configuration file", integrationConfigEnvironment)
	}
	if !filepath.IsAbs(path) {
		t.Fatalf("%s must be an absolute path outside the repository", integrationConfigEnvironment)
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve hardware configuration: %v", err)
	}
	repository, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolve repository directory: %v", err)
	}
	resolvedRepository, err := filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatalf("resolve repository directory: %v", err)
	}
	if strings.EqualFold(filepath.VolumeName(resolvedRepository), filepath.VolumeName(resolvedPath)) {
		relative, err := filepath.Rel(resolvedRepository, resolvedPath)
		if err != nil || relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			t.Fatalf("%s must resolve outside the repository", integrationConfigEnvironment)
		}
	}
	path = resolvedPath
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxIntegrationConfigBytes {
		t.Fatalf("hardware configuration must be a regular file of 1..%d bytes", maxIntegrationConfigBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxIntegrationConfigBytes+1))
	decoder.DisallowUnknownFields()
	var value integrationConfiguration
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("decode hardware configuration: %v", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatal("hardware configuration must contain exactly one JSON value")
	}
	if len(value.Reads) > 4096 || len(value.EchoWrites) > 256 || len(value.WriteAllowlist) > 256 {
		t.Fatal("hardware configuration exceeds qualification item limits")
	}
	if value.ConnectTimeoutMS > 300_000 || value.ExchangeTimeoutMS > 300_000 {
		t.Fatal("hardware qualification timeouts must not exceed 300000 milliseconds")
	}
	return value
}

func (configuration integrationConfiguration) clientConfig() gos7.Config {
	addressing := gos7.Addressing{}
	switch configuration.Routing {
	case "rack-slot":
		addressing = gos7.Addressing{Mode: gos7.AddressByRackSlot, Rack: configuration.Rack, Slot: configuration.Slot, Role: parseIntegrationRole(configuration.Role)}
	case "tsap":
		addressing = gos7.Addressing{Mode: gos7.AddressByTSAP, LocalTSAP: configuration.LocalTSAP, RemoteTSAP: configuration.RemoteTSAP}
	}
	return gos7.Config{
		Endpoint: configuration.Endpoint, Addressing: addressing,
		LocalAddress: configuration.LocalAddress, LocalPort: configuration.LocalPort,
		RequestedPDU:    configuration.RequestedPDU,
		ConnectTimeout:  milliseconds(configuration.ConnectTimeoutMS),
		ExchangeTimeout: milliseconds(configuration.ExchangeTimeoutMS),
	}
}

func parseIntegrationRole(value string) gos7.ConnectionRole {
	switch value {
	case "pg":
		return gos7.ConnectionPG
	case "op":
		return gos7.ConnectionOP
	case "basic":
		return gos7.ConnectionBasic
	default:
		return 0
	}
}

func milliseconds(value uint32) time.Duration {
	if value == 0 {
		return 0
	}
	return time.Duration(value) * time.Millisecond
}

func (configuration integrationConfiguration) operationWindow(itemCount int) time.Duration {
	request := milliseconds(configuration.ExchangeTimeoutMS)
	if request == 0 {
		request = gos7.DefaultExchangeTimeout
	}
	if itemCount < 1 {
		itemCount = 1
	}
	window := request*time.Duration(itemCount+1) + time.Second
	if window > 5*time.Minute {
		return 5 * time.Minute
	}
	return window
}

func prepareIntegrationItems(t *testing.T, configured []integrationItem) []preparedIntegrationItem {
	t.Helper()
	result := make([]preparedIntegrationItem, len(configured))
	seenNames := make(map[string]struct{}, len(configured))
	seenTargets := make(map[string]struct{}, len(configured))
	for index, item := range configured {
		if item.Name == "" || strings.TrimSpace(item.Name) != item.Name || len(item.Name) > 128 || strings.ContainsAny(item.Name, "/\\\r\n\x00") {
			t.Fatalf("integration item %d has an invalid name", index)
		}
		if _, found := seenNames[item.Name]; found {
			t.Fatalf("duplicate integration item name %q", item.Name)
		}
		address, parsedTransport, err := gos7.ParseAddress(item.Address)
		if err != nil {
			t.Fatalf("integration item %q address: %v", item.Name, err)
		}
		transport, err := parseIntegrationTransport(item.Transport, parsedTransport)
		if err != nil {
			t.Fatalf("integration item %q transport: %v", item.Name, err)
		}
		count := item.Count
		if count == 0 {
			count = 1
		}
		canonical, err := gos7.FormatAddress(address, parsedTransport)
		if err != nil {
			t.Fatalf("integration item %q canonical address: %v", item.Name, err)
		}
		target := canonical + "/" + transport.String() + "/" + strconv.FormatUint(uint64(count), 10)
		if _, found := seenTargets[target]; found {
			t.Fatalf("duplicate integration target %q", target)
		}
		seenNames[item.Name] = struct{}{}
		seenTargets[target] = struct{}{}
		result[index] = preparedIntegrationItem{
			name: item.Name, target: target,
			read: gos7.ReadItem{Address: address, Transport: transport, Count: count},
		}
	}
	return result
}

func parseIntegrationTransport(value string, fallback gos7.TransportType) (gos7.TransportType, error) {
	switch value {
	case "":
		return fallback, nil
	case "bit":
		return gos7.TransportBit, nil
	case "byte":
		return gos7.TransportByte, nil
	case "word":
		return gos7.TransportWord, nil
	case "dword":
		return gos7.TransportDWord, nil
	case "real":
		return gos7.TransportReal, nil
	case "timer":
		return gos7.TransportTimer, nil
	case "counter":
		return gos7.TransportCounter, nil
	default:
		return 0, fmt.Errorf("unsupported transport %q", value)
	}
}

func echoWrite(t *testing.T, client *gos7.Client, item preparedIntegrationItem, timeout time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	before, err := client.Read(ctx, []gos7.ReadItem{item.read})
	cancel()
	if err != nil || len(before) != 1 || before[0].Err != nil {
		t.Fatalf("read echo-write source %s: batch=%v item=%v", item.target, err, firstReadError(before))
	}
	data := append([]byte(nil), before[0].Data...)
	ctx, cancel = context.WithTimeout(context.Background(), timeout)
	written, err := client.Write(ctx, []gos7.WriteItem{{
		Address: item.read.Address, Transport: item.read.Transport, Count: item.read.Count, Data: data,
	}})
	cancel()
	if err != nil || len(written) != 1 || written[0].Err != nil || written[0].Outcome != gos7.WriteAcknowledged {
		t.Fatalf("echo-write %s was not acknowledged: batch=%v item=%v outcome=%v; reconcile manually before retrying", item.target, err, integrationFirstWriteError(written), firstWriteOutcome(written))
	}
	ctx, cancel = context.WithTimeout(context.Background(), timeout)
	after, err := client.Read(ctx, []gos7.ReadItem{item.read})
	cancel()
	if err != nil || len(after) != 1 || after[0].Err != nil || !bytes.Equal(after[0].Data, data) {
		t.Fatalf("echo-write readback %s failed: batch=%v item=%v", item.target, err, firstReadError(after))
	}
	t.Logf("echo-write %q (%s): acknowledged and read back %d bytes", item.name, item.target, len(data))
}

func firstReadError(results []gos7.ReadResult) error {
	if len(results) != 1 {
		return errors.New("misaligned result")
	}
	return results[0].Err
}

func integrationFirstWriteError(results []gos7.WriteResult) error {
	if len(results) != 1 {
		return errors.New("misaligned result")
	}
	return results[0].Err
}

func firstWriteOutcome(results []gos7.WriteResult) gos7.WriteOutcome {
	if len(results) != 1 {
		return gos7.WriteNotAttempted
	}
	return results[0].Outcome
}
