//go:build !linux

package tools

func (t *SPITool) transfer(args map[string]interface{}) *ToolResult {
	return ErrorResult("SPI is only supported on Linux")
}

func (t *SPITool) readDevice(args map[string]interface{}) *ToolResult {
	return ErrorResult("SPI is only supported on Linux")
}
