package gos7

// Copyright 2018 Trung Hieu Le. All rights reserved.
// This software may be modified and distributed under the terms
// of the BSD license. See the LICENSE file for details.
import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
)

const (
	// Area ID
	s7areape = 0x81 //process inputs
	s7areapa = 0x82 //process outputs
	s7areamk = 0x83 //Merkers
	s7areadb = 0x84 //DB
	s7areact = 0x1C //counters
	s7areatm = 0x1D //timers

	// Word Length
	s7wlbit     = 0x01 //Bit (inside a word)
	s7wlbyte    = 0x02 //Byte (8 bit)
	s7wlChar    = 0x03
	s7wlword    = 0x04 //Word (16 bit)
	s7wlint     = 0x05
	s7wldword   = 0x06 //Double Word (32 bit)
	s7wldint    = 0x07
	s7wlreal    = 0x08 //Real (32 bit float)
	s7wlcounter = 0x1C //Counter (16 bit)
	s7wltimer   = 0x1D //Timer (16 bit)

	// PLC Status
	s7CpuStatusUnknown = 0
	s7CpuStatusRun     = 8
	s7CpuStatusStop    = 4

	//size header
	sizeHeaderRead  int = 31 // Header Size when Reading
	sizeHeaderWrite int = 35 // Header Size when Writing
	//
	sizeNckHeaderRead  int = 19 // Header Size when Reading
	sizeNckHeaderWrite int = 19 // Header Size when Writing

	// Result transport size
	tsResBit   = 3
	tsResByte  = 4
	tsResInt   = 5
	tsResReal  = 7
	tsResOctet = 9
)

const (
	s7WriteReadFunction = 0x04
	s7WriteVarFunction  = 0x05
)

//PDULength variable to store pdu length after connect
//var tt, _ := mb.transporter.(*tcpTransporter)tt, _ := mb.transporter.(*tcpTransporter) int //global variable pdulength

// CliePDULengthntHandler is the interface that groups the Packager and Transporter methods.
type ClientHandler interface {
	Packager
	Transporter
}
type client struct {
	packager    Packager
	transporter Transporter
}

// NewClient creates a new s7 client with given backend handler.
func NewClient(handler ClientHandler) Client {
	return &client{packager: handler, transporter: handler}
}

// NewClient2 creates a new s7 client with given backend packager and transporter.
func NewClient2(packager Packager, transporter Transporter) Client {
	return &client{packager: packager, transporter: transporter}
}

// implement of the interface AGReadDB
func (mb *client) AGReadDB(dbnumber int, start int, size int, buffer []byte) (err error) {
	return mb.readArea(s7areadb, dbnumber, start, size, s7wlbyte, buffer)
}

// implement of the interface AGWriteDB
func (mb *client) AGWriteDB(dbNumber int, start int, size int, buffer []byte) (err error) {
	return mb.writeArea(s7areadb, dbNumber, start, size, s7wlbyte, buffer)
}

// implement of the interface AGReadMB
func (mb *client) AGReadMB(start int, size int, buffer []byte) (err error) {
	return mb.readArea(s7areamk, 0, start, size, s7wlbyte, buffer)
}

// implement of the interface AGWriteMB
func (mb *client) AGWriteMB(start int, size int, buffer []byte) (err error) {
	return mb.writeArea(s7areamk, 0, start, size, s7wlbyte, buffer)
}

// implement of the interface AGReadEB
func (mb *client) AGReadEB(start int, size int, buffer []byte) (err error) {
	return mb.readArea(s7areape, 0, start, size, s7wlbyte, buffer)
}

// implement of the interface AGWriteEB
func (mb *client) AGWriteEB(start int, size int, buffer []byte) (err error) {
	return mb.writeArea(s7areape, 0, start, size, s7wlbyte, buffer)
}

// implement of the interface AGReadAB
func (mb *client) AGReadAB(start int, size int, buffer []byte) (err error) {
	return mb.readArea(s7areapa, 0, start, size, s7wlbyte, buffer)
}

