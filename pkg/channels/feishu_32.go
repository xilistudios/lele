//go:build !amd64 && !arm64 && !riscv64 && !mips64 && !ppc64

package channels

import (
	"context"
	"errors"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

type FeishuChannel struct {
	*BaseChannel
}

func NewFeishuChannel(cfg config.FeishuConfig, bus *bus.MessageBus) (*FeishuChannel, error) {
	return nil, errors.New("feishu channel is not supported on 32-bit architectures (armv7l, 386, etc.). Please use a 64-bit system or disable feishu in your config")
}

func (c *FeishuChannel) Start(ctx context.Context) error {
	return nil
}

func (c *FeishuChannel) Stop(ctx context.Context) error {
	return nil
}

func (c *FeishuChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	return errors.New("feishu channel is not supported on 32-bit architectures")
}
