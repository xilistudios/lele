package tools

import (
	"encoding/json"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

const (
	spiIocWrMode        = 0x40016B01
	spiIocWrBitsPerWord = 0x40016B03
	spiIocWrMaxSpeedHz  = 0x40046B04
	spiIocMessage1      = 0x40206B00
)

type spiTransfer struct {
	txBuf       uint64
	rxBuf       uint64
	length      uint32
	speedHz     uint32
	delayUsecs  uint16
	bitsPerWord uint8
	csChange    uint8
	txNbits     uint8
	rxNbits     uint8
	wordDelay   uint8
	pad         uint8
}

func configureSPI(devPath string, mode uint8, bits uint8, speed uint32) (int, *ToolResult) {
	fd, err := syscall.Open(devPath, syscall.O_RDWR, 0)
	if err != nil {
		return -1, ErrorResult(fmt.Sprintf("failed to open %s: %v (check permissions and spidev module)", devPath, err))
	}

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), spiIocWrMode, uintptr(unsafe.Pointer(&mode)))
	if errno != 0 {
		syscall.Close(fd)
		return -1, ErrorResult(fmt.Sprintf("failed to set SPI mode %d: %v", mode, errno))
	}

	_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), spiIocWrBitsPerWord, uintptr(unsafe.Pointer(&bits)))
	if errno != 0 {
		syscall.Close(fd)
		return -1, ErrorResult(fmt.Sprintf("failed to set bits per word %d: %v", bits, errno))
	}

	_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), spiIocWrMaxSpeedHz, uintptr(unsafe.Pointer(&speed)))
	if errno != 0 {
		syscall.Close(fd)
		return -1, ErrorResult(fmt.Sprintf("failed to set SPI speed %d Hz: %v", speed, errno))
	}

	return fd, nil
}

func (t *SPITool) transfer(args map[string]interface{}) *ToolResult {
	confirm, _ := args["confirm"].(bool)
	if !confirm {
		return ErrorResult("transfer operations require confirm: true. Please confirm with the user before sending data to SPI devices.")
	}

	dev, speed, mode, bits, errMsg := parseSPIArgs(args)
	if errMsg != "" {
		return ErrorResult(errMsg)
	}

	dataRaw, ok := args["data"].([]interface{})
	if !ok || len(dataRaw) == 0 {
		return ErrorResult("data is required for transfer (array of byte values 0-255)")
	}
	if len(dataRaw) > 4096 {
		return ErrorResult("data too long: maximum 4096 bytes per SPI transfer")
	}

	txBuf := make([]byte, len(dataRaw))
	for i, v := range dataRaw {
		f, ok := v.(float64)
		if !ok {
			return ErrorResult(fmt.Sprintf("data[%d] is not a valid byte value", i))
		}
		b := int(f)
		if b < 0 || b > 255 {
			return ErrorResult(fmt.Sprintf("data[%d] = %d is out of byte range (0-255)", i, b))
		}
		txBuf[i] = byte(b)
	}

	devPath := fmt.Sprintf("/dev/spidev%s", dev)
	fd, errResult := configureSPI(devPath, mode, bits, speed)
	if errResult != nil {
		return errResult
	}
	defer syscall.Close(fd)

	rxBuf := make([]byte, len(txBuf))

	xfer := spiTransfer{
		txBuf:       uint64(uintptr(unsafe.Pointer(&txBuf[0]))),
		rxBuf:       uint64(uintptr(unsafe.Pointer(&rxBuf[0]))),
		length:      uint32(len(txBuf)),
		speedHz:     speed,
		bitsPerWord: bits,
	}

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), spiIocMessage1, uintptr(unsafe.Pointer(&xfer)))
	runtime.KeepAlive(txBuf)
	runtime.KeepAlive(rxBuf)
	if errno != 0 {
		return ErrorResult(fmt.Sprintf("SPI transfer failed: %v", errno))
	}

	hexBytes := make([]string, len(rxBuf))
	intBytes := make([]int, len(rxBuf))
	for i, b := range rxBuf {
		hexBytes[i] = fmt.Sprintf("0x%02x", b)
		intBytes[i] = int(b)
	}

	result, _ := json.MarshalIndent(map[string]interface{}{
		"device":   devPath,
		"sent":     len(txBuf),
		"received": intBytes,
		"hex":      hexBytes,
	}, "", "  ")
	return SilentResult(string(result))
}

func (t *SPITool) readDevice(args map[string]interface{}) *ToolResult {
	dev, speed, mode, bits, errMsg := parseSPIArgs(args)
	if errMsg != "" {
		return ErrorResult(errMsg)
	}

	length := 0
	if l, ok := args["length"].(float64); ok {
		length = int(l)
	}
	if length < 1 || length > 4096 {
		return ErrorResult("length is required for read (1-4096)")
	}

	devPath := fmt.Sprintf("/dev/spidev%s", dev)
	fd, errResult := configureSPI(devPath, mode, bits, speed)
	if errResult != nil {
		return errResult
	}
	defer syscall.Close(fd)

	txBuf := make([]byte, length)
	rxBuf := make([]byte, length)

	xfer := spiTransfer{
		txBuf:       uint64(uintptr(unsafe.Pointer(&txBuf[0]))),
		rxBuf:       uint64(uintptr(unsafe.Pointer(&rxBuf[0]))),
		length:      uint32(length),
		speedHz:     speed,
		bitsPerWord: bits,
	}

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), spiIocMessage1, uintptr(unsafe.Pointer(&xfer)))
	runtime.KeepAlive(txBuf)
	runtime.KeepAlive(rxBuf)
	if errno != 0 {
		return ErrorResult(fmt.Sprintf("SPI read failed: %v", errno))
	}

	hexBytes := make([]string, len(rxBuf))
	intBytes := make([]int, len(rxBuf))
	for i, b := range rxBuf {
		hexBytes[i] = fmt.Sprintf("0x%02x", b)
		intBytes[i] = int(b)
	}

	result, _ := json.MarshalIndent(map[string]interface{}{
		"device": devPath,
		"bytes":  intBytes,
		"hex":    hexBytes,
		"length": len(rxBuf),
	}, "", "  ")
	return SilentResult(string(result))
}
