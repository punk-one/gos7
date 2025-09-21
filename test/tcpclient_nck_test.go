package test

import (
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"os"
	"time"

	"github.com/punk-one/gos7"
)

// const (
// 	tcpDevice = "192.168.211.12"
// 	rack      = 0
// 	slot      = 3
// )

func main() {
	// TCPClient  slot modify to 3 for nck test
	handler := gos7.NewTCPClientHandler(tcpDevice, rack, slot)
	handler.Timeout = 200 * time.Second
	handler.IdleTimeout = 200 * time.Second
	handler.Logger = log.New(os.Stdout, "tcp: ", log.LstdFlags)
	// Connect manually so that multiple requests are handled in one connection session
	err := handler.Connect()
	if err != nil {
		fmt.Println(err)
	}
	defer handler.Close()
	//init client
	client := gos7.NewClient(handler)

	err = writeNck(client)
	//err = readNck(client)
	// slot修改为2，读取PLC的DB块数据
	//bufDb := make([]byte, 1024)
	//client.AGReadDB(1, 10, 2, bufDb)

	if err != nil {
		fmt.Println(err)
	}
}

func readNck(client gos7.Client) error {

	items := []gos7.S7NckAddrItem{{
		Area:   4,
		Unit:   1,
		Column: 3,
		Line:   3,
		Module: 0x26,
	}, {
		Area:   4,
		Unit:   1,
		Column: 1,
		Line:   3,
		Module: 0x21,
	}, {
		Area:   4,
		Unit:   1,
		Column: 14,
		Line:   3,
		Module: 0x21,
	}, {
		Area:   4,
		Unit:   1,
		Column: 17,
		Line:   3,
		Module: 0x21,
	}, {
		Area:   4,
		Unit:   1,
		Column: 3,
		Line:   3,
		Module: 0x26,
	}}

	items2 := []gos7.S7NckAddrItem{
		{
			Area:   4,
			Unit:   1,
			Column: 3,
			Line:   12,
			Module: 0x14,
		}, {
			Area:   4,
			Unit:   1,
			Column: 3,
			Line:   13,
			Module: 0x14,
		}, {
			Area:   4,
			Unit:   1,
			Column: 3,
			Line:   26,
			Module: 0x14,
		}, {
			Area:   4,
			Unit:   1,
			Column: 3,
			Line:   3,
			Module: 0x14,
		}, {
			Area:   4,
			Unit:   1,
			Column: 3,
			Line:   4,
			Module: 0x14,
		}, {
			Area:   4,
			Unit:   1,
			Column: 3,
			Line:   6,
			Module: 0x14,
		}, {
			Area:   4,
			Unit:   1,
			Column: 3,
			Line:   4,
			Module: 0x22,
		}, {
			Area:   4,
			Unit:   1,
			Column: 3,
			Line:   6,
			Module: 0x22,
		}, {
			Area:   4,
			Unit:   1,
			Column: 3,
			Line:   8,
			Module: 0x14,
		}, {
			Area:   4,
			Unit:   1,
			Column: 3,
			Line:   9,
			Module: 0x14,
		}, {
			Area:   4,
			Unit:   1,
			Column: 3,
			Line:   15,
			Module: 0x14,
		}, {
			Area:   4,
			Unit:   1,
			Column: 3,
			Line:   3,
			Module: 0x22,
		}, {
			Area:   4,
			Unit:   1,
			Column: 3,
			Line:   7,
			Module: 0x22,
		}, {
			Area:   4,
			Unit:   1,
			Column: 3,
			Line:   8,
			Module: 0x22,
		}, {
			Area:   4,
			Unit:   1,
			Column: 3,
			Line:   9,
			Module: 0x22,
		}, {
			Area:   4,
			Unit:   1,
			Column: 3,
			Line:   26,
			Module: 0x22,
		}}
	resp2, err := client.AGReadMultiNCK(&items2)
	for i := range *resp2 {
		m := (*resp2)[i]
		f, _ := bytes2Float(m.Data)
		fmt.Println("response Mulity: f", f)
	}
	return err
}
func writeNck(client gos7.Client) error {

	addrs := []gos7.S7NckAddrItem{{
		Area:   4,
		Unit:   1,
		Column: 1,
		Line:   3,
		Module: 0x14,
	},
	}
	data1 := cvt64BitsData(99.99)
	dataArr := []gos7.S7NckDataItem{*data1}
	println(addrs, dataArr)
	//addr2 := []gos7.S7NckAddrItem{{
	//	Area:   4,
	//	Unit:   1,
	//	Column: 1,
	//	Line:   3,
	//	Module: 0x21,
	//},
	//}
	//dataArr2 := []gos7.S7NckDataItem{{
	//	Length: 32,
	//	Data: []byte{0x33, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	//		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
	//}}
	returnCode2, err := client.AGWriteMultiNCK(&addrs, &dataArr)

	println("returnCode2", returnCode2[0:1])
	return err
}

func cvt64BitsData(val float64) *gos7.S7NckDataItem {
	bytes := float2Bytes(val)
	data := &gos7.S7NckDataItem{
		Length: 8,
		Data:   bytes,
	}
	return data
}

func float2Bytes(f float64) []byte {
	bits := math.Float64bits(f)
	// 创建一个字节数组来存储位转换结果
	bytes := make([]byte, 8)
	// 将 bits 转换为小端字
	binary.LittleEndian.PutUint64(bytes, bits)
	return bytes
}

func bytes2Float(b []byte) (float64, error) {
	if len(b) != 8 {
		return 0, fmt.Errorf("输入的字节切片长度必须为8")
	}
	bits := binary.LittleEndian.Uint64(b)
	return math.Float64frombits(bits), nil
}