// implement of the interface AGWriteAB
func (mb *client) AGWriteAB(start int, size int, buffer []byte) (err error) {
	return mb.writeArea(s7areapa, 0, start, size, s7wlbyte, buffer)
}

// implement of the interface AGReadTM - read timer
func (mb *client) AGReadTM(start int, amount int, buffer []byte) (err error) {
	sbuffer := make([]byte, amount*2)
	err = mb.readArea(s7areatm, 0, start, amount, s7wltimer, sbuffer)
	if err == nil {
		for c := 0; c < amount; c++ {
			buffer[c] = byte(uint16(sbuffer[c*2+1])<<8 + uint16(sbuffer[c*2]))
		}
	}
	return err
}

// implement of the interface AGWriteTM - write timer
func (mb *client) AGWriteTM(start int, amount int, buffer []byte) (err error) {
	sbuffer := make([]byte, amount*2)
	for c := 0; c < amount; c++ {
		sbuffer[c*2+1] = byte((uint(buffer[c]) & uint(0xFF00)) >> 8)
		sbuffer[c*2] = byte(buffer[c] & 0x00FF)
	}
	err = mb.writeArea(s7areatm, 0, start, amount, s7wltimer, sbuffer)
	return err
}

// implement of the interface AGReadCT - read counter
func (mb *client) AGReadCT(start int, amount int, buffer []byte) (err error) {
	sbuffer := make([]byte, amount*2)
	err = mb.readArea(s7areact, 0, start, amount, s7wlcounter, sbuffer)
	if err == nil {
		for c := 0; c < amount; c++ {
			buffer[c] = byte(uint(sbuffer[c*2+1])<<8 + uint(sbuffer[c*2]))
		}
	}
	return err
}

// implement of the interface AGWriteCT - write counter
func (mb *client) AGWriteCT(start int, amount int, buffer []byte) (err error) {
	sbuffer := make([]byte, amount*2)
	for c := 0; c < amount; c++ {
		sbuffer[c*2+1] = byte((uint(buffer[c]) & uint(0xFF00)) >> 8)
		sbuffer[c*2] = byte(buffer[c] & 0x00FF)
	}
	err = mb.writeArea(s7areact, 0, start, amount, s7wlcounter, sbuffer)
	return err
}

// implement of the interface AGReadNCK
func (mb *client) AGReadNCK(addrItem *S7NckAddrItem) (dataItem *S7NckDataItem, err error) {
	addrItems := []S7NckAddrItem{*addrItem}
	dataItems := make([]S7NckDataItem, 0)
	err = mb.readNckArea(&addrItems, &dataItems)
	return &dataItems[0], err
}

// implement of the interface AGWriteNCK
func (mb *client) AGWriteNCK(addrItem *S7NckAddrItem, dataItem *S7NckDataItem) (returnCode byte, err error) {
	addrItems := []S7NckAddrItem{*addrItem}
	dataItems := []S7NckDataItem{*dataItem}
	returnCodes, err := mb.writeNckArea(&addrItems, &dataItems)
	return returnCodes[0], err
}

// implement of the interface AGReadMultiNCK
func (mb *client) AGReadMultiNCK(addrItems *[]S7NckAddrItem) (dataItems *[]S7NckDataItem, err error) {
	respItems := make([]S7NckDataItem, 0)
	err = mb.readNckArea(addrItems, &respItems)
	return &respItems, err
}

// implement of the interface AGWriteMultiNCK
func (mb *client) AGWriteMultiNCK(addrItems *[]S7NckAddrItem, dataItems *[]S7NckDataItem) (returnCodes []byte, err error) {

	return mb.writeNckArea(addrItems, dataItems)
}

