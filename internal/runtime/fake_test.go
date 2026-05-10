package runtime

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Compile-time check: Fake implements Provider.
var _ Provider = (*Fake)(nil)

func TestFake_StartStop(t *testing.T) {
	f := NewFake()

	if err := f.Start(context.Background(), "mayor", Config{WorkDir: "/tmp"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !f.IsRunning("mayor") {
		t.Fatal("expected mayor to be running after Start")
	}

	// Duplicate start should fail.
	if err := f.Start(context.Background(), "mayor", Config{}); err == nil {
		t.Fatal("expected error on duplicate Start")
	}

	if err := f.Stop("mayor"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if f.IsRunning("mayor") {
		t.Fatal("expected mayor to not be running after Stop")
	}

	// Idempotent stop.
	if err := f.Stop("mayor"); err != nil {
		t.Fatalf("idempotent Stop: %v", err)
	}
}

func TestFake_Attach(t *testing.T) {
	f := NewFake()

	// Attach to nonexistent session.
	if err := f.Attach("ghost"); err == nil {
		t.Fatal("expected error attaching to nonexistent session")
	}

	_ = f.Start(context.Background(), "mayor", Config{})
	if err := f.Attach("mayor"); err != nil {
		t.Fatalf("Attach: %v", err)
	}
}

func TestFailFake_AllOpsFail(t *testing.T) {
	f := NewFailFake()

	if err := f.Start(context.Background(), "mayor", Config{WorkDir: "/tmp"}); err == nil {
		t.Fatal("expected Start to fail on broken fake")
	}
	if f.IsRunning("mayor") {
		t.Fatal("expected IsRunning to return false on broken fake")
	}
	if err := f.Attach("mayor"); err == nil {
		t.Fatal("expected Attach to fail on broken fake")
	}
	if err := f.Stop("mayor"); err == nil {
		t.Fatal("expected Stop to fail on broken fake")
	}
}

func TestFailFake_RecordsCalls(t *testing.T) {
	f := NewFailFake()

	_ = f.Start(context.Background(), "a", Config{})
	f.IsRunning("a")
	_ = f.Attach("a")
	_ = f.Stop("a")

	want := []string{"Start", "IsRunning", "Attach", "Stop"}
	if len(f.Calls) != len(want) {
		t.Fatalf("got %d calls, want %d", len(f.Calls), len(want))
	}
	for i, c := range f.Calls {
		if c.Method != want[i] {
			t.Errorf("call %d: got %q, want %q", i, c.Method, want[i])
		}
	}
}

func TestFake_SpyRecordsCalls(t *testing.T) {
	f := NewFake()

	_ = f.Start(context.Background(), "a", Config{WorkDir: "/w"})
	f.IsRunning("a")
	_ = f.Attach("a")
	_ = f.Stop("a")

	want := []string{"Start", "IsRunning", "Attach", "Stop"}
	if len(f.Calls) != len(want) {
		t.Fatalf("got %d calls, want %d", len(f.Calls), len(want))
	}
	for i, c := range f.Calls {
		if c.Method != want[i] {
			t.Errorf("call %d: got %q, want %q", i, c.Method, want[i])
		}
		if c.Name != "a" {
			t.Errorf("call %d: got name %q, want %q", i, c.Name, "a")
		}
	}

	// Verify config was captured on Start.
	if f.Calls[0].Config.WorkDir != "/w" {
		t.Errorf("Start config WorkDir: got %q, want %q", f.Calls[0].Config.WorkDir, "/w")
	}
}

func TestFake_CapturesAllConfigFields(t *testing.T) {
	f := NewFake()

	cfg := Config{
		WorkDir:                "/proj",
		Command:                "claude --dangerously-skip-permissions",
		Env:                    map[string]string{"GC_AGENT": "mayor", "HOME": "/home/user"},
		ReadyPromptPrefix:      "❯ ",
		ReadyDelayMs:           10000,
		ProcessNames:           []string{"claude", "node"},
		EmitsPermissionWarning: true,
	}
	if err := f.Start(context.Background(), "mayor", cfg); err != nil {
		t.Fatalf("Start: %v", err)
	}

	got := f.Calls[0].Config
	if got.WorkDir != "/proj" {
		t.Errorf("WorkDir = %q, want %q", got.WorkDir, "/proj")
	}
	if got.Command != "claude --dangerously-skip-permissions" {
		t.Errorf("Command = %q, want %q", got.Command, "claude --dangerously-skip-permissions")
	}
	if got.Env["GC_AGENT"] != "mayor" {
		t.Errorf("Env[GC_AGENT] = %q, want %q", got.Env["GC_AGENT"], "mayor")
	}
	if got.Env["HOME"] != "/home/user" {
		t.Errorf("Env[HOME] = %q, want %q", got.Env["HOME"], "/home/user")
	}
	if got.ReadyPromptPrefix != "❯ " {
		t.Errorf("ReadyPromptPrefix = %q, want %q", got.ReadyPromptPrefix, "❯ ")
	}
	if got.ReadyDelayMs != 10000 {
		t.Errorf("ReadyDelayMs = %d, want %d", got.ReadyDelayMs, 10000)
	}
	if len(got.ProcessNames) != 2 || got.ProcessNames[0] != "claude" || got.ProcessNames[1] != "node" {
		t.Errorf("ProcessNames = %v, want [claude node]", got.ProcessNames)
	}
	if !got.EmitsPermissionWarning {
		t.Error("EmitsPermissionWarning = false, want true")
	}
}

func TestFakeProcessAliveDefault(t *testing.T) {
	f := NewFake()
	_ = f.Start(context.Background(), "mayor", Config{})

	if !f.ProcessAlive("mayor", []string{"claude"}) {
		t.Error("ProcessAlive = false for healthy session, want true")
	}
}

func TestFakeProcessAliveZombie(t *testing.T) {
	f := NewFake()
	_ = f.Start(context.Background(), "mayor", Config{})
	f.Zombies["mayor"] = true

	if f.ProcessAlive("mayor", []string{"claude"}) {
		t.Error("ProcessAlive = true for zombie, want false")
	}
}

func TestFakeProcessAliveEmptyNames(t *testing.T) {
	f := NewFake()
	_ = f.Start(context.Background(), "mayor", Config{})
	f.Zombies["mayor"] = true // zombie, but no names to check

	if !f.ProcessAlive("mayor", nil) {
		t.Error("ProcessAlive = false with empty names, want true")
	}
}

func TestFakeProcessAliveBroken(t *testing.T) {
	f := NewFailFake()

	if f.ProcessAlive("mayor", []string{"claude"}) {
		t.Error("ProcessAlive = true on broken fake, want false")
	}
}

func TestFakeNudge(t *testing.T) {
	f := NewFake()
	_ = f.Start(context.Background(), "mayor", Config{})

	if err := f.Nudge("mayor", TextContent("wake up")); err != nil {
		t.Fatalf("Nudge: %v", err)
	}

	// Find the Nudge call.
	var found bool
	for _, c := range f.Calls {
		if c.Method == "Nudge" {
			found = true
			if c.Name != "mayor" {
				t.Errorf("Nudge Name = %q, want %q", c.Name, "mayor")
			}
			if c.Message != "wake up" {
				t.Errorf("Nudge Message = %q, want %q", c.Message, "wake up")
			}
			if len(c.Content) != 1 || c.Content[0].Type != "text" || c.Content[0].Text != "wake up" {
				t.Errorf("Nudge Content = %v, want single text block", c.Content)
			}
		}
	}
	if !found {
		t.Error("Nudge call not recorded")
	}
}

func TestFakeNudgeBroken(t *testing.T) {
	f := NewFailFake()

	err := f.Nudge("mayor", TextContent("wake up"))
	if err == nil {
		t.Fatal("expected Nudge to fail on broken fake")
	}

	// Call should still be recorded.
	var found bool
	for _, c := range f.Calls {
		if c.Method == "Nudge" {
			found = true
		}
	}
	if !found {
		t.Error("Nudge call not recorded on broken fake")
	}
}

func TestFakeSetGetMeta(t *testing.T) {
	f := NewFake()
	_ = f.Start(context.Background(), "mayor", Config{})

	if err := f.SetMeta("mayor", "GC_DRAIN", "123"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	val, err := f.GetMeta("mayor", "GC_DRAIN")
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if val != "123" {
		t.Errorf("GetMeta = %q, want %q", val, "123")
	}
}

func TestFakeGetMetaUnset(t *testing.T) {
	f := NewFake()
	val, err := f.GetMeta("mayor", "GC_DRAIN")
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if val != "" {
		t.Errorf("GetMeta unset key = %q, want empty", val)
	}
}

func TestFakeRemoveMeta(t *testing.T) {
	f := NewFake()
	_ = f.SetMeta("mayor", "GC_DRAIN", "123")
	if err := f.RemoveMeta("mayor", "GC_DRAIN"); err != nil {
		t.Fatalf("RemoveMeta: %v", err)
	}
	val, _ := f.GetMeta("mayor", "GC_DRAIN")
	if val != "" {
		t.Errorf("GetMeta after remove = %q, want empty", val)
	}
}

func TestFakeListRunning(t *testing.T) {
	f := NewFake()
	_ = f.Start(context.Background(), "gc-city-mayor", Config{})
	_ = f.Start(context.Background(), "gc-city-worker", Config{})
	_ = f.Start(context.Background(), "gc-other-agent", Config{})

	names, err := f.ListRunning("gc-city-")
	if err != nil {
		t.Fatalf("ListRunning: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("ListRunning = %v, want 2 sessions", names)
	}
}

func TestFakePeek(t *testing.T) {
	f := NewFake()
	_ = f.Start(context.Background(), "mayor", Config{})
	f.SetPeekOutput("mayor", "line1\nline2\n")

	output, err := f.Peek("mayor", 50)
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	if output != "line1\nline2\n" {
		t.Errorf("Peek output = %q, want %q", output, "line1\nline2\n")
	}

	// Verify call was recorded.
	var found bool
	for _, c := range f.Calls {
		if c.Method == "Peek" {
			found = true
			if c.Name != "mayor" {
				t.Errorf("Peek Name = %q, want %q", c.Name, "mayor")
			}
		}
	}
	if !found {
		t.Error("Peek call not recorded")
	}
}

func TestFakePeekNoOutput(t *testing.T) {
	f := NewFake()

	output, err := f.Peek("ghost", 50)
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	if output != "" {
		t.Errorf("Peek output = %q, want empty", output)
	}
}

func TestFakePeekBroken(t *testing.T) {
	f := NewFailFake()

	_, err := f.Peek("mayor", 50)
	if err == nil {
		t.Fatal("expected Peek to fail on broken fake")
	}

	// Call should still be recorded.
	var found bool
	for _, c := range f.Calls {
		if c.Method == "Peek" {
			found = true
		}
	}
	if !found {
		t.Error("Peek call not recorded on broken fake")
	}
}

func TestFakeMetaBroken(t *testing.T) {
	f := NewFailFake()

	if err := f.SetMeta("mayor", "k", "v"); err == nil {
		t.Error("SetMeta should fail on broken fake")
	}
	if _, err := f.GetMeta("mayor", "k"); err == nil {
		t.Error("GetMeta should fail on broken fake")
	}
	if err := f.RemoveMeta("mayor", "k"); err == nil {
		t.Error("RemoveMeta should fail on broken fake")
	}
	if _, err := f.ListRunning("gc-"); err == nil {
		t.Error("ListRunning should fail on broken fake")
	}
}

func TestFakePermissionModeStateReadAndCapability(t *testing.T) {
	f := NewFake()
	if err := f.Start(context.Background(), "session-a", Config{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	f.SetPermissionModeState("session-a", PermissionModeState{
		Mode:    PermissionModeDefault,
		Version: 7,
	})

	capability := f.PermissionModeCapability("session-a", "claude")
	if !capability.Supported || !capability.Readable || !capability.LiveSwitch {
		t.Fatalf("PermissionModeCapability = %+v, want fully supported", capability)
	}
	if len(capability.Values) != len(CanonicalPermissionModes()) {
		t.Fatalf("PermissionModeCapability.Values = %v, want canonical values", capability.Values)
	}
	capability.Values[0] = PermissionMode("mutated")
	if fresh := f.PermissionModeCapability("session-a", "claude").Values[0]; fresh != PermissionModeDefault {
		t.Fatalf("PermissionModeCapability returned shared values; got %q", fresh)
	}

	state, err := f.PermissionMode(context.Background(), "session-a", "claude")
	if err != nil {
		t.Fatalf("PermissionMode: %v", err)
	}
	if state.Mode != PermissionModeDefault || state.Version != 7 || !state.Verified {
		t.Fatalf("PermissionMode = %+v, want default version 7 verified", state)
	}
}

func TestFakePermissionModeUnsupportedAndReadErrors(t *testing.T) {
	f := NewFake()
	if capability := f.PermissionModeCapability("missing", "claude"); capability.Supported || capability.Reason == "" {
		t.Fatalf("missing capability = %+v, want unsupported reason", capability)
	}
	if _, err := f.PermissionMode(context.Background(), "missing", "claude"); !errors.Is(err, ErrPermissionModeUnsupported) {
		t.Fatalf("PermissionMode missing error = %v, want %v", err, ErrPermissionModeUnsupported)
	}

	readErr := errors.New("read failed")
	f.PermissionModeReadErrors["session-a"] = readErr
	if capability := f.PermissionModeCapability("session-a", "claude"); capability.Reason != readErr.Error() {
		t.Fatalf("read error capability = %+v, want reason %q", capability, readErr.Error())
	}
	if _, err := f.PermissionMode(context.Background(), "session-a", "claude"); !errors.Is(err, readErr) {
		t.Fatalf("PermissionMode read error = %v, want %v", err, readErr)
	}

	delete(f.PermissionModeReadErrors, "session-a")
	f.SetPermissionModeState("session-a", PermissionModeState{Mode: PermissionModePlan})
	f.SetPermissionModeCapability("session-a", PermissionModeCapability{Supported: true, Readable: false})
	if _, err := f.PermissionMode(context.Background(), "session-a", "claude"); !errors.Is(err, ErrPermissionModeUnsupported) {
		t.Fatalf("PermissionMode unreadable error = %v, want %v", err, ErrPermissionModeUnsupported)
	}
}

func TestFakeSetPermissionModeRequiresRunningSession(t *testing.T) {
	f := NewFake()
	_, err := f.SetPermissionMode(context.Background(), "missing", "claude", PermissionModeAcceptEdits)
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("SetPermissionMode missing error = %v, want %v", err, ErrSessionNotFound)
	}
}

func TestFakeSetPermissionModeUpdatesModeAndVersion(t *testing.T) {
	f := NewFake()
	if err := f.Start(context.Background(), "session-a", Config{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	f.SetPermissionModeState("session-a", PermissionModeState{
		Mode:    PermissionModeDefault,
		Version: 3,
	})

	state, err := f.SetPermissionMode(context.Background(), "session-a", "claude", PermissionModeAcceptEdits)
	if err != nil {
		t.Fatalf("SetPermissionMode: %v", err)
	}
	if state.Mode != PermissionModeAcceptEdits || state.Version != 4 || !state.Verified {
		t.Fatalf("SetPermissionMode = %+v, want acceptEdits version 4 verified", state)
	}
	read, err := f.PermissionMode(context.Background(), "session-a", "claude")
	if err != nil {
		t.Fatalf("PermissionMode after set: %v", err)
	}
	if read.Mode != PermissionModeAcceptEdits || read.Version != 4 {
		t.Fatalf("PermissionMode after set = %+v, want acceptEdits version 4", read)
	}
}

func TestFakeSetPermissionModeErrors(t *testing.T) {
	f := NewFake()
	if err := f.Start(context.Background(), "session-a", Config{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	f.SetPermissionModeState("session-a", PermissionModeState{Mode: PermissionModeDefault})

	setErr := errors.New("set failed")
	f.PermissionModeSetErrors["session-a"] = setErr
	if _, err := f.SetPermissionMode(context.Background(), "session-a", "claude", PermissionModePlan); !errors.Is(err, setErr) {
		t.Fatalf("SetPermissionMode set error = %v, want %v", err, setErr)
	}

	delete(f.PermissionModeSetErrors, "session-a")
	f.SetPermissionModeCapability("session-a", PermissionModeCapability{Supported: false})
	if _, err := f.SetPermissionMode(context.Background(), "session-a", "claude", PermissionModePlan); !errors.Is(err, ErrPermissionModeUnsupported) {
		t.Fatalf("SetPermissionMode unsupported error = %v, want %v", err, ErrPermissionModeUnsupported)
	}

	f.SetPermissionModeCapability("session-a", PermissionModeCapability{Supported: true, LiveSwitch: false})
	if _, err := f.SetPermissionMode(context.Background(), "session-a", "claude", PermissionModePlan); !errors.Is(err, ErrPermissionModeSwitchUnsupported) {
		t.Fatalf("SetPermissionMode live switch error = %v, want %v", err, ErrPermissionModeSwitchUnsupported)
	}
}

func TestFakeSetPermissionModeWithoutConfiguredStateIsUnsupported(t *testing.T) {
	f := NewFake()
	if err := f.Start(context.Background(), "session-a", Config{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := f.SetPermissionMode(context.Background(), "session-a", "claude", PermissionModePlan); !errors.Is(err, ErrPermissionModeUnsupported) {
		t.Fatalf("SetPermissionMode without state error = %v, want %v", err, ErrPermissionModeUnsupported)
	}
}

func TestFakePermissionModeZeroValueMapInitialization(t *testing.T) {
	stateful := &Fake{}
	stateful.SetPermissionModeState("session-a", PermissionModeState{
		Mode:    PermissionModeDefault,
		Version: 2,
	})
	if stateful.PermissionModes["session-a"] != PermissionModeDefault {
		t.Fatalf("zero-value SetPermissionModeState mode = %q, want default", stateful.PermissionModes["session-a"])
	}
	if stateful.PermissionModeVersions["session-a"] != 2 {
		t.Fatalf("zero-value SetPermissionModeState version = %d, want 2", stateful.PermissionModeVersions["session-a"])
	}
	if capability := stateful.PermissionModeCaps["session-a"]; !capability.Supported || !capability.LiveSwitch {
		t.Fatalf("zero-value SetPermissionModeState capability = %+v, want supported live switch", capability)
	}

	capabilityOnly := &Fake{}
	capabilityOnly.SetPermissionModeCapability("session-a", PermissionModeCapability{Supported: true})
	if !capabilityOnly.PermissionModeCaps["session-a"].Supported {
		t.Fatal("zero-value SetPermissionModeCapability did not initialize capability map")
	}

	capabilityFromMode := &Fake{PermissionModes: map[string]PermissionMode{"session-a": PermissionModePlan}}
	capability := capabilityFromMode.PermissionModeCapability("session-a", "claude")
	if !capability.Supported || !capability.Readable || !capability.LiveSwitch {
		t.Fatalf("PermissionModeCapability from configured mode = %+v, want supported", capability)
	}

	switcher := &Fake{
		sessions: map[string]Config{"session-a": {}},
		PermissionModeCaps: map[string]PermissionModeCapability{
			"session-a": {Supported: true, LiveSwitch: true},
		},
	}
	switched, err := switcher.SetPermissionMode(context.Background(), "session-a", "claude", PermissionModeAcceptEdits)
	if err != nil {
		t.Fatalf("zero-value SetPermissionMode: %v", err)
	}
	if switched.Mode != PermissionModeAcceptEdits || switched.Version != 1 || !switched.Verified {
		t.Fatalf("zero-value SetPermissionMode = %+v, want acceptEdits version 1 verified", switched)
	}

	statefulSwitcher := &Fake{sessions: map[string]Config{"session-a": {}}}
	fromState, err := statefulSwitcher.SetPermissionModeFromState(
		context.Background(),
		"session-a",
		"claude",
		PermissionModeDefault,
		PermissionModeBypassPermissions,
	)
	if err != nil {
		t.Fatalf("zero-value SetPermissionModeFromState: %v", err)
	}
	if fromState.Mode != PermissionModeBypassPermissions || fromState.Version != 1 || fromState.Verified {
		t.Fatalf("zero-value SetPermissionModeFromState = %+v, want bypassPermissions version 1 unverified", fromState)
	}
}

func TestFakePermissionModeCapabilityForState(t *testing.T) {
	f := NewFake()
	if capability := f.PermissionModeCapabilityForState("missing", "claude", PermissionModeDefault); capability.Supported || capability.Reason == "" {
		t.Fatalf("missing stateful capability = %+v, want unsupported reason", capability)
	}

	if err := f.Start(context.Background(), "session-a", Config{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	capability := f.PermissionModeCapabilityForState("session-a", "claude", PermissionModeDefault)
	if !capability.Supported || !capability.Readable || !capability.LiveSwitch {
		t.Fatalf("PermissionModeCapabilityForState = %+v, want supported", capability)
	}
	if len(capability.Values) != len(CanonicalPermissionModes()) {
		t.Fatalf("PermissionModeCapabilityForState.Values = %v, want canonical values", capability.Values)
	}

	f.SetPermissionModeCapability("session-a", PermissionModeCapability{Supported: true, LiveSwitch: false, Reason: "read only"})
	if capability := f.PermissionModeCapabilityForState("session-a", "claude", PermissionModeDefault); capability.LiveSwitch || capability.Reason != "read only" {
		t.Fatalf("read-only stateful capability = %+v, want live switch false with reason", capability)
	}

	delete(f.PermissionModeCaps, "session-a")
	if capability := f.PermissionModeCapabilityForState("session-a", "claude", PermissionMode("unknown")); capability.Supported || capability.Reason == "" {
		t.Fatalf("unknown current capability = %+v, want unsupported reason", capability)
	}
}

func TestFakeSetPermissionModeFromStateUpdatesUnverified(t *testing.T) {
	f := NewFake()
	if err := f.Start(context.Background(), "session-a", Config{}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	state, err := f.SetPermissionModeFromState(
		context.Background(),
		"session-a",
		"claude",
		PermissionModeDefault,
		PermissionModePlan,
	)
	if err != nil {
		t.Fatalf("SetPermissionModeFromState: %v", err)
	}
	if state.Mode != PermissionModePlan || state.Version != 1 || state.Verified {
		t.Fatalf("SetPermissionModeFromState = %+v, want plan version 1 unverified", state)
	}
}

func TestFakeSetPermissionModeFromStateErrors(t *testing.T) {
	f := NewFake()
	if _, err := f.SetPermissionModeFromState(context.Background(), "missing", "claude", PermissionModeDefault, PermissionModePlan); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("SetPermissionModeFromState missing error = %v, want %v", err, ErrSessionNotFound)
	}
	if err := f.Start(context.Background(), "session-a", Config{}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	setErr := errors.New("stateful set failed")
	f.PermissionModeSetErrors["session-a"] = setErr
	if _, err := f.SetPermissionModeFromState(context.Background(), "session-a", "claude", PermissionModeDefault, PermissionModePlan); !errors.Is(err, setErr) {
		t.Fatalf("SetPermissionModeFromState set error = %v, want %v", err, setErr)
	}

	delete(f.PermissionModeSetErrors, "session-a")
	f.SetPermissionModeCapability("session-a", PermissionModeCapability{Supported: false})
	if _, err := f.SetPermissionModeFromState(context.Background(), "session-a", "claude", PermissionModeDefault, PermissionModePlan); !errors.Is(err, ErrPermissionModeUnsupported) {
		t.Fatalf("SetPermissionModeFromState unsupported error = %v, want %v", err, ErrPermissionModeUnsupported)
	}

	f.SetPermissionModeCapability("session-a", PermissionModeCapability{Supported: true, LiveSwitch: false})
	if _, err := f.SetPermissionModeFromState(context.Background(), "session-a", "claude", PermissionModeDefault, PermissionModePlan); !errors.Is(err, ErrPermissionModeSwitchUnsupported) {
		t.Fatalf("SetPermissionModeFromState live switch error = %v, want %v", err, ErrPermissionModeSwitchUnsupported)
	}

	delete(f.PermissionModeCaps, "session-a")
	if _, err := f.SetPermissionModeFromState(context.Background(), "session-a", "claude", PermissionMode("unknown"), PermissionModePlan); !errors.Is(err, ErrPermissionModeSwitchUnsupported) {
		t.Fatalf("SetPermissionModeFromState unknown current error = %v, want %v", err, ErrPermissionModeSwitchUnsupported)
	}
}

func TestFailFakePermissionModeOperationsFailAndRecordCalls(t *testing.T) {
	f := NewFailFake()

	if capability := f.PermissionModeCapability("session-a", "claude"); capability.Supported || capability.Reason == "" {
		t.Fatalf("broken PermissionModeCapability = %+v, want unsupported reason", capability)
	}
	if _, err := f.PermissionMode(context.Background(), "session-a", "claude"); err == nil {
		t.Fatal("PermissionMode on broken fake succeeded, want error")
	}
	if _, err := f.SetPermissionMode(context.Background(), "session-a", "claude", PermissionModePlan); err == nil {
		t.Fatal("SetPermissionMode on broken fake succeeded, want error")
	}
	if capability := f.PermissionModeCapabilityForState("session-a", "claude", PermissionModeDefault); capability.Supported || capability.Reason == "" {
		t.Fatalf("broken PermissionModeCapabilityForState = %+v, want unsupported reason", capability)
	}
	if _, err := f.SetPermissionModeFromState(context.Background(), "session-a", "claude", PermissionModeDefault, PermissionModePlan); err == nil {
		t.Fatal("SetPermissionModeFromState on broken fake succeeded, want error")
	}

	want := []string{
		"PermissionModeCapability",
		"PermissionMode",
		"SetPermissionMode",
		"PermissionModeCapabilityForState",
		"SetPermissionModeFromState",
	}
	if len(f.Calls) != len(want) {
		t.Fatalf("recorded calls = %v, want %v", f.Calls, want)
	}
	for i, method := range want {
		if f.Calls[i].Method != method {
			t.Fatalf("call %d method = %q, want %q", i, f.Calls[i].Method, method)
		}
	}
}

func TestTextContent(t *testing.T) {
	blocks := TextContent("hello")
	if len(blocks) != 1 {
		t.Fatalf("len = %d, want 1", len(blocks))
	}
	if blocks[0].Type != "text" || blocks[0].Text != "hello" {
		t.Errorf("block = %+v, want text=hello", blocks[0])
	}
}

func TestFlattenText(t *testing.T) {
	blocks := []ContentBlock{
		{Type: "text", Text: "hello"},
		{Type: "file_path", Path: "/some/dir/readme.md"},
		{Type: "text", Text: "world"},
	}
	got := FlattenText(blocks)
	want := "hello\n[File: readme.md]\nworld"
	if got != want {
		t.Errorf("FlattenText = %q, want %q", got, want)
	}
}

func TestFlattenText_Empty(t *testing.T) {
	if got := FlattenText(nil); got != "" {
		t.Errorf("FlattenText(nil) = %q, want empty", got)
	}
	if got := FlattenText([]ContentBlock{{Type: "text"}}); got != "" {
		t.Errorf("FlattenText(empty text) = %q, want empty", got)
	}
}

func TestFakeWaitForIdleGate_BlocksUntilClosed(t *testing.T) {
	f := NewFake()
	f.WaitForIdleErrors["s1"] = nil
	gate := make(chan struct{})
	f.WaitForIdleGates["s1"] = gate

	done := make(chan error, 1)
	go func() {
		done <- f.WaitForIdle(context.Background(), "s1", time.Second)
	}()

	select {
	case <-done:
		t.Fatal("WaitForIdle returned before gate closed")
	case <-time.After(50 * time.Millisecond):
	}

	close(gate)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitForIdle returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitForIdle did not return after gate closed")
	}
}

func TestFakeWaitForIdleGate_RespectsContextCancel(t *testing.T) {
	f := NewFake()
	f.WaitForIdleErrors["s1"] = nil
	f.WaitForIdleGates["s1"] = make(chan struct{}) // never closed

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- f.WaitForIdle(ctx, "s1", time.Second)
	}()

	select {
	case <-done:
		t.Fatal("WaitForIdle returned before cancel")
	case <-time.After(50 * time.Millisecond):
	}

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("WaitForIdle error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitForIdle did not return after context cancel")
	}
}

func TestFakeWaitForIdleGate_MuReleasedWhileBlocked(t *testing.T) {
	f := NewFake()
	_ = f.Start(context.Background(), "s1", Config{WorkDir: "/tmp"})
	f.WaitForIdleErrors["s1"] = nil
	gate := make(chan struct{})
	f.WaitForIdleGates["s1"] = gate

	// Start a gated WaitForIdle in the background.
	go func() {
		_ = f.WaitForIdle(context.Background(), "s1", time.Second)
	}()

	// Give the goroutine time to acquire and release the lock.
	time.Sleep(20 * time.Millisecond)

	// Other Fake operations must not deadlock while the gate is held.
	if !f.IsRunning("s1") {
		t.Fatal("IsRunning returned false while gate is held")
	}

	close(gate)
}
