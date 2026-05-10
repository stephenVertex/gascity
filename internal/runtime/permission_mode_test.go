package runtime

import "testing"

func TestNormalizePermissionModeCanonicalAndAliases(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  PermissionMode
		ok    bool
	}{
		{name: "default", input: "default", want: PermissionModeDefault, ok: true},
		{name: "normal alias", input: "normal", want: PermissionModeDefault, ok: true},
		{name: "suggest alias", input: " suggest ", want: PermissionModeDefault, ok: true},
		{name: "accept camel", input: "acceptEdits", want: PermissionModeAcceptEdits, ok: true},
		{name: "accept hyphen alias", input: "auto-edit", want: PermissionModeAcceptEdits, ok: true},
		{name: "accept underscore alias", input: "auto_edit", want: PermissionModeAcceptEdits, ok: true},
		{name: "plan", input: "plan", want: PermissionModePlan, ok: true},
		{name: "plan hyphen alias", input: "plan-mode", want: PermissionModePlan, ok: true},
		{name: "bypass camel", input: "bypassPermissions", want: PermissionModeBypassPermissions, ok: true},
		{name: "bypass unrestricted alias", input: "unrestricted", want: PermissionModeBypassPermissions, ok: true},
		{name: "bypass full auto alias", input: "FULL_AUTO", want: PermissionModeBypassPermissions, ok: true},
		{name: "invalid", input: "review-only", ok: false},
		{name: "empty", input: " ", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := NormalizePermissionMode(tt.input)
			if ok != tt.ok {
				t.Fatalf("NormalizePermissionMode(%q) ok = %v, want %v", tt.input, ok, tt.ok)
			}
			if got != tt.want {
				t.Fatalf("NormalizePermissionMode(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCanonicalPermissionModesReturnsOrderedCopy(t *testing.T) {
	got := CanonicalPermissionModes()
	want := []PermissionMode{
		PermissionModeDefault,
		PermissionModeAcceptEdits,
		PermissionModePlan,
		PermissionModeBypassPermissions,
	}
	if len(got) != len(want) {
		t.Fatalf("len(CanonicalPermissionModes()) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("CanonicalPermissionModes()[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	got[0] = PermissionMode("mutated")
	if fresh := CanonicalPermissionModes()[0]; fresh != PermissionModeDefault {
		t.Fatalf("CanonicalPermissionModes returned shared backing storage; got first mode %q", fresh)
	}
}

func TestPermissionModeCycleSteps(t *testing.T) {
	tests := []struct {
		name    string
		current PermissionMode
		target  PermissionMode
		steps   int
		ok      bool
	}{
		{name: "same mode", current: PermissionModeDefault, target: PermissionModeDefault, steps: 0, ok: true},
		{name: "next mode", current: PermissionModeDefault, target: PermissionModeAcceptEdits, steps: 1, ok: true},
		{name: "later mode", current: PermissionModeDefault, target: PermissionModeBypassPermissions, steps: 3, ok: true},
		{name: "wrap around", current: PermissionModeBypassPermissions, target: PermissionModeDefault, steps: 1, ok: true},
		{name: "wrap multiple", current: PermissionModePlan, target: PermissionModeAcceptEdits, steps: 3, ok: true},
		{name: "unknown current", current: PermissionMode("unknown"), target: PermissionModeDefault, ok: false},
		{name: "unknown target", current: PermissionModeDefault, target: PermissionMode("unknown"), ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			steps, ok := PermissionModeCycleSteps(tt.current, tt.target)
			if ok != tt.ok {
				t.Fatalf("PermissionModeCycleSteps(%q, %q) ok = %v, want %v", tt.current, tt.target, ok, tt.ok)
			}
			if steps != tt.steps {
				t.Fatalf("PermissionModeCycleSteps(%q, %q) steps = %d, want %d", tt.current, tt.target, steps, tt.steps)
			}
			if canSwitch := PermissionModeCanSwitch(tt.current, tt.target); canSwitch != tt.ok {
				t.Fatalf("PermissionModeCanSwitch(%q, %q) = %v, want %v", tt.current, tt.target, canSwitch, tt.ok)
			}
		})
	}
}

func TestPermissionModeCycleValuesUnknownCurrent(t *testing.T) {
	if got := PermissionModeCycleValues(PermissionMode("unknown")); got != nil {
		t.Fatalf("PermissionModeCycleValues(unknown) = %v, want nil", got)
	}
}
