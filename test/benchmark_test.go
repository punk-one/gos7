package gos7_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/punk-one/gos7"
	"github.com/punk-one/gos7/internal/testplc"
)

var (
	benchmarkReadResults []gos7.ReadResult
	benchmarkAreaBytes   int
)

func startBenchmarkClient(b *testing.B, pdu uint16) *gos7.Client {
	b.Helper()
	server, err := testplc.Start(pdu)
	if err != nil {
		b.Fatal(err)
	}
	client, err := gos7.Dial(context.Background(), gos7.Config{
		Endpoint: server.Endpoint(),
		Addressing: gos7.Addressing{
			Mode: gos7.AddressByRackSlot,
			Rack: 0,
			Slot: 2,
			Role: gos7.ConnectionPG,
		},
	})
	if err != nil {
		_ = server.Close()
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	return client
}

func BenchmarkBatchRead(b *testing.B) {
	for _, itemCount := range []int{1, 20, 21, 100, 1000, 4096} {
		b.Run(fmt.Sprintf("items-%d", itemCount), func(b *testing.B) {
			client := startBenchmarkClient(b, 960)
			items := make([]gos7.ReadItem, itemCount)
			for index := range items {
				items[index] = gos7.ReadItem{
					Address: gos7.Address{
						Area: gos7.AreaDB, DBNumber: 1, Offset: uint32(index * 4),
					},
					Transport: gos7.TransportReal,
					Count:     1,
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				results, err := client.Read(context.Background(), items)
				if err != nil {
					b.Fatal(err)
				}
				for _, result := range results {
					if result.Err != nil {
						b.Fatal(result.Err)
					}
				}
				benchmarkReadResults = results
			}
		})
	}
}

func BenchmarkLargeContinuousRead(b *testing.B) {
	client := startBenchmarkClient(b, 960)
	buffer := make([]byte, 1<<20)
	b.SetBytes(int64(len(buffer)))
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		read, err := client.ReadArea(context.Background(), gos7.AreaDB, 1, 0, buffer)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkAreaBytes = read
	}
}
