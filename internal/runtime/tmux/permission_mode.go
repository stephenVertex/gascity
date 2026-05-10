package tmux

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/runtime"
)

var _ runtime.PermissionModeSwitcher = (*Provider)(nil)

// PermissionModeCapability reports whether the tmux-backed provider can read
// and switch the running session's permission mode.
func (p *Provider) PermissionModeCapability(name, provider string) runtime.PermissionModeCapability {
	if !providerSupportsClaudePermissionMode(provider) {
		return runtime.PermissionModeCapability{Reason: "provider does not support runtime permission mode"}
	}
	state, err := p.readPermissionMode(name)
	if err != nil {
		return tmuxPermissionModeCapabilityForReadError(err)
	}
	return runtime.PermissionModeCapability{
		Supported:  true,
		Readable:   true,
		LiveSwitch: true,
		Values:     runtime.PermissionModeCycleValues(state.Mode),
	}
}

// PermissionModeCapabilityForState reports live-switch capability from a known
// current mode when the running pane no longer prints it.
func (p *Provider) PermissionModeCapabilityForState(name, provider string, current runtime.PermissionMode) runtime.PermissionModeCapability {
	if !providerSupportsClaudePermissionMode(provider) {
		return runtime.PermissionModeCapability{Reason: "provider does not support runtime permission mode"}
	}
	if !p.IsRunning(name) {
		return runtime.PermissionModeCapability{Reason: "session is not running"}
	}
	values := runtime.PermissionModeCycleValues(current)
	if len(values) == 0 {
		return runtime.PermissionModeCapability{Reason: "session permission mode is unavailable for this session"}
	}
	return runtime.PermissionModeCapability{
		Supported:  true,
		Readable:   true,
		LiveSwitch: true,
		Values:     values,
	}
}

// PermissionMode reads the current permission mode from the running session.
func (p *Provider) PermissionMode(_ context.Context, name, provider string) (runtime.PermissionModeState, error) {
	if !providerSupportsClaudePermissionMode(provider) {
		return runtime.PermissionModeState{}, runtime.ErrPermissionModeUnsupported
	}
	return p.readPermissionMode(name)
}

func (p *Provider) readPermissionMode(name string) (runtime.PermissionModeState, error) {
	if !p.IsRunning(name) {
		return runtime.PermissionModeState{}, runtime.ErrSessionNotFound
	}
	pane, err := p.tm.CapturePane(name, 80)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrNoServer) {
			return runtime.PermissionModeState{}, runtime.ErrSessionNotFound
		}
		return runtime.PermissionModeState{}, err
	}
	mode, ok := parseClaudePermissionMode(pane)
	if !ok {
		return runtime.PermissionModeState{}, runtime.ErrPermissionModeUnsupported
	}
	return runtime.PermissionModeState{Mode: mode, Verified: true}, nil
}

func tmuxPermissionModeCapabilityForReadError(err error) runtime.PermissionModeCapability {
	switch {
	case errors.Is(err, runtime.ErrSessionNotFound):
		return runtime.PermissionModeCapability{Reason: "session is not running"}
	case errors.Is(err, runtime.ErrPermissionModeUnsupported):
		return runtime.PermissionModeCapability{Reason: "session permission mode is unavailable for this session"}
	default:
		return runtime.PermissionModeCapability{Reason: err.Error()}
	}
}

// SetPermissionMode switches a running session to the requested permission
// mode and verifies the resulting state when the provider surfaces it.
func (p *Provider) SetPermissionMode(ctx context.Context, name, provider string, mode runtime.PermissionMode) (runtime.PermissionModeState, error) {
	if !providerSupportsClaudePermissionMode(provider) {
		return runtime.PermissionModeState{}, runtime.ErrPermissionModeUnsupported
	}
	if !p.IsRunning(name) {
		return runtime.PermissionModeState{}, runtime.ErrSessionNotFound
	}
	current, err := p.PermissionMode(ctx, name, provider)
	if err != nil {
		return runtime.PermissionModeState{}, err
	}
	return p.setPermissionModeFromCurrent(ctx, name, provider, current.Mode, mode, true)
}

