package api

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/session"
)

const (
	permissionModeOptionKey          = "permission_mode"
	permissionModeMetadataKey        = "permission_mode"
	permissionModeVersionMetadataKey = "permission_mode_version"
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
				Supported: false,
				Reason:    "permission mode is configured at launch only",
				Values:    stringPermissionModes(runtime.CanonicalPermissionModes()),
			}
		}
	}
	if b == nil {
		return
	}
	if mode, ok := runtime.NormalizePermissionMode(b.Metadata[permissionModeMetadataKey]); ok {
		setResponsePermissionMode(resp, mode, parseModeVersion(b.Metadata[permissionModeVersionMetadataKey]))
	}
}

func (s *Server) enrichLivePermissionMode(resp *sessionResponse, info session.Info) {
	if resp == nil || s == nil || s.state == nil || strings.TrimSpace(info.SessionName) == "" {
		return
	}
	reader, ok := s.state.SessionProvider().(runtime.PermissionModeReader)
	if !ok {
		return
	}
	capability := reader.PermissionModeCapability(info.SessionName, info.Provider)
	resp.Capabilities.PermissionMode = apiPermissionModeCapability(capability)
	if !capability.Supported || !capability.Readable || info.State != session.StateActive {
		return
	}
	state, err := reader.PermissionMode(context.Background(), info.SessionName, info.Provider)
	if err != nil {
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

func apiPermissionModeCapability(capability runtime.PermissionModeCapability) sessionPermissionModeCapability {
	values := capability.Values
	if len(values) == 0 && capability.Supported {
		values = runtime.CanonicalPermissionModes()
	}
	return sessionPermissionModeCapability{
		Supported:  capability.Supported,
		Readable:   capability.Readable,
		LiveSwitch: capability.LiveSwitch,
		Values:     stringPermissionModes(values),
		Reason:     capability.Reason,
	}
}

func permissionModeCapabilityForError(err error) runtime.PermissionModeCapability {
	switch {
	case errors.Is(err, runtime.ErrPermissionModeSwitchUnsupported):
		return runtime.PermissionModeCapability{
			Supported: true,
			Readable:  true,
			Values:    runtime.CanonicalPermissionModes(),
			Reason:    err.Error(),
		}
	case errors.Is(err, runtime.ErrPermissionModeUnsupported):
		return runtime.PermissionModeCapability{Reason: err.Error()}
	default:
		return runtime.PermissionModeCapability{
			Supported: true,
			Readable:  true,
			Values:    runtime.CanonicalPermissionModes(),
			Reason:    err.Error(),
		}
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

func parseModeVersion(value string) uint64 {
	n, _ := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	return n
}

func (s *Server) humaHandleSessionPermissionMode(ctx context.Context, input *SessionPermissionModeInput) (*SessionPermissionModeOutput, error) {
	store := s.state.CityBeadStore()
	if store == nil {
		return nil, huma.Error503ServiceUnavailable("no bead store configured")
	}
	id, err := s.resolveSessionIDAllowClosedWithConfig(store, input.ID)
	if err != nil {
		return nil, humaResolveError(err)
	}
	mgr := s.sessionManager(store)
	info, err := mgr.Get(id)
	if err != nil {
		return nil, humaSessionManagerError(err)
	}
	if info.Closed || info.State != session.StateActive {
		return nil, huma.Error409Conflict("not_running: session is not running")
	}
	mode, ok := runtime.NormalizePermissionMode(input.Body.PermissionMode)
	if !ok {
		return nil, huma.Error422UnprocessableEntity("invalid: " + runtime.ErrPermissionModeInvalid.Error())
	}
	sp := s.state.SessionProvider()
	if sp == nil || !sp.IsRunning(info.SessionName) {
		return nil, huma.Error409Conflict("not_running: session is not running")
	}
	switcher, ok := sp.(runtime.PermissionModeSwitcher)
	if !ok {
		return nil, huma.Error501NotImplemented("unsupported: " + runtime.ErrPermissionModeUnsupported.Error())
	}
	capability := switcher.PermissionModeCapability(info.SessionName, info.Provider)
	if !capability.Supported {
		return nil, huma.Error501NotImplemented("unsupported: " + firstNonEmptyString(capability.Reason, runtime.ErrPermissionModeUnsupported.Error()))
	}
	if !capability.LiveSwitch {
		return nil, huma.Error501NotImplemented("unsupported: " + firstNonEmptyString(capability.Reason, runtime.ErrPermissionModeSwitchUnsupported.Error()))
	}
	state, err := switcher.SetPermissionMode(ctx, info.SessionName, info.Provider, mode)
	if err != nil {
		return nil, humaPermissionModeError(err)
	}
	if state.Mode != mode {
		return nil, huma.Error502BadGateway(fmt.Sprintf("verification_failed: confirmed %q, want %q", state.Mode, mode))
	}
	if !state.Verified {
		return nil, huma.Error502BadGateway("verification_failed: provider did not verify permission mode")
	}
	version := state.Version
	if version == 0 {
		version = s.nextStoredModeVersion(store, id)
	}
	if err := store.SetMetadataBatch(id, map[string]string{
		permissionModeMetadataKey:        string(mode),
		permissionModeVersionMetadataKey: strconv.FormatUint(version, 10),
	}); err != nil {
		return nil, humaStoreError(err)
	}
	s.emitAsyncResult(events.SessionUpdated, id, SessionUpdatedPayload{
		SessionID:      id,
		SessionName:    info.SessionName,
		Provider:       info.Provider,
		PermissionMode: string(mode),
		ModeVersion:    version,
		Options:        map[string]string{permissionModeOptionKey: string(mode)},
	})
	out := &SessionPermissionModeOutput{}
	out.Body.ID = id
	out.Body.PermissionMode = string(mode)
	out.Body.ModeVersion = version
	out.Body.Verified = state.Verified
	return out, nil
}

func (s *Server) nextStoredModeVersion(store beads.Store, id string) uint64 {
	b, err := store.Get(id)
	if err != nil {
		return 1
	}
	return parseModeVersion(b.Metadata[permissionModeVersionMetadataKey]) + 1
}

func humaPermissionModeError(err error) error {
	switch {
	case errors.Is(err, runtime.ErrPermissionModeInvalid):
		return huma.Error422UnprocessableEntity("invalid: " + err.Error())
	case errors.Is(err, runtime.ErrPermissionModeUnsupported):
		return huma.Error501NotImplemented("unsupported: " + err.Error())
	case errors.Is(err, runtime.ErrPermissionModeSwitchUnsupported):
		return huma.Error501NotImplemented("unsupported: " + err.Error())
	case errors.Is(err, runtime.ErrPermissionModeVerificationFailed):
		return huma.Error502BadGateway("verification_failed: " + err.Error())
	case errors.Is(err, runtime.ErrSessionNotFound):
		return huma.Error409Conflict("not_running: " + err.Error())
	default:
		return huma.Error500InternalServerError("internal: " + err.Error())
	}
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
				snapshot.Mode = string(mode)
				snapshot.Version = parseModeVersion(b.Metadata[permissionModeVersionMetadataKey])
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
		}
	}
	reader, ok := s.state.SessionProvider().(runtime.PermissionModeReader)
	if !ok || strings.TrimSpace(info.SessionName) == "" || info.State != session.StateActive {
		return snapshot
	}
	capability := reader.PermissionModeCapability(info.SessionName, info.Provider)
	if !capability.Supported || !capability.Readable {
		return snapshot
	}
	state, err := reader.PermissionMode(context.Background(), info.SessionName, info.Provider)
	if err != nil || state.Mode == "" {
		return snapshot
	}
	snapshot.Mode = string(state.Mode)
	if state.Version > 0 {
		snapshot.Version = state.Version
	}
	snapshot.Known = true
	return snapshot
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
	event := SessionActivityEvent{Activity: activity}
	snapshot := s.sessionPermissionModeSnapshot(info)
	if snapshot.Known {
		event.PermissionMode = snapshot.Mode
		event.ModeVersion = snapshot.Version
	}
	return event
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
	event := sessionStreamActivityPayload{Activity: activity}
	snapshot := s.sessionPermissionModeSnapshot(info)
	if snapshot.Known {
		event.PermissionMode = snapshot.Mode
		event.ModeVersion = snapshot.Version
	}
	return event
}
