package acp

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

// withTraceWriter temporarily overrides acpTraceWriter so tests can inspect
// emitted trace frames without depending on init() having processed
// GC_ACP_TRACE. Restores the previous value on cleanup.
func withTraceWriter(t *testing.T, w *bytes.Buffer) {
	t.Helper()
	prev := acpTraceWriter
	acpTraceWriter = w
	t.Cleanup(func() { acpTraceWriter = prev })
}

func TestTraceFrame_Disabled_WritesNothing(t *testing.T) {
	prev := acpTraceWriter
	acpTraceWriter = nil
	t.Cleanup(func() { acpTraceWriter = prev })

	// Must not panic or write anywhere when disabled.
	traceFrame(nil, "send", []byte(`{"jsonrpc":"2.0"}`))
}

func TestTraceFrame_Enabled_WritesFormattedLine(t *testing.T) {
	var buf bytes.Buffer
	withTraceWriter(t, &buf)

	traceFrame(nil, "send", []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	traceFrame(nil, "recv", []byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))

	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 trace lines, got %d: %q", len(lines), out)
	}
	for i, want := range []string{"send", "recv"} {
		if !strings.Contains(lines[i], " "+want+" session=") {
			t.Errorf("line %d missing %q direction marker: %s", i, want, lines[i])
		}
		if !strings.Contains(lines[i], "jsonrpc") {
			t.Errorf("line %d missing frame body: %s", i, lines[i])
		}
	}
}

func TestTraceFrame_UsesSessionIDFromConn(t *testing.T) {
	var buf bytes.Buffer
	withTraceWriter(t, &buf)

	sc := &sessionConn{sessionID: "sess-abc"}
	traceFrame(sc, "send", []byte(`{}`))

	if !strings.Contains(buf.String(), "session=sess-abc") {
		t.Fatalf("trace did not include sessionID: %q", buf.String())
	}
}

func TestTraceFrame_ConcurrentWritesAreSerialized(t *testing.T) {
	// Line interleaving would produce JSON on one line and garbage on the next;
	// detect by checking every non-empty line ends in a closing brace (the
	// frames written here all do).
	var buf bytes.Buffer
	withTraceWriter(t, &buf)

	var wg sync.WaitGroup
	const n = 50
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			traceFrame(nil, "send", []byte(`{"ok":true}`))
		}(i)
	}
	wg.Wait()

	for i, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		if !strings.HasSuffix(line, `{"ok":true}`) {
			t.Fatalf("line %d not cleanly terminated (suggests interleaving): %q", i, line)
		}
	}
}