// read generic area, pass result into a buffer
func (mb *client) readArea(area int, dbNumber int, start int, amount int, wordLen int, buffer []byte) (err error) {
	var address, numElements, maxElements, totElements, sizeRequested int
	offset := 0
	wordSize := 1
	// Some adjustment
	if area == s7areact {
		wordLen = s7wlcounter
	}
	if area == s7areatm {
		wordLen = s7wltimer
	}
	// Calc Word size
	wordSize = dataSizeByte(wordLen)
	if wordSize == 0 {
		return fmt.Errorf(ErrorText(errIsoInvalidDataSize))
	}

	if wordLen == s7wlbit {
		amount = 1 // Only 1 bit can be transferred at time
	} else {
		if wordLen != s7wlcounter && wordLen != s7wltimer {
			amount = amount * wordSize
			wordSize = 1
			wordLen = s7wlbyte
		}
	}

	tt, _ := interface{}(mb.transporter).(*TCPClientHandler)

	maxElements = (tt.PDULength - 18) / wordSize // 18 = Reply telegram header //lth note here
	totElements = amount
	for totElements > 0 && err == nil {
		numElements = totElements
		if numElements > maxElements {
			numElements = maxElements
		}

		sizeRequested = numElements * wordSize
		// Setup the telegram
		requestData := make([]byte, sizeHeaderRead)
		copy(requestData[0:], s7ReadWriteTelegram[0:])
		request := NewProtocolDataUnit(requestData)
		// Set DB Number
		request.Data[27] = byte(area)
		// Set Area
		if area == s7areadb {
			binary.BigEndian.PutUint16(request.Data[25:], uint16(dbNumber))
			//SetWordAt(request.Data, 25, uint16(DBNumber))
		}

		// Adjusts Start and word length
		if wordLen == s7wlbit || wordLen == s7wlcounter || wordLen == s7wltimer {
			address = start
			request.Data[22] = byte(wordLen)
		} else {
			address = start << 3
		}
		// Num elements
		binary.BigEndian.PutUint16(request.Data[23:], uint16(numElements))
		//SetWordAt(request.Data, 23, uint16(numElements))
		// Address into the PLC (only 3 bytes)
		request.Data[30] = byte(address & 0x0FF)
		address = address >> 8
		request.Data[29] = byte(address & 0x0FF)
		address = address >> 8
		request.Data[28] = byte(address & 0x0FF)
		var response *ProtocolDataUnit
		response, sendError := mb.send(&request)
		err = sendError

		if err == nil {
			if size := len(response.Data); size < 25 {
				err = fmt.Errorf(ErrorText(errIsoInvalidDataSize)+"'%v'", len(response.Data))
			} else {
				if response.Data[21] != 0xFF {
					err = fmt.Errorf(ErrorText(CPUError(uint(response.Data[21]))))
				} else {
					//copy response to buffer
					copy(buffer[offset:offset+sizeRequested], response.Data[25:25+sizeRequested])
					offset += sizeRequested
				}
			}

		}
		totElements -= numElements
		start += numElements * wordSize
	}
	return
}

