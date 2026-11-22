//go:build !linux

package tools

func (t *I2CTool) scan(args map[string]interface{}) *ToolResult {
	return ErrorResult("I2C is only supported on Linux")
}

func (t *I2CTool) readDevice(args map[string]interface{}) *ToolResult {
	return ErrorResult("I2C is only supported on Linux")
}

func (t *I2CTool) writeDevice(args map[string]interface{}) *ToolResult {
	return ErrorResult("I2C is only supported on Linux")
}
