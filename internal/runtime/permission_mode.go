package runtime

import (
	"context"
	"errors"
	"strings"
)

// PermissionMode is the canonical runtime permission mode exposed by providers
// that can report or switch a running session's edit/approval behavior.
type PermissionMode string

const (
	// PermissionModeDefault is the provider's normal permission mode.
	PermissionModeDefault PermissionMode = "default"
	// PermissionModeAcceptEdits allows edits while preserving other prompts.
	PermissionModeAcceptEdits PermissionMode = "acceptEdits"
	// PermissionModePlan keeps the session in planning/read-only mode.
	PermissionModePlan PermissionMode = "plan"
	// PermissionModeBypassPermissions bypasses permission prompts.
	PermissionModeBypassPermissions PermissionMode = "bypassPermissions"
)

var (
	// ErrPermissionModeUnsupported reports that the provider or session cannot
	// surface permission mode state.
	ErrPermissionModeUnsupported = errors.New("session permission mode is unsupported")
	// ErrPermissionModeSwitchUnsupported reports that the provider can read mode
	// state but cannot switch the running session live.
	ErrPermissionModeSwitchUnsupported = errors.New("session permission mode live switch is unsupported")
	// ErrPermissionModeVerificationFailed reports that a switch command ran but
	// the provider did not confirm the requested mode afterward.
	ErrPermissionModeVerificationFailed = errors.New("session permission mode verification failed")
	// ErrPermissionModeInvalid reports that a requested mode is not recognized.
	ErrPermissionModeInvalid = errors.New("invalid session permission mode")
)

// PermissionModeState reports the current runtime mode for a session.
type PermissionModeState struct {
	Mode     PermissionMode
	Version  uint64
	Verified bool
}

// PermissionModeCapability describes whether a session provider can read and
// switch runtime permission mode for a session.
type PermissionModeCapability struct {
	Supported  bool
	Readable   bool
	LiveSwitch bool
	Values     []PermissionMode
	Reason     string
}

// PermissionModeReader is implemented by providers that can report runtime
// permission mode state for a session.
type PermissionModeReader interface {
	PermissionModeCapability(sessionName, provider string) PermissionModeCapability
	PermissionMode(ctx context.Context, sessionName, provider string) (PermissionModeState, error)
}

// PermissionModeSwitcher is implemented by providers that can change runtime
// permission mode for a running session.
type PermissionModeSwitcher interface {
	PermissionModeReader
	SetPermissionMode(ctx context.Context, sessionName, provider string, mode PermissionMode) (PermissionModeState, error)
}

// PermissionModeStatefulSwitcher is implemented by providers that can switch
// from a caller-supplied current mode when the current pane no longer reports it.
type PermissionModeStatefulSwitcher interface {
	PermissionModeCapabilityForState(sessionName, provider string, current PermissionMode) PermissionModeCapability
	SetPermissionModeFromState(ctx context.Context, sessionName, provider string, current, mode PermissionMode) (PermissionModeState, error)
}

// NormalizePermissionMode maps provider aliases onto the canonical permission
// mode vocabulary used by the supervisor API.
func NormalizePermissionMode(value string) (PermissionMode, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "default", "normal", "suggest":
		return PermissionModeDefault, true
	case "acceptedits", "accept-edits", "accept_edits", "auto-edit", "auto_edit":
		return PermissionModeAcceptEdits, true
	case "plan", "plan-mode", "plan_mode":
		return PermissionModePlan, true
	case "bypasspermissions", "bypass-permissions", "bypass_permissions", "unrestricted", "full-auto", "full_auto", "yolo":
		return PermissionModeBypassPermissions, true
	default:
		return "", false
	}
}

// CanonicalPermissionModes returns the stable set of API permission mode
// values in display order.
func CanonicalPermissionModes() []PermissionMode {
	return []PermissionMode{
		PermissionModeDefault,
		PermissionModeAcceptEdits,
		PermissionModePlan,
		PermissionModeBypassPermissions,
	}
}

// PermissionModeCycleValues returns the modes reachable from the current mode
// through the standard live mode cycle.
func PermissionModeCycleValues(current PermissionMode) []PermissionMode {
	switch current {
	case PermissionModeDefault, PermissionModeAcceptEdits, PermissionModePlan, PermissionModeBypassPermissions:
		return []PermissionMode{
			PermissionModeDefault,
			PermissionModeAcceptEdits,
			PermissionModePlan,
			PermissionModeBypassPermissions,
		}
	default:
		return nil
	}
}

// PermissionModeCycleSteps reports how many live-cycle steps move from current
// to target.
func PermissionModeCycleSteps(current, target PermissionMode) (int, bool) {
	cycle := PermissionModeCycleValues(current)
	if len(cycle) == 0 {
		return 0, false
	}
	currentIndex := -1
	targetIndex := -1
	for i, mode := range cycle {
		if mode == current {
			currentIndex = i
		}
		if mode == target {
			targetIndex = i
		}
	}
	if currentIndex < 0 || targetIndex < 0 {
		return 0, false
	}
	if targetIndex >= currentIndex {
		return targetIndex - currentIndex, true
	}
	return len(cycle) - currentIndex + targetIndex, true
}

// PermissionModeCanSwitch reports whether target is reachable from current
// through the standard live mode cycle.
func PermissionModeCanSwitch(current, target PermissionMode) bool {
	_, ok := PermissionModeCycleSteps(current, target)
	return ok
}
