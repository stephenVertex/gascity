package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/session"
)

func (s *Server) lockSessionPermissionMode(id string) func() {
	if s == nil {
		var mu sync.Mutex
		mu.Lock()
		return mu.Unlock
	}
	s.permissionModeLockMu.Lock()
	if s.permissionModeLocks == nil {
		s.permissionModeLocks = make(map[string]*sync.Mutex)
	}
	mu := s.permissionModeLocks[id]
	if mu == nil {
		mu = &sync.Mutex{}
		s.permissionModeLocks[id] = mu
	}
	s.permissionModeLockMu.Unlock()

	mu.Lock()
	return mu.Unlock
}

func (s *Server) warnPermissionMode(key, format string, args ...any) {
	if s == nil {
		log.Printf("gc api: "+format, args...)
		return
	}
	now := time.Now()
	s.permissionModeWarningMu.Lock()
	if s.permissionModeWarnings == nil {
		s.permissionModeWarnings = make(map[string]time.Time)
	}
	if last, ok := s.permissionModeWarnings[key]; ok && now.Sub(last) < permissionModeWarningInterval {
		s.permissionModeWarningMu.Unlock()
		return
	}
	s.permissionModeWarnings[key] = now
	s.permissionModeWarningMu.Unlock()
	log.Printf("gc api: "+format, args...)
}

func (s *Server) humaHandleSessionPermissionMode(ctx context.Context, input *SessionPermissionModeInput) (*SessionPermissionModeOutput, error) {
	target, err := s.resolvePermissionModeMutation(input)
	if err != nil {
		return nil, err
	}
	unlock := s.lockSessionPermissionMode(target.id)
	defer unlock()

	plan, err := s.planPermissionModeSwitch(target)
	if err != nil {
		return nil, err
	}
	state, err := s.applyPermissionModeSwitch(ctx, target, plan)
	if err != nil {
		return nil, err
	}
	version, err := s.persistPermissionMode(target.store, target.id, target.mode, state.Version)
	if err != nil {
		return nil, err
	}
	s.emitPermissionModeUpdate(target.info, target.id, target.mode, version)

	out := &SessionPermissionModeOutput{}
	out.Body.ID = target.id
	out.Body.PermissionMode = target.mode
	out.Body.ModeVersion = version
	out.Body.Verified = state.Verified
	return out, nil
}

type permissionModeMutationTarget struct {
	store    beads.Store
	id       string
	info     session.Info
	mode     runtime.PermissionMode
	provider string
	switcher runtime.PermissionModeSwitcher
}

type permissionModeSwitchPlan struct {
	capability runtime.PermissionModeCapability
	stateful   runtime.PermissionModeStatefulSwitcher
	knownMode  runtime.PermissionMode
	useKnown   bool
}

func (s *Server) resolvePermissionModeMutation(input *SessionPermissionModeInput) (permissionModeMutationTarget, error) {
	var target permissionModeMutationTarget
	store := s.state.CityBeadStore()
	if store == nil {
		return target, huma.Error503ServiceUnavailable("no bead store configured")
	}
	id, err := s.resolveSessionIDAllowClosedWithConfig(store, input.ID)
	if err != nil {
		return target, humaResolveError(err)
	}
	mgr := s.sessionManager(store)
	info, err := mgr.Get(id)
	if err != nil {
		return target, humaSessionManagerError(err)
	}
	if info.Closed || info.State != session.StateActive {
		return target, huma.Error409Conflict("not_running: session is not running")
	}
	mode, ok := runtime.NormalizePermissionMode(string(input.Body.PermissionMode))
	if !ok {
		return target, huma.Error422UnprocessableEntity("invalid: " + runtime.ErrPermissionModeInvalid.Error())
	}
	sp := s.state.SessionProvider()
	if sp == nil || !sp.IsRunning(info.SessionName) {
		return target, huma.Error409Conflict("not_running: session is not running")
	}
	switcher, ok := sp.(runtime.PermissionModeSwitcher)
	if !ok {
		return target, huma.Error501NotImplemented("unsupported: " + runtime.ErrPermissionModeUnsupported.Error())
	}
	target.store = store
	target.id = id
	target.info = info
	target.mode = mode
	target.provider = s.permissionModeRuntimeProvider(info)
	target.switcher = switcher
	return target, nil
}

