//go:build !windows

package beads

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestExecCommandRunnerDetectsAutoImportOnExitZero reproduces the ga-9hd
// "evaporating writes" failure mode.
//
// When bd cannot reach the managed Dolt server it silently falls back to
// opening the on-disk .beads/dolt/ store, sees it as "empty", and auto-imports
// JSONL into an ephemeral database. Any writes performed against that
// ephemeral database are later overwritten by the next call's auto-import,
// so they evaporate.
//
// bd emits the auto-import markers to stderr and, on the happy path, exits
// zero. Before the fix, ExecCommandRunnerWithEnv discarded stderr entirely
// on exit-zero, so the runner returned (out, nil) and the retry path in
// bdCommandRunnerWithManagedRetry never triggered.
//
// After the fix, the runner must surface stderr as a non-nil error whenever
// the auto-import markers are present so the existing transport-retry
// machinery recovers the managed Dolt port and reruns the command against
// the live server.
func TestExecCommandRunnerDetectsAutoImportOnExitZero(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh unavailable")
	}

	dir := t.TempDir()
	fakeBd := filepath.Join(dir, "bd")
	// Fake bd writes the auto-import markers to stderr and exits 0 — the
	// exact shape of the evaporation bug on the success-looking code path.
	script := `#!/bin/sh
echo 'auto-importing 857907 bytes from /tmp/.beads/issues.jsonl into empty database...' >&2
echo 'auto-imported 1475 issues from /tmp/.beads/issues.jsonl' >&2
echo '{"ok":true}'
exit 0
`
	if err := os.WriteFile(fakeBd, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake bd: %v", err)
	}
	// exec resolves the binary against the parent process PATH at
	// construction time, so we have to prepend the fake dir here rather
	// than relying on cmd.Env.
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	out, err := ExecCommandRunnerWithEnv(nil)(dir, "bd", "show", "ga-9hd")
	if err == nil {
		t.Fatalf("runner silently accepted auto-import fallback; want non-nil error so retry path triggers.\nstdout: %s", string(out))
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "auto-importing") || !strings.Contains(msg, "into empty database") {
		t.Fatalf("error does not expose auto-import markers for retry matcher: %v", err)
	}
}

// TestExecCommandRunnerDoesNotFlagNonBdAutoImportOnStderr guards against
// over-matching. The auto-import sniff must only apply to bd — other
// commands that happen to print those words must still return success.
func TestExecCommandRunnerDoesNotFlagNonBdAutoImportOnStderr(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh unavailable")
	}

	dir := t.TempDir()
	fakeTool := filepath.Join(dir, "not-bd")
	script := `#!/bin/sh
echo 'auto-importing 100 bytes into empty database' >&2
exit 0
`
	if err := os.WriteFile(fakeTool, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tool: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := ExecCommandRunnerWithEnv(nil)(dir, "not-bd")
	if err != nil {
		t.Fatalf("non-bd command should not be flagged: %v", err)
	}
}

// TestExecCommandRunnerAutoImportDetectionEscapeHatch verifies that
// setting GC_BD_AUTOIMPORT_DETECT=0 disables the synthesized error so
// operators can unblock work in environments where bd's auto-import
// banner is benign (e.g. shared rig/city Dolt servers). The stderr
// banner is still visible to telemetry and tracing, but the runner
// returns (out, nil) matching bd's own exit code.
func TestExecCommandRunnerAutoImportDetectionEscapeHatch(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh unavailable")
	}

	dir := t.TempDir()
	fakeBd := filepath.Join(dir, "bd")
	script := `#!/bin/sh
echo 'auto-importing 100 bytes from /tmp/.beads/issues.jsonl into empty database' >&2
echo '{"ok":true}'
exit 0
`
	if err := os.WriteFile(fakeBd, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake bd: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	for _, disableValue := range []string{"0", "false", "off", "no", "FALSE", "Off"} {
		t.Run("disable="+disableValue, func(t *testing.T) {
			t.Setenv("GC_BD_AUTOIMPORT_DETECT", disableValue)
			out, err := ExecCommandRunnerWithEnv(nil)(dir, "bd", "show")
			if err != nil {
				t.Fatalf("escape hatch %q did not disable detector: %v", disableValue, err)
			}
			if !strings.Contains(string(out), "ok") {
				t.Fatalf("stdout lost: %q", string(out))
			}
		})
	}
}