// writeArea write generic area into PLC with following parameters:
// 1.area: s7areape/s7areapa/s7areamk/s7areadb/s7areact/s7areatm
// 2.dbnumber: specify dbnumber, to use in write DB area, otherwise = 0
// 3.start: start of the address
// 4.amount: amount of the address
// 5.wordlen: bit/byte/word/dword/real/counter/timer
// 6.buffer: a byte array input for writing
func (mb *client) writeArea(area int, dbnumber int, start int, amount int, wordlen int, buffer []byte) (err error) {
	var address, numElements, maxElements, totElements, dataSize, isoSize, length int
	offset := 0
	wordSize := 1

	// Some adjustment
	if area == s7areact {
		wordlen = s7wlcounter
	}
	if area == s7areatm {
		wordlen = s7wltimer
	}

	// Calc Word size
	wordSize = dataSizeByte(wordlen)
	if wordSize == 0 {
		return fmt.Errorf(ErrorText(errIsoInvalidDataSize))
	}

	if wordlen == s7wlbit {
		amount = 1 // Only 1 bit can be transferred at time
	} else {
		if wordlen != s7wlcounter && wordlen != s7wltimer {
			amount = amount * wordSize
			wordSize = 1
			wordlen = s7wlbyte
		}
	}
	tt, _ := interface{}(mb.transporter).(*TCPClientHandler)
	maxElements = (tt.PDULength - 35) / wordSize // 35 = Reply telegram header
	totElements = amount
	for totElements > 0 && err == nil {
		numElements = totElements
		if numElements > maxElements {
			numElements = maxElements
		}
		dataSize = numElements * wordSize
		isoSize = sizeHeaderWrite + dataSize

		// Setup the telegram
		requestData := make([]byte, sizeHeaderWrite)
		copy(requestData[0:], s7ReadWriteTelegram[0:])

		request := NewProtocolDataUnit(requestData)
		// Whole telegram Size
		binary.BigEndian.PutUint16(request.Data[2:], uint16(isoSize))
		//SetWordAt(request.Data, 2, uint16(isoSize))
		// Data length
		length = dataSize + 4
		binary.BigEndian.PutUint16(request.Data[15:], uint16(length))
		// SetWordAt(request.Data, 15, uint16(length))
		// Function
		request.Data[17] = byte(0x05)
		// Set DB Number
		request.Data[27] = byte(area)
		if area == s7areadb {
			binary.BigEndian.PutUint16(request.Data[25:], uint16(dbnumber))
			//SetWordAt(request.Data, 25, uint16(dbnumber))
		}
		// Adjusts start and word length
		if wordlen == s7wlbit || wordlen == s7wlcounter || wordlen == s7wltimer {
			address = start
			length = dataSize
			request.Data[22] = byte(wordlen)
		} else {
			address = start << 3
			length = dataSize << 3
		}

		// Num elements
		binary.BigEndian.PutUint16(request.Data[23:], uint16(numElements))
		// SetWordAt(request.Data, 23, uint16(numElements))
		// address into the PLC
		request.Data[30] = byte(address & 0x0FF)
		address = address >> 8
		request.Data[29] = byte(address & 0x0FF)
		address = address >> 8
		request.Data[28] = byte(address & 0x0FF)

		// Transport Size
		switch wordlen {
		case s7wlbit:
			request.Data[32] = tsResBit
			break
		case s7wlcounter:
		case s7wltimer:
			request.Data[32] = tsResOctet
			break
		default:
			request.Data[32] = tsResByte // byte/word/dword etc.
			break
		}
		// length
		// SetWordAt(request.Data, 33, uint16(length))
		binary.BigEndian.PutUint16(request.Data[33:], uint16(length))

		//expand values into array
		request.Data = append(request.Data[:35], append(buffer[offset:offset+dataSize], request.Data[35:]...)...)
		response, sendError := mb.send(&request)
		err = sendError
		if err == nil {
			if length = len(response.Data); length == 22 {
				if response.Data[21] != byte(0xFF) {
					err = fmt.Errorf(ErrorText(CPUError(uint(response.Data[21]))))
				}
			} else {
				err = fmt.Errorf(ErrorText(errIsoInvalidPDU))
			}

		}
		offset += dataSize
		totElements -= numElements
		start += numElements * wordSize
	}
	return
}

