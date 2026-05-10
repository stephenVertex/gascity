package api

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/session"
)

type permissionModeWarningFunc func(key, format string, args ...any)

type sessionPermissionModeStore struct {
	store beads.Store
	cfg   *config.City
	warn  permissionModeWarningFunc
}

type sessionPermissionModeSnapshot struct {
	Mode    string
	Version uint64
	Known   bool
}

func newSessionPermissionModeStore(store beads.Store, cfg *config.City, warn permissionModeWarningFunc) sessionPermissionModeStore {
	return sessionPermissionModeStore{
		store: store,
		cfg:   cfg,
		warn:  warn,
	}
}

func (s *Server) permissionModeStore() sessionPermissionModeStore {
	if s == nil || s.state == nil {
		return newSessionPermissionModeStore(nil, nil, nil)
	}
	return newSessionPermissionModeStore(s.state.CityBeadStore(), s.state.Config(), s.warnPermissionMode)
}

func (s sessionPermissionModeStore) LoadStored(id string) (sessionPermissionModeSnapshot, error) {
	if s.store == nil {
		return sessionPermissionModeSnapshot{}, nil
	}
	b, err := s.store.Get(id)
	if err != nil {
		return sessionPermissionModeSnapshot{}, err
	}
	return s.LoadStoredFromBead(id, &b)
}

func (s sessionPermissionModeStore) LoadStoredFromBead(id string, b *beads.Bead) (sessionPermissionModeSnapshot, error) {
	mode, version, ok, err := permissionModeFromBead(b)
	if !ok {
		return sessionPermissionModeSnapshot{}, nil
	}
	snapshot := sessionPermissionModeSnapshot{
		Mode:    string(mode),
		Version: version,
		Known:   true,
	}
	if err != nil {
		s.warnInvalidVersion(id, err)
	}
	return snapshot, err
}

func (s sessionPermissionModeStore) LoadConfigured(id string, info session.Info) (sessionPermissionModeSnapshot, error) {
	if s.store == nil {
		return sessionPermissionModeSnapshot{}, nil
	}
	b, err := s.store.Get(id)
	if err != nil {
		return sessionPermissionModeSnapshot{}, err
	}
	return s.LoadConfiguredFromBead(info, &b), nil
}

func (s sessionPermissionModeStore) LoadConfiguredFromBead(info session.Info, b *beads.Bead) sessionPermissionModeSnapshot {
	resp := sessionResponseWithReason(info, b, s.cfg, false)
	mode, version, ok := responsePermissionMode(&resp)
	if !ok {
		return sessionPermissionModeSnapshot{}
	}
	return sessionPermissionModeSnapshot{
		Mode:    string(mode),
		Version: version,
		Known:   true,
	}
}

func (s sessionPermissionModeStore) SaveNext(id string, mode runtime.PermissionMode, providerVersion uint64) (uint64, error) {
	if s.store == nil {
		return 0, fmt.Errorf("no bead store configured")
	}
	version := providerVersion
	nextVersion, err := s.NextVersion(id)
	if err != nil {
		return 0, err
	}
	if version < nextVersion {
		version = nextVersion
	}
	if err := s.store.SetMetadataBatch(id, map[string]string{
		permissionModeMetadataKey:        string(mode),
		permissionModeVersionMetadataKey: strconv.FormatUint(version, 10),
	}); err != nil {
		return 0, err
	}
	return version, nil
}

func (s sessionPermissionModeStore) NextVersion(id string) (uint64, error) {
	if s.store == nil {
		return 0, fmt.Errorf("no bead store configured")
	}
	b, err := s.store.Get(id)
	if err != nil {
		return 0, err
	}
	version, err := parseModeVersion(b.Metadata[permissionModeVersionMetadataKey])
	if err != nil {
		s.warnInvalidVersion(id, err)
		return 0, err
	}
	return version + 1, nil
}

func (s sessionPermissionModeStore) warnInvalidVersion(id string, err error) {
	if s.warn == nil || err == nil {
		return
	}
	s.warn("mode-version:"+id, "session %s permission mode version ignored: %v", id, err)
}

func snapshotRuntimePermissionMode(snapshot sessionPermissionModeSnapshot) (runtime.PermissionMode, bool) {
	if !snapshot.Known {
		return "", false
	}
	return runtime.NormalizePermissionMode(snapshot.Mode)
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

func permissionModeFromBead(b *beads.Bead) (runtime.PermissionMode, uint64, bool, error) {
	if b == nil {
		return "", 0, false, nil
	}
	mode, ok := runtime.NormalizePermissionMode(b.Metadata[permissionModeMetadataKey])
	if !ok {
		return "", 0, false, nil
	}
	version, err := parseModeVersion(b.Metadata[permissionModeVersionMetadataKey])
	return mode, version, true, err
}
