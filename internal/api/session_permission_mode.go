package api

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
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
	SessionID      string                 `json:"session_id,omitempty"`
	SessionName    string                 `json:"session_name,omitempty"`
	Provider       string                 `json:"provider,omitempty"`
	PermissionMode runtime.PermissionMode `json:"permission_mode,omitempty" enum:"default,acceptEdits,plan,bypassPermissions"`
	ModeVersion    uint64                 `json:"mode_version,omitempty"`
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
			setResponseRuntimePermissionMode(resp, mode, 0)
			resp.Capabilities.PermissionMode = sessionPermissionModeCapability{
				Supported: true,
				Values:    []string{string(mode)},
				Reason:    "permission mode is configured at launch only",
			}
		}
	}
	if b == nil {
		return
	}
	if mode, version, ok, _ := permissionModeFromBead(b); ok {
		setResponseRuntimePermissionMode(resp, mode, version)
	}
}

func (s *Server) enrichLivePermissionModeFromBead(resp *sessionResponse, info session.Info, b *beads.Bead) {
	if resp == nil || s == nil || s.state == nil || strings.TrimSpace(info.SessionName) == "" {
		return
	}
	_, _ = s.permissionModeStore().LoadStoredFromBead(info.ID, b)
	reader, ok := s.state.SessionProvider().(runtime.PermissionModeReader)
	if !ok {
		return
	}
	knownMode, knownVersion, knownModeState := responsePermissionMode(resp)
	configuredCapability := resp.Capabilities.PermissionMode
	provider := s.permissionModeRuntimeProviderFromBead(info, b)
	selection := selectPermissionModeReadCapability(reader, info.SessionName, provider, info.State, knownMode, knownModeState, configuredCapability)
	capability := selection.Capability
	if selection.UseConfiguredCapability {
		resp.Capabilities.PermissionMode = configuredCapability
		return
	}
	resp.Capabilities.PermissionMode = apiPermissionModeCapability(capability)
	if !capability.Supported || !capability.Readable || info.State != session.StateActive {
		return
	}
	state, err := reader.PermissionMode(context.Background(), info.SessionName, provider)
	if err != nil {
		if selection.UsedStatefulCapability && knownModeState {
			setResponseRuntimePermissionMode(resp, knownMode, knownVersion)
			return
		}
		if knownModeState && knownVersion > 0 {
			setResponseRuntimePermissionMode(resp, knownMode, knownVersion)
			return
		}
		if knownModeState && configuredCapability.Supported {
			setResponseRuntimePermissionMode(resp, knownMode, knownVersion)
			resp.Capabilities.PermissionMode = configuredCapability
			return
		}
		resp.Capabilities.PermissionMode = apiPermissionModeCapability(permissionModeCapabilityForError(err))
		return
	}
	if state.Mode != "" {
		setResponseRuntimePermissionMode(resp, state.Mode, state.Version)
	}
}

type permissionModeReadCapabilitySelection struct {
	Capability              runtime.PermissionModeCapability
	UsedStatefulCapability  bool
	UseConfiguredCapability bool
}

func selectPermissionModeReadCapability(reader runtime.PermissionModeReader, sessionName, provider string, state session.State, knownMode runtime.PermissionMode, hasKnownMode bool, configuredCapability sessionPermissionModeCapability) permissionModeReadCapabilitySelection {
	selection := permissionModeReadCapabilitySelection{
		Capability: reader.PermissionModeCapability(sessionName, provider),
	}
	if (!selection.Capability.Supported || !selection.Capability.Readable) && state == session.StateActive && hasKnownMode {
		if stateful, ok := reader.(runtime.PermissionModeStatefulSwitcher); ok {
			fallback := stateful.PermissionModeCapabilityForState(sessionName, provider, knownMode)
			if fallback.Supported {
				selection.Capability = fallback
				selection.UsedStatefulCapability = true
			}
		}
	}
	if !selection.Capability.Supported && hasKnownMode && configuredCapability.Supported {
		selection.UseConfiguredCapability = true
	}
	return selection
}

func setResponseRuntimePermissionMode(resp *sessionResponse, mode runtime.PermissionMode, version uint64) {
	if resp.Runtime == nil {
		resp.Runtime = &sessionRuntimeState{}
	}
	resp.Runtime.PermissionMode = mode
	if version > 0 {
		resp.Runtime.ModeVersion = version
	}
}

func responsePermissionMode(resp *sessionResponse) (runtime.PermissionMode, uint64, bool) {
	if resp == nil || resp.Runtime == nil {
		return "", 0, false
	}
	mode, ok := runtime.NormalizePermissionMode(string(resp.Runtime.PermissionMode))
	if !ok {
		return "", 0, false
	}
	return mode, resp.Runtime.ModeVersion, true
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

func (s *Server) sessionPermissionModeSnapshot(info session.Info) sessionPermissionModeSnapshot {
	var snapshot sessionPermissionModeSnapshot
	if s == nil || s.state == nil {
		return snapshot
	}
	modeStore := s.permissionModeStore()
	var err error
	snapshot, err = modeStore.LoadStored(info.ID)
	if err != nil && !snapshot.Known {
		s.warnPermissionMode("snapshot-store:"+info.ID, "session %s permission mode bead lookup failed: %v", info.ID, err)
	}
	if !snapshot.Known {
		if configured, cfgErr := modeStore.LoadConfigured(info.ID, info); cfgErr == nil {
			snapshot = configured
		} else if cfgErr != nil {
			s.warnPermissionMode("snapshot-configured:"+info.ID, "session %s permission mode configured lookup failed: %v", info.ID, cfgErr)
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
	s.sessionPermissionModeProjection(info).ApplyMessage(event)
}

func (s *Server) decorateRawStreamMessage(info session.Info, event *SessionStreamRawMessageEvent) {
	s.sessionPermissionModeProjection(info).ApplyRawMessage(event)
}

func (s *Server) sessionActivityEvent(info session.Info, activity string) SessionActivityEvent {
	return s.sessionPermissionModeProjection(info).ActivityEvent(activity)
}

func (s *Server) sessionPermissionModeHeaderValues(info session.Info) (string, string) {
	return s.sessionPermissionModeProjection(info).HeaderValues()
}

func (s *Server) sessionStreamActivityPayload(info session.Info, activity string) sessionStreamActivityPayload {
	return s.sessionPermissionModeProjection(info).ActivityPayload(activity)
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
	return sessionPermissionModeProjectionFromSnapshot(snapshot).ActivityPayload(activity), true
}

func (s *Server) nextSessionPermissionModeActivityEvent(info session.Info, tracker *sessionPermissionModeStreamTracker, activity string) (SessionActivityEvent, bool) {
	snapshot := s.sessionPermissionModeSnapshot(info)
	if tracker == nil || !tracker.update(snapshot) {
		return SessionActivityEvent{}, false
	}
	return sessionPermissionModeProjectionFromSnapshot(snapshot).ActivityEvent(activity), true
}
