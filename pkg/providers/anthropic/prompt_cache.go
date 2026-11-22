package anthropicprovider

import (
	"github.com/anthropics/anthropic-sdk-go"
)

func sdkCacheControl(ttl string) anthropic.CacheControlEphemeralParam {
	cc := anthropic.NewCacheControlEphemeralParam()
	if ttl == "1h" {
		cc.TTL = anthropic.CacheControlEphemeralTTLTTL1h
	} else {
		cc.TTL = anthropic.CacheControlEphemeralTTLTTL5m
	}
	return cc
}

func applyCacheBreakpoints(params *anthropic.MessageNewParams, ttl string) {
	cc := sdkCacheControl(ttl)

	if n := len(params.System); n > 0 {
		params.System[n-1].CacheControl = cc
	}

	for i := len(params.Tools) - 1; i >= 0; i-- {
		if t := params.Tools[i].OfTool; t != nil {
			t.CacheControl = cc
			break
		}
	}

	if n := len(params.Messages); n > 0 {
		blocks := params.Messages[n-1].Content
		for i := len(blocks) - 1; i >= 0; i-- {
			if tb := blocks[i].OfText; tb != nil {
				tb.CacheControl = cc
				break
			}
		}
	}
}