// DBRead
func (mb *client) Read(variable string, buffer []byte) (value interface{}, err error) {
	variable = strings.ToUpper(variable)              //upper
	variable = strings.Replace(variable, " ", "", -1) //remove spaces

	if variable == "" {
		err = fmt.Errorf("input variable is empty, variable should be S7 syntax")
		return
	}
	//var area, dbNumber, start, amount, wordLen int
	switch valueArea := variable[0:2]; valueArea {
	case "EB": //input byte
	case "EW": //input word
	case "ED": //Input double-word
	case "AB": //Output byte
	case "AW": //Output word
	case "AD": //Output double-word
	case "MB": //Memory byte
	case "MW": //Memory word
	case "MD": //Memory double-word
	case "DB": //Data Block
		dbArray := strings.Split(variable, ".")
		if len(dbArray) < 2 {
			err = fmt.Errorf("Db Area read variable should not be empty")
			return
		}
		dbNo, _ := strconv.ParseInt(string(string(dbArray[0])[2:]), 10, 16)
		dbIndex, _ := strconv.ParseInt(string(string(dbArray[1])[3:]), 10, 16)
		dbType := string(dbArray[1])[0:3]

		switch dbType {
		case "DBB": //byte
			err = mb.AGReadDB(int(dbNo), int(dbIndex), 1, buffer)
			value = buffer[0]
			return
		case "DBW": //word
			err = mb.AGReadDB(int(dbNo), int(dbIndex), 2, buffer)
			value = binary.BigEndian.Uint16(buffer[0:])
			return
		case "DBD": //dword
			err = mb.AGReadDB(int(dbNo), int(dbIndex), 4, buffer)
			value = binary.BigEndian.Uint32(buffer[0:])
			return
		case "DBX": //bit
			mBit, _ := strconv.ParseInt(string(string(dbArray[2])[0:]), 10, 16)
			if mBit > 7 || mBit < 0 {
				err = fmt.Errorf("Db read bit is invalid")
				return
			}
			err = mb.AGReadDB(int(dbNo), int(dbIndex), 1, buffer)
			mask := []byte{0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40, 0x80}
			value = buffer[0] & mask[mBit]
			return
		default:
			err = fmt.Errorf("error when parsing dbtype")
			return
		}
	default:
		switch otherArea := variable[0:1]; otherArea {
		case "E":
		case "I": //input
		case "A":
		case "0": //output
		case "M": //memory
		case "T": //timer
			startByte, _ := strconv.ParseInt(string(variable[1:]), 10, 16)
			err = mb.AGReadTM(int(startByte), 1, buffer)
			if err != nil {
				return
			}
			helper := Helper{}
			helper.GetValueAt(buffer, 0, value)
			return
		case "Z":
		case "C": //counter
			startByte, _ := strconv.ParseInt(string(variable[1:]), 10, 16)
			err = mb.AGReadCT(int(startByte), 1, buffer)
			if err != nil {
				return
			}
			helper := Helper{}
			helper.GetValueAt(buffer, 0, value)
			return
		default:
			err = fmt.Errorf("error when parsing db area")
			return
		}

	}
	return
}

// send the package of a pdu request and a pdu response, check for response error and verify the package
func (mb *client) send(request *ProtocolDataUnit) (response *ProtocolDataUnit, err error) {
	dataResponse, err := mb.transporter.Send(request.Data)
	if err != nil {
		return
	}

	if err = mb.packager.Verify(request.Data, dataResponse); err != nil {
		return
	}
	if dataResponse == nil || len(dataResponse) == 0 {
		// Empty response
		err = fmt.Errorf("s7: response data is empty")
		return
	}
	response = &ProtocolDataUnit{
		Data: dataResponse,
	}
	//check for error if any
	err = responseError(response)
	return response, err
}

// responseError get response error from pdu return S7Error with high and low byte
func responseError(response *ProtocolDataUnit) error {
	s7Error := &S7Error{}
	if response.Data != nil && len(response.Data) > 0 {
		switch int(response.Data[1]) {
		case 1:
		case 7:
			s7Error.High = response.Data[2]
			s7Error.Low = response.Data[3]
			break
		case 2:
		case 3:
			s7Error.High = response.Data[10]
			s7Error.Low = response.Data[11]
			break
		default:
			return nil
		}
	}
	return s7Error
}

// dataSize to number of byte accordingly
func dataSizeByte(wordLength int) int {
	switch wordLength {
	case s7wlbit:
		return 1
	case s7wlbyte:
		return 1
	case s7wlChar:
		return 1
	case s7wlword:
		return 2
	case s7wlint:
		return 2
	case s7wlcounter:
		return 2
	case s7wltimer:
		return 2
	case s7wldword:
		return 4
	case s7wldint:
		return 4
	case s7wlreal:
		return 4
	default:
		return 0
	}

}