// SetPermissionModeFromState switches a running session using caller-supplied
// current state when live reads are temporarily unavailable.
func (p *Provider) SetPermissionModeFromState(ctx context.Context, name, provider string, current, mode runtime.PermissionMode) (runtime.PermissionModeState, error) {
	if !providerSupportsClaudePermissionMode(provider) {
		return runtime.PermissionModeState{}, runtime.ErrPermissionModeUnsupported
	}
	if !p.IsRunning(name) {
		return runtime.PermissionModeState{}, runtime.ErrSessionNotFound
	}
	return p.setPermissionModeFromCurrent(ctx, name, provider, current, mode, false)
}

func (p *Provider) setPermissionModeFromCurrent(ctx context.Context, name, provider string, current, mode runtime.PermissionMode, verifiedCurrent bool) (runtime.PermissionModeState, error) {
	if current == mode {
		return runtime.PermissionModeState{Mode: mode, Verified: verifiedCurrent}, nil
	}
	if !runtime.PermissionModeCanSwitch(current, mode) {
		return runtime.PermissionModeState{}, fmt.Errorf("%w: cannot switch from %q to %q", runtime.ErrPermissionModeSwitchUnsupported, current, mode)
	}
	steps, ok := runtime.PermissionModeCycleSteps(current, mode)
	if !ok {
		return runtime.PermissionModeState{}, fmt.Errorf("%w: cannot switch from %q to %q", runtime.ErrPermissionModeSwitchUnsupported, current, mode)
	}
	for i := 0; i < steps; i++ {
		if err := p.sendClaudePermissionModeCycleKey(name); err != nil {
			return runtime.PermissionModeState{}, err
		}
		if err := sleepWithContext(ctx, 200*time.Millisecond); err != nil {
			return runtime.PermissionModeState{}, err
		}
	}
	if err := sleepWithContext(ctx, 300*time.Millisecond); err != nil {
		return runtime.PermissionModeState{}, err
	}
	confirmed, err := p.PermissionMode(ctx, name, provider)
	if err != nil {
		if errors.Is(err, runtime.ErrPermissionModeUnsupported) {
			return runtime.PermissionModeState{Mode: mode, Verified: false}, nil
		}
		return runtime.PermissionModeState{}, fmt.Errorf("%w: %w", runtime.ErrPermissionModeVerificationFailed, err)
	}
	if confirmed.Mode != mode {
		return runtime.PermissionModeState{}, fmt.Errorf("%w: confirmed %q, want %q", runtime.ErrPermissionModeVerificationFailed, confirmed.Mode, mode)
	}
	confirmed.Verified = true
	return confirmed, nil
}

func providerSupportsClaudePermissionMode(provider string) bool {
	return strings.Contains(strings.TrimSpace(strings.ToLower(provider)), "claude")
}

func parseClaudePermissionMode(pane string) (runtime.PermissionMode, bool) {
	lines := strings.Split(pane, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.ToLower(strings.TrimSpace(lines[i]))
		if line == "" {
			continue
		}
		if !strings.Contains(line, "permission") && !strings.Contains(line, "shift+tab") && !strings.Contains(line, "mode") {
			continue
		}
		switch {
		case strings.Contains(line, "bypass permissions"):
			return runtime.PermissionModeBypassPermissions, true
		case strings.Contains(line, "accept edits"):
			return runtime.PermissionModeAcceptEdits, true
		case strings.Contains(line, "plan mode") || strings.Contains(line, "plan"):
			return runtime.PermissionModePlan, true
		case strings.Contains(line, "default mode") || strings.Contains(line, "normal mode"):
			return runtime.PermissionModeDefault, true
		}
	}
	return "", false
}

func (p *Provider) sendClaudePermissionModeCycleKey(name string) error {
	if _, err := p.tm.run("send-keys", "-t", name, "BTab"); err != nil {
		if errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrNoServer) {
			return runtime.ErrSessionNotFound
		}
		return err
	}
	return nil
}
