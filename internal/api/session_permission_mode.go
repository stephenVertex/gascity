package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/session"
)

const (
	permissionModeOptionKey          = "permission_mode"
	permissionModeMetadataKey        = "permission_mode"
	permissionModeVersionMetadataKey = "permission_mode_version"
	permissionModeWarningInterval    = time.Minute
)

type sessionCapabilities struct {
	PermissionMode sessionPermissionModeCapability `json:"permission_mode"`
}

type sessionPermissionModeCapability struct {
	Supported  bool     `json:"supported" doc:"Whether runtime permission mode is supported for this provider/session."`
	Readable   bool     `json:"readable" doc:"Whether the current runtime permission mode can be read."`
	LiveSwitch bool     `json:"live_switch" doc:"Whether the running session can switch permission mode without restart."`
	Values     []string `json:"values,omitempty" doc:"Canonical permission mode values accepted by this session."`
	Reason     string   `json:"reason,omitempty" doc:"Reason permission mode is unavailable or limited."`
}

// SessionUpdatedPayload is emitted when mutable session state changes.
type SessionUpdatedPayload struct {
	SessionID      string            `json:"session_id,omitempty"`
	SessionName    string            `json:"session_name,omitempty"`
	Provider       string            `json:"provider,omitempty"`
	PermissionMode string            `json:"permission_mode,omitempty"`
	ModeVersion    uint64            `json:"mode_version,omitempty"`
	Options        map[string]string `json:"options,omitempty"`
}

// IsEventPayload marks SessionUpdatedPayload as an events.Payload variant.
func (SessionUpdatedPayload) IsEventPayload() {}

func applyConfiguredPermissionMode(resp *sessionResponse, b *beads.Bead) {
	if resp == nil {
		return
	}
	resp.Capabilities.PermissionMode = sessionPermissionModeCapability{
		Supported: false,
		Reason:    "provider does not support runtime permission mode",
	}
	if resp.Options != nil {
		if mode, ok := runtime.NormalizePermissionMode(resp.Options[permissionModeOptionKey]); ok {
			setResponsePermissionMode(resp, mode, 0)
			resp.Capabilities.PermissionMode = sessionPermissionModeCapability{
				Supported: true,
				Values:    stringPermissionModes(runtime.CanonicalPermissionModes()),
				Reason:    "permission mode is configured at launch only",
			}
		}
	}
	if b == nil {
		return
	}
	if mode, ok := runtime.NormalizePermissionMode(b.Metadata[permissionModeMetadataKey]); ok {
		version, _ := parseModeVersion(b.Metadata[permissionModeVersionMetadataKey])
		setResponsePermissionMode(resp, mode, version)
	}
}

func (s *Server) enrichLivePermissionModeFromBead(resp *sessionResponse, info session.Info, b *beads.Bead) {
	if resp == nil || s == nil || s.state == nil || strings.TrimSpace(info.SessionName) == "" {
		return
	}
	reader, ok := s.state.SessionProvider().(runtime.PermissionModeReader)
	if !ok {
		return
	}
	knownMode, knownVersion, knownModeState := responsePermissionMode(resp)
	configuredCapability := resp.Capabilities.PermissionMode
	provider := s.permissionModeRuntimeProviderFromBead(info, b)
	capability := reader.PermissionModeCapability(info.SessionName, provider)
	usedStatefulCapability := false
	if (!capability.Supported || !capability.Readable) && info.State == session.StateActive && knownModeState {
		if stateful, ok := reader.(runtime.PermissionModeStatefulSwitcher); ok {
			fallback := stateful.PermissionModeCapabilityForState(info.SessionName, provider, knownMode)
			if fallback.Supported {
				capability = fallback
				usedStatefulCapability = true
			}
		}
	}
	if !capability.Supported && knownModeState && configuredCapability.Supported {
		resp.Capabilities.PermissionMode = configuredCapability
		return
	}
	resp.Capabilities.PermissionMode = apiPermissionModeCapability(capability)
	if !capability.Supported || !capability.Readable || info.State != session.StateActive {
		return
	}
	state, err := reader.PermissionMode(context.Background(), info.SessionName, provider)
	if err != nil {
		if usedStatefulCapability && knownModeState {
			setResponsePermissionMode(resp, knownMode, knownVersion)
			return
		}
		if knownModeState && knownVersion > 0 {
			setResponsePermissionMode(resp, knownMode, knownVersion)
			return
		}
		if knownModeState && configuredCapability.Supported {
			setResponsePermissionMode(resp, knownMode, knownVersion)
			resp.Capabilities.PermissionMode = configuredCapability
			return
		}
		resp.Capabilities.PermissionMode = apiPermissionModeCapability(permissionModeCapabilityForError(err))
		return
	}
	if state.Mode != "" {
		setResponsePermissionMode(resp, state.Mode, state.Version)
	}
}