// read generic area, pass result into a buffer
func (mb *client) readNckArea(addrItem *[]S7NckAddrItem, respItems *[]S7NckDataItem) (err error) {
	addrCnt := len(*addrItem)
	if addrCnt == 0 {
		return fmt.Errorf("read NckArea Must Give DataItem")
	}
	sizeProtocolBuf := sizeNckHeaderRead + addrCnt*10
	requestData := make([]byte, sizeProtocolBuf)
	copy(requestData[0:], s7NckReadWriteTelegram[0:])
	request := NewProtocolDataUnit(requestData)
	// 写协议的数据长度信息
	binary.BigEndian.PutUint16(request.Data[2:], uint16(sizeProtocolBuf))
	request.Data[18] = byte(addrCnt)
	// 参数长度
	paraLen := addrCnt*10 + 2
	binary.BigEndian.PutUint16(request.Data[13:], uint16(paraLen))

	// 写协议地址信息
	for i, item := range *addrItem {
		offset := 19 + i*10
		request.Data[offset] = 0x12   // variable specification
		request.Data[offset+1] = 0x08 // Length of the following address specification
		request.Data[offset+2] = 0x82 // SyntaxId NCK = 0x82
		request.Data[offset+3] = combineToByte(item.Area, item.Unit)
		binary.BigEndian.PutUint16(request.Data[offset+4:], uint16(item.Column))
		binary.BigEndian.PutUint16(request.Data[offset+6:], uint16(item.Line))
		request.Data[offset+8] = byte(item.Module)
		request.Data[offset+9] = 1 // LINE COUNT
	}
	var response *ProtocolDataUnit
	response, sendError := mb.send(&request)
	err = sendError

	if err == nil {
		if size := len(response.Data); size < 25 {
			err = fmt.Errorf(ErrorText(errIsoInvalidDataSize)+"'%v'", len(response.Data))
		} else {
			fmt.Printf("response data: %v\n", response.Data)
			//copy response to buffer
			err := ParseS7NckRespItems(response.Data[21:], respItems)
			if err != nil {
				fmt.Println(err)
			}
		}
	}
	return
}

// 解析[]byte为[]S7NckDataItem
func ParseS7NckRespItems(data []byte, items *[]S7NckDataItem) error {
	offset := 0
	for offset < len(data) {
		// 检查是否有足够的数据
		if offset+4 > len(data) {
			return fmt.Errorf("invalid data length")
		}

		// 解析ReturnCode, TransportSize
		returnCode := int(data[offset])
		transportSize := int(data[offset+1])

		// 解析Length (Length是2个字节，需要大端序)
		length := int(binary.BigEndian.Uint16(data[offset+2 : offset+4]))
		offset += 4

		// 解析Data
		var respData []byte
		if length > 0 {
			if offset+length > len(data) {
				return fmt.Errorf("invalid length for data section")
			}
			respData = data[offset : offset+length]
			offset += length
		} else {
			respData = nil
		}

		// 添加解析结果到items
		item := S7NckDataItem{
			ReturnCode:    returnCode,
			TransportSize: transportSize,
			Length:        length,
			Data:          respData,
		}
		*items = append(*items, item)
	}

	return nil
}

func hexStringToBytes(hex string) ([]byte, error) {
	hexLen := len(hex)
	if hexLen%2 != 0 {
		return nil, fmt.Errorf("hex string length is not even")
	}

	byteLen := hexLen / 2
	bytesArr := make([]byte, byteLen)

	for i := 0; i < byteLen; i++ {
		var value uint64
		var err error
		value, err = strconv.ParseUint(hex[i*2:i*2+2], 16, 8)
		if err != nil {
			return nil, err
		}
		// Convert the uint64 value to uint8
		bytesArr[i] = uint8(value)
	}

	return bytesArr, nil
}
func combineToByte(area int, unit int) byte {
	// 检查输入是否有效
	if area < 0 || area > 7 || unit < 0 || unit > 31 {
		panic("Invalid input values")
	}

	// 组合高三位和低五位
	combined := (byte(area) << 5) | byte(unit)

	return combined
}