func (s *Server) planPermissionModeSwitch(target permissionModeMutationTarget) (permissionModeSwitchPlan, error) {
	var plan permissionModeSwitchPlan
	modeStore := newSessionPermissionModeStore(target.store, s.state.Config(), s.warnPermissionMode)
	stored, err := modeStore.LoadStored(target.id)
	if err != nil {
		return plan, humaStoreError(err)
	}
	knownMode, hasKnownMode := snapshotRuntimePermissionMode(stored)
	if !hasKnownMode {
		configured, err := modeStore.LoadConfigured(target.id, target.info)
		if err != nil {
			return plan, humaStoreError(err)
		}
		knownMode, hasKnownMode = snapshotRuntimePermissionMode(configured)
	}
	capability := target.switcher.PermissionModeCapability(target.info.SessionName, target.provider)
	if (!capability.Supported || !capability.Readable) && hasKnownMode {
		if statefulSwitch, ok := target.switcher.(runtime.PermissionModeStatefulSwitcher); ok {
			fallback := statefulSwitch.PermissionModeCapabilityForState(target.info.SessionName, target.provider, knownMode)
			if fallback.Supported {
				capability = fallback
				plan.stateful = statefulSwitch
				plan.useKnown = true
				plan.knownMode = knownMode
			}
		}
	}
	if !capability.Supported {
		if hasKnownMode {
			return plan, huma.Error501NotImplemented("unsupported: " + runtime.ErrPermissionModeSwitchUnsupported.Error())
		}
		return plan, huma.Error501NotImplemented("unsupported: " + firstNonEmptyString(capability.Reason, runtime.ErrPermissionModeUnsupported.Error()))
	}
	if !capability.LiveSwitch {
		return plan, huma.Error501NotImplemented("unsupported: " + firstNonEmptyString(capability.Reason, runtime.ErrPermissionModeSwitchUnsupported.Error()))
	}
	if !permissionModeCapabilityAllows(capability, target.mode) {
		return plan, huma.Error501NotImplemented(fmt.Sprintf("unsupported: permission mode %q is not supported by this session", target.mode))
	}
	plan.capability = capability
	return plan, nil
}

func (s *Server) applyPermissionModeSwitch(ctx context.Context, target permissionModeMutationTarget, plan permissionModeSwitchPlan) (runtime.PermissionModeState, error) {
	var state runtime.PermissionModeState
	var err error
	if plan.useKnown {
		state, err = plan.stateful.SetPermissionModeFromState(ctx, target.info.SessionName, target.provider, plan.knownMode, target.mode)
	} else {
		state, err = target.switcher.SetPermissionMode(ctx, target.info.SessionName, target.provider, target.mode)
	}
	if err != nil {
		if permissionModeVerificationUnavailable(err) {
			state = runtime.PermissionModeState{Mode: target.mode, Verified: false}
		} else {
			return runtime.PermissionModeState{}, humaPermissionModeError(err)
		}
	}
	if state.Mode != target.mode {
		return runtime.PermissionModeState{}, huma.Error502BadGateway(fmt.Sprintf("verification_failed: confirmed %q, want %q", state.Mode, target.mode))
	}
	return state, nil
}

func (s *Server) persistPermissionMode(store beads.Store, id string, mode runtime.PermissionMode, providerVersion uint64) (uint64, error) {
	version, err := newSessionPermissionModeStore(store, s.state.Config(), s.warnPermissionMode).SaveNext(id, mode, providerVersion)
	if err != nil {
		return 0, humaStoreError(err)
	}
	return version, nil
}

func (s *Server) emitPermissionModeUpdate(info session.Info, id string, mode runtime.PermissionMode, version uint64) {
	s.emitAsyncResult(events.SessionUpdated, id, SessionUpdatedPayload{
		SessionID:      id,
		SessionName:    info.SessionName,
		Provider:       info.Provider,
		PermissionMode: mode,
		ModeVersion:    version,
	})
}

func humaPermissionModeError(err error) error {
	switch {
	case errors.Is(err, runtime.ErrPermissionModeInvalid):
		return huma.Error422UnprocessableEntity("invalid: " + err.Error())
	case errors.Is(err, runtime.ErrPermissionModeVerificationFailed):
		return huma.Error502BadGateway("verification_failed: " + err.Error())
	case errors.Is(err, runtime.ErrPermissionModeUnsupported):
		return huma.Error501NotImplemented("unsupported: " + err.Error())
	case errors.Is(err, runtime.ErrPermissionModeSwitchUnsupported):
		return huma.Error501NotImplemented("unsupported: " + err.Error())
	case errors.Is(err, runtime.ErrSessionNotFound):
		return huma.Error409Conflict("not_running: " + err.Error())
	default:
		return huma.Error500InternalServerError("internal: " + err.Error())
	}
}

func permissionModeVerificationUnavailable(err error) bool {
	return errors.Is(err, runtime.ErrPermissionModeVerificationFailed) && errors.Is(err, runtime.ErrPermissionModeUnsupported)
}

func permissionModeCapabilityAllows(capability runtime.PermissionModeCapability, mode runtime.PermissionMode) bool {
	for _, value := range capability.Values {
		if value == mode {
			return true
		}
	}
	return false
}