func setResponsePermissionMode(resp *sessionResponse, mode runtime.PermissionMode, version uint64) {
	if resp.Options == nil {
		resp.Options = make(map[string]string, 1)
	}
	resp.Options[permissionModeOptionKey] = string(mode)
	if version > 0 {
		resp.ModeVersion = version
	}
}

func responsePermissionMode(resp *sessionResponse) (runtime.PermissionMode, uint64, bool) {
	if resp == nil || resp.Options == nil {
		return "", 0, false
	}
	mode, ok := runtime.NormalizePermissionMode(resp.Options[permissionModeOptionKey])
	if !ok {
		return "", 0, false
	}
	return mode, resp.ModeVersion, true
}

func apiPermissionModeCapability(capability runtime.PermissionModeCapability) sessionPermissionModeCapability {
	return sessionPermissionModeCapability{
		Supported:  capability.Supported,
		Readable:   capability.Readable,
		LiveSwitch: capability.LiveSwitch,
		Values:     stringPermissionModes(capability.Values),
		Reason:     capability.Reason,
	}
}

func permissionModeCapabilityForError(err error) runtime.PermissionModeCapability {
	switch {
	case errors.Is(err, runtime.ErrPermissionModeSwitchUnsupported):
		return runtime.PermissionModeCapability{
			Supported: true,
			Readable:  true,
			Reason:    err.Error(),
		}
	case errors.Is(err, runtime.ErrPermissionModeUnsupported):
		return runtime.PermissionModeCapability{Reason: err.Error()}
	default:
		return runtime.PermissionModeCapability{Reason: err.Error()}
	}
}

func stringPermissionModes(values []runtime.PermissionMode) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		out = append(out, string(value))
	}
	return out
}

func parseModeVersion(value string) (uint64, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, nil
	}
	n, err := strconv.ParseUint(trimmed, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid permission mode version %q: %w", trimmed, err)
	}
	return n, nil
}

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
	knownMode, _, hasKnownMode, err := storedPermissionMode(target.store, target.id)
	if err != nil {
		return plan, humaStoreError(err)
	}
	if !hasKnownMode {
		knownMode, hasKnownMode, err = configuredPermissionMode(target.store, target.id, target.info, s.state.Config())
		if err != nil {
			return plan, humaStoreError(err)
		}
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
	version := providerVersion
	nextVersion, err := s.nextStoredModeVersion(store, id)
	if err != nil {
		return 0, humaStoreError(err)
	}
	if version < nextVersion {
		version = nextVersion
	}
	if err := store.SetMetadataBatch(id, map[string]string{
		permissionModeMetadataKey:        string(mode),
		permissionModeVersionMetadataKey: strconv.FormatUint(version, 10),
	}); err != nil {
		return 0, humaStoreError(err)
	}
	return version, nil
}

func (s *Server) emitPermissionModeUpdate(info session.Info, id string, mode runtime.PermissionMode, version uint64) {
	s.emitAsyncResult(events.SessionUpdated, id, SessionUpdatedPayload{
		SessionID:      id,
		SessionName:    info.SessionName,
		Provider:       info.Provider,
		PermissionMode: string(mode),
		ModeVersion:    version,
		Options:        map[string]string{permissionModeOptionKey: string(mode)},
	})
}

func (s *Server) nextStoredModeVersion(store beads.Store, id string) (uint64, error) {
	b, err := store.Get(id)
	if err != nil {
		return 0, err
	}
	version, err := parseModeVersion(b.Metadata[permissionModeVersionMetadataKey])
	if err != nil {
		return 0, err
	}
	return version + 1, nil
}

