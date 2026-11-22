package mcp

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func fenceMarker(trimmed string) (n int, info string, ok bool) {
	if !strings.HasPrefix(trimmed, "```") {
		return 0, "", false
	}
	for n < len(trimmed) && trimmed[n] == '`' {
		n++
	}
	return n, strings.TrimSpace(trimmed[n:]), true
}

func jsonFencedBlocks(doc string) []string {
	var (
		blocks   []string
		cur      []string
		open     bool
		openLen  int
		openJSON bool
	)
	for _, line := range strings.Split(doc, "\n") {
		n, info, isFence := fenceMarker(strings.TrimSpace(line))
		switch {
		case !open && isFence:
			open, openLen, openJSON = true, n, info == "json"
			cur = nil
		case open && isFence && info == "" && n >= openLen:
			if openJSON {
				blocks = append(blocks, strings.Join(cur, "\n"))
			}
			open, openJSON = false, false
		case open:
			cur = append(cur, line)
		}
	}
	return blocks
}

func TestDocsMCPExamplesAreValidMCPConfigs(t *testing.T) {
	const docPath = "../../docs/mcp.md"
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}

	blocks := jsonFencedBlocks(string(data))
	if len(blocks) < 4 {
		t.Fatalf("%s: found %d ```json blocks, want at least the 4 documented examples", docPath, len(blocks))
	}

	var (
		shapeStdio  bool
		shapeHTTP   bool
		shapeSSE    bool
		shapeDisabl bool
		validBlocks int
	)
	for i, block := range blocks {
		var f File
		if err := json.Unmarshal([]byte(block), &f); err != nil {
			t.Errorf("json block %d: not valid JSON for mcp.json: %v", i+1, err)
			continue
		}
		if len(f.MCPServers) == 0 {
			t.Errorf("json block %d: no mcpServers entries — every ```json block in %s must be an mcp.json example:\n%s",
				i+1, docPath, block)
			continue
		}
		f.validate()
		if len(f.EntryErrors) > 0 {
			for name, e := range f.EntryErrors {
				t.Errorf("json block %d: invalid entry %q: %v", i+1, name, e)
			}
			continue
		}
		validBlocks++
		for name, srv := range f.MCPServers {
			switch {
			case srv.Command != "":
				shapeStdio = true
			case strings.EqualFold(srv.Type, typeSSE):
				shapeSSE = true
			case strings.EqualFold(srv.Type, typeHTTP):
				shapeHTTP = true
			default:
				t.Errorf("json block %d: entry %q is neither stdio nor remote", i+1, name)
			}
			if srv.Disabled {
				shapeDisabl = true
			}
		}
	}

	if validBlocks < 4 {
		t.Errorf("valid mcp.json example blocks = %d, want >= 4", validBlocks)
	}
	if !shapeStdio {
		t.Error("docs/mcp.md lacks a stdio example (command)")
	}
	if !shapeHTTP {
		t.Error("docs/mcp.md lacks a remote http example (url)")
	}
	if !shapeSSE {
		t.Error("docs/mcp.md lacks an sse example (type: sse)")
	}
	if !shapeDisabl {
		t.Error("docs/mcp.md lacks a disabled example (disabled: true)")
	}
}
