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
