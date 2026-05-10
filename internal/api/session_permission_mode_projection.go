package api

import (
	"strconv"

	"github.com/gastownhall/gascity/internal/session"
)

type sessionPermissionModeProjection struct {
	snapshot sessionPermissionModeSnapshot
}

func sessionPermissionModeProjectionFromSnapshot(snapshot sessionPermissionModeSnapshot) sessionPermissionModeProjection {
	return sessionPermissionModeProjection{snapshot: snapshot}
}

func (s *Server) sessionPermissionModeProjection(info session.Info) sessionPermissionModeProjection {
	return sessionPermissionModeProjectionFromSnapshot(s.sessionPermissionModeSnapshot(info))
}

func (p sessionPermissionModeProjection) ApplyMessage(event *SessionStreamMessageEvent) {
	if event == nil || !p.snapshot.Known {
		return
	}
	event.PermissionMode = p.snapshot.Mode
	event.ModeVersion = p.snapshot.Version
}

func (p sessionPermissionModeProjection) ApplyRawMessage(event *SessionStreamRawMessageEvent) {
	if event == nil || !p.snapshot.Known {
		return
	}
	event.PermissionMode = p.snapshot.Mode
	event.ModeVersion = p.snapshot.Version
}

func (p sessionPermissionModeProjection) ActivityPayload(activity string) sessionStreamActivityPayload {
	event := sessionStreamActivityPayload{Activity: activity}
	if p.snapshot.Known {
		event.PermissionMode = p.snapshot.Mode
		event.ModeVersion = p.snapshot.Version
	}
	return event
}

func (p sessionPermissionModeProjection) ActivityEvent(activity string) SessionActivityEvent {
	event := SessionActivityEvent{Activity: activity}
	if p.snapshot.Known {
		event.PermissionMode = p.snapshot.Mode
		event.ModeVersion = p.snapshot.Version
	}
	return event
}

func (p sessionPermissionModeProjection) HeaderValues() (string, string) {
	if !p.snapshot.Known {
		return "", ""
	}
	version := ""
	if p.snapshot.Version > 0 {
		version = strconv.FormatUint(p.snapshot.Version, 10)
	}
	return p.snapshot.Mode, version
}