func storedPermissionMode(store beads.Store, id string) (runtime.PermissionMode, uint64, bool, error) {
	if store == nil {
		return "", 0, false, nil
	}
	b, err := store.Get(id)
	if err != nil {
		return "", 0, false, err
	}
	mode, ok := runtime.NormalizePermissionMode(b.Metadata[permissionModeMetadataKey])
	if !ok {
		return "", 0, false, nil
	}
	version, err := parseModeVersion(b.Metadata[permissionModeVersionMetadataKey])
	if err != nil {
		return "", 0, false, err
	}
	return mode, version, true, nil
}

func configuredPermissionMode(store beads.Store, id string, info session.Info, cfg *config.City) (runtime.PermissionMode, bool, error) {
	if store == nil {
		return "", false, nil
	}
	b, err := store.Get(id)
	if err != nil {
		return "", false, err
	}
	resp := sessionResponseWithReason(info, &b, cfg, false)
	mode, _, ok := responsePermissionMode(&resp)
	return mode, ok, nil
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

type sessionPermissionModeSnapshot struct {
	Mode    string
	Version uint64
	Known   bool
}

func (s *Server) sessionPermissionModeSnapshot(info session.Info) sessionPermissionModeSnapshot {
	var snapshot sessionPermissionModeSnapshot
	if s == nil || s.state == nil {
		return snapshot
	}
	if store := s.state.CityBeadStore(); store != nil {
		if b, err := store.Get(info.ID); err == nil {
			if mode, ok := runtime.NormalizePermissionMode(b.Metadata[permissionModeMetadataKey]); ok {
				version, versionErr := parseModeVersion(b.Metadata[permissionModeVersionMetadataKey])
				if versionErr != nil {
					s.warnPermissionMode("snapshot-version:"+info.ID, "session %s permission mode version ignored: %v", info.ID, versionErr)
				}
				snapshot.Mode = string(mode)
				snapshot.Version = version
				snapshot.Known = true
			}
			if !snapshot.Known {
				resp := sessionToResponse(info, s.state.Config())
				applyConfiguredPermissionMode(&resp, &b)
				if mode := resp.Options[permissionModeOptionKey]; mode != "" {
					snapshot.Mode = mode
					snapshot.Version = resp.ModeVersion
					snapshot.Known = true
				}
			}
		} else {
			s.warnPermissionMode("snapshot-store:"+info.ID, "session %s permission mode bead lookup failed: %v", info.ID, err)
		}
	}
	reader, ok := s.state.SessionProvider().(runtime.PermissionModeReader)
	if !ok || strings.TrimSpace(info.SessionName) == "" || info.State != session.StateActive {
		return snapshot
	}
	provider := s.permissionModeRuntimeProvider(info)
	capability := reader.PermissionModeCapability(info.SessionName, provider)
	if !capability.Supported || !capability.Readable {
		return snapshot
	}
	state, err := reader.PermissionMode(context.Background(), info.SessionName, provider)
	if err != nil || state.Mode == "" {
		if err != nil {
			s.warnPermissionMode("snapshot-runtime:"+info.ID, "session %s permission mode live read failed: %v", info.ID, err)
		}
		return snapshot
	}
	snapshot.Mode = string(state.Mode)
	if state.Version > 0 {
		snapshot.Version = state.Version
	}
	snapshot.Known = true
	return snapshot
}

func (s *Server) permissionModeRuntimeProvider(info session.Info) string {
	return s.permissionModeRuntimeProviderFromBead(info, nil)
}

func (s *Server) permissionModeRuntimeProviderFromBead(info session.Info, b *beads.Bead) string {
	provider := strings.TrimSpace(info.Provider)
	if s == nil || s.state == nil {
		return provider
	}
	if b != nil {
		if ancestor := strings.TrimSpace(b.Metadata["builtin_ancestor"]); ancestor != "" {
			return ancestor
		}
		if kind := strings.TrimSpace(b.Metadata["provider_kind"]); kind != "" {
			return kind
		}
	} else if store := s.state.CityBeadStore(); store != nil {
		if b, err := store.Get(info.ID); err == nil {
			if ancestor := strings.TrimSpace(b.Metadata["builtin_ancestor"]); ancestor != "" {
				return ancestor
			}
			if kind := strings.TrimSpace(b.Metadata["provider_kind"]); kind != "" {
				return kind
			}
		}
	}
	cfg := s.state.Config()
	if cfg == nil {
		return provider
	}
	if resolved, err := resolveProviderForTemplate(info.Template, cfg); err == nil {
		if family := permissionModeResolvedProviderFamily(resolved); family != "" {
			return family
		}
	}
	if family := config.BuiltinFamily(provider, cfg.Providers); family != "" {
		return family
	}
	return provider
}

func permissionModeResolvedProviderFamily(resolved *config.ResolvedProvider) string {
	if resolved == nil {
		return ""
	}
	if family := strings.TrimSpace(resolved.BuiltinAncestor); family != "" {
		return family
	}
	return strings.TrimSpace(resolved.Name)
}

func (s *Server) decorateStreamMessage(info session.Info, event *SessionStreamMessageEvent) {
	snapshot := s.sessionPermissionModeSnapshot(info)
	if snapshot.Known {
		event.PermissionMode = snapshot.Mode
		event.ModeVersion = snapshot.Version
	}
}

func (s *Server) decorateRawStreamMessage(info session.Info, event *SessionStreamRawMessageEvent) {
	snapshot := s.sessionPermissionModeSnapshot(info)
	if snapshot.Known {
		event.PermissionMode = snapshot.Mode
		event.ModeVersion = snapshot.Version
	}
}

func (s *Server) sessionActivityEvent(info session.Info, activity string) SessionActivityEvent {
	snapshot := s.sessionPermissionModeSnapshot(info)
	return sessionActivityEventFromSnapshot(activity, snapshot)
}

func (s *Server) sessionPermissionModeHeaderValues(info session.Info) (string, string) {
	snapshot := s.sessionPermissionModeSnapshot(info)
	if !snapshot.Known {
		return "", ""
	}
	version := ""
	if snapshot.Version > 0 {
		version = strconv.FormatUint(snapshot.Version, 10)
	}
	return snapshot.Mode, version
}

func (s *Server) sessionStreamActivityPayload(info session.Info, activity string) sessionStreamActivityPayload {
	snapshot := s.sessionPermissionModeSnapshot(info)
	return sessionStreamActivityPayloadFromSnapshot(activity, snapshot)
}

type sessionPermissionModeStreamTracker struct {
	mode    string
	version uint64
}

func (t *sessionPermissionModeStreamTracker) update(snapshot sessionPermissionModeSnapshot) bool {
	if !snapshot.Known || (snapshot.Mode == t.mode && snapshot.Version == t.version) {
		return false
	}
	t.mode = snapshot.Mode
	t.version = snapshot.Version
	return true
}

func (s *Server) nextSessionPermissionModeActivityPayload(info session.Info, tracker *sessionPermissionModeStreamTracker, activity string) (sessionStreamActivityPayload, bool) {
	snapshot := s.sessionPermissionModeSnapshot(info)
	if tracker == nil || !tracker.update(snapshot) {
		return sessionStreamActivityPayload{}, false
	}
	return sessionStreamActivityPayloadFromSnapshot(activity, snapshot), true
}

func (s *Server) nextSessionPermissionModeActivityEvent(info session.Info, tracker *sessionPermissionModeStreamTracker, activity string) (SessionActivityEvent, bool) {
	snapshot := s.sessionPermissionModeSnapshot(info)
	if tracker == nil || !tracker.update(snapshot) {
		return SessionActivityEvent{}, false
	}
	return sessionActivityEventFromSnapshot(activity, snapshot), true
}

func sessionStreamActivityPayloadFromSnapshot(activity string, snapshot sessionPermissionModeSnapshot) sessionStreamActivityPayload {
	event := sessionStreamActivityPayload{Activity: activity}
	if snapshot.Known {
		event.PermissionMode = snapshot.Mode
		event.ModeVersion = snapshot.Version
	}
	return event
}

func sessionActivityEventFromSnapshot(activity string, snapshot sessionPermissionModeSnapshot) SessionActivityEvent {
	event := SessionActivityEvent{Activity: activity}
	if snapshot.Known {
		event.PermissionMode = snapshot.Mode
		event.ModeVersion = snapshot.Version
	}
	return event
}