// writeArea write generic area into PLC with following parameters:
// 1.area: s7areape/s7areapa/s7areamk/s7areadb/s7areact/s7areatm
// 2.dbnumber: specify dbnumber, to use in write DB area, otherwise = 0
// 3.start: start of the address
// 4.amount: amount of the address
// 5.wordlen: bit/byte/word/dword/real/counter/timer
// 6.buffer: a byte array input for writing
func (mb *client) writeNckArea(addrItems *[]S7NckAddrItem, dataItems *[]S7NckDataItem) (returnCodes []byte, err error) {
	// Setup the telegram
	addrCnt := len(*addrItems)
	dataCnt := len(*dataItems)
	if addrCnt == 0 || dataCnt == 0 || addrCnt != dataCnt {
		return nil, fmt.Errorf("addrItems size must equal dataItems size and not equal zero")
	}
	nckDataLen := calcNckDataLen(dataItems)
	buffLen := sizeNckHeaderWrite + addrCnt*10 + nckDataLen
	requestData := make([]byte, buffLen)
	copy(requestData[0:], s7NckReadWriteTelegram[0:])

	request := NewProtocolDataUnit(requestData)
	// 填写TPKT数据长度
	binary.BigEndian.PutUint16(requestData[2:], uint16(buffLen))
	// 填写S7 参数长度
	binary.BigEndian.PutUint16(requestData[13:], uint16(addrCnt*10+2))
	// 填写S7 数据长度
	binary.BigEndian.PutUint16(requestData[15:], uint16(nckDataLen))
	// 写参数是  为5
	requestData[17] = s7WriteVarFunction
	// addr item个数
	requestData[18] = byte(addrCnt)

	// 写协议地址信息
	for i, item := range *addrItems {
		offset := 19 + i*10
		request.Data[offset] = 0x12   // variable specification
		request.Data[offset+1] = 0x08 // Length of the following address specification
		request.Data[offset+2] = 0x82 // SyntaxId NCK = 0x82
		request.Data[offset+3] = combineToByte(item.Area, item.Unit)
		binary.BigEndian.PutUint16(request.Data[offset+4:], uint16(item.Column))
		binary.BigEndian.PutUint16(request.Data[offset+6:], uint16(item.Line))
		request.Data[offset+8] = byte(item.Module)
		request.Data[offset+9] = 1 // LINE COUNT
	}
	// 写数据
	dataBuff, _ := conNckDataItems(dataItems)
	//expand values into array
	addrOffset := 19 + addrCnt*10
	request.Data = append(request.Data[:addrOffset], dataBuff[:]...)
	response, sendError := mb.send(&request)
	err = sendError
	if err == nil {
		if length := len(response.Data); length == 21+addrCnt {
			return response.Data[21:], nil
		} else {
			err = fmt.Errorf(ErrorText(errIsoInvalidPDU))
		}

	}

	return
}

func calcNckDataLen(items *[]S7NckDataItem) int {
	totalLength := 0

	for _, item := range *items {
		// 每个 ReturnCode 占 1 字节
		// 每个 TransportSize 占 1 字节
		// 每个 Length 占 2 字节
		// Data 的长度由 Length 字段决定
		itemLength := 1 + 1 + 2 + item.Length
		totalLength += itemLength
	}

	return totalLength
}

func conNckDataItems(dataItems *[]S7NckDataItem) ([]byte, error) {
	var buffer bytes.Buffer
	for _, item := range *dataItems {
		buffer.WriteByte(0x00) // ReturnCode为保留字，固定为0x00
		buffer.WriteByte(0x09) // TransportSize为0x09时表示
		lengthBytes := make([]byte, 2)
		binary.BigEndian.PutUint16(lengthBytes, uint16(item.Length))
		buffer.Write(lengthBytes)
		// 将 Data 写入 buffer
		buffer.Write(item.Data)
	}
	// 返回拼接的结果
	return buffer.Bytes(), nil
}
