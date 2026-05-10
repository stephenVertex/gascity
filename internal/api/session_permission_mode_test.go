package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/session"
)

const permissionModeTestProvider = "test-agent"

func configurePermissionModeProvider(fs *fakeState, defaultMode string) {
	fs.cfg.Agents[0].Provider = permissionModeTestProvider
	fs.cfg.Providers = map[string]config.ProviderSpec{
		permissionModeTestProvider: {
			DisplayName: "Permission Provider",
			Command:     "echo",
			OptionDefaults: map[string]string{
				"permission_mode": defaultMode,
			},
			OptionsSchema: []config.ProviderOption{
				{
					Key:     "permission_mode",
					Label:   "Permission Mode",
					Type:    "select",
					Default: defaultMode,
					Choices: []config.OptionChoice{
						{Value: "normal", Label: "Default"},
						{Value: "auto-edit", Label: "Accept edits"},
						{Value: "plan", Label: "Plan"},
						{Value: "unrestricted", Label: "Bypass permissions"},
					},
				},
			},
		},
	}
}

func createPermissionModeSession(t *testing.T, fs *fakeState) session.Info {
	t.Helper()
	mgr := session.NewManager(fs.cityBeadStore, fs.sp)
	info, err := mgr.Create(context.Background(), "myrig/worker", "Permission", permissionModeTestProvider, t.TempDir(), permissionModeTestProvider, nil, session.ProviderResume{}, runtime.Config{})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return info
}

func TestSessionReadsExposeCanonicalPermissionMode(t *testing.T) {
	fs := newSessionFakeState(t)
	configurePermissionModeProvider(fs, "unrestricted")
	srv := New(fs)
	h := newTestCityHandlerWith(t, fs, srv)
	info := createPermissionModeSession(t, fs)
	fs.sp.SetPermissionModeState(info.SessionName, runtime.PermissionModeState{
		Mode:     runtime.PermissionModeAcceptEdits,
		Version:  11,
		Verified: true,
	})

	listReq := httptest.NewRequest(http.MethodGet, cityURL(fs, "/sessions"), nil)
	listRec := httptest.NewRecorder()
	h.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d; body: %s", listRec.Code, http.StatusOK, listRec.Body.String())
	}
	var list struct {
		Items []sessionResponse `json:"items"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("list items = %d, want 1", len(list.Items))
	}
	assertPermissionModeResponse(t, list.Items[0], "acceptEdits", 11, true)

	getReq := httptest.NewRequest(http.MethodGet, cityURL(fs, "/session/")+info.ID, nil)
	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d; body: %s", getRec.Code, http.StatusOK, getRec.Body.String())
	}
	var detail sessionResponse
	if err := json.Unmarshal(getRec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	assertPermissionModeResponse(t, detail, "acceptEdits", 11, true)
}

func TestSessionReadCanonicalizesConfiguredPermissionMode(t *testing.T) {
	fs := newSessionFakeState(t)
	configurePermissionModeProvider(fs, "unrestricted")
	h := newTestCityHandler(t, fs)
	info := createPermissionModeSession(t, fs)

	req := httptest.NewRequest(http.MethodGet, cityURL(fs, "/session/")+info.ID, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var detail sessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if got := detail.Options["permission_mode"]; got != "bypassPermissions" {
		t.Fatalf("options.permission_mode = %q, want bypassPermissions", got)
	}
	capability := detail.Capabilities.PermissionMode
	if !capability.Supported || !capability.Readable || !capability.LiveSwitch {
		t.Fatalf("permission mode capability = %+v, want supported readable live_switch", capability)
	}
	for _, want := range []string{"default", "acceptEdits", "plan", "bypassPermissions"} {
		if !containsPermissionModeValue(capability.Values, want) {
			t.Fatalf("permission mode values = %v, want %s", capability.Values, want)
		}
	}
}

func TestProviderSessionReadExposesConfiguredPermissionMode(t *testing.T) {
	fs := newSessionFakeState(t)
	configurePermissionModeProvider(fs, "unrestricted")
	h := newTestCityHandler(t, fs)
	mgr := session.NewManager(fs.cityBeadStore, fs.sp)
	info, err := mgr.Create(context.Background(), permissionModeTestProvider, "Provider Permission", permissionModeTestProvider, t.TempDir(), permissionModeTestProvider, nil, session.ProviderResume{}, runtime.Config{})
	if err != nil {
		t.Fatalf("create provider session: %v", err)
	}
	fs.sp.SetPermissionModeCapability(info.SessionName, runtime.PermissionModeCapability{
		Supported: true,
		Values:    runtime.CanonicalPermissionModes(),
		Reason:    "permission mode is configured at launch only",
	})

	req := httptest.NewRequest(http.MethodGet, cityURL(fs, "/session/")+info.ID, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var detail sessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if got := detail.Options["permission_mode"]; got != "bypassPermissions" {
		t.Fatalf("options.permission_mode = %q, want bypassPermissions", got)
	}
	capability := detail.Capabilities.PermissionMode
	if !capability.Supported || capability.Readable || capability.LiveSwitch {
		t.Fatalf("permission mode capability = %+v, want launch-only supported", capability)
	}
	if !containsPermissionModeValue(capability.Values, "bypassPermissions") {
		t.Fatalf("permission mode values = %v, want bypassPermissions", capability.Values)
	}
}

func TestSessionReadsExposeUnsupportedPermissionCapability(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)
	info := createTestSession(t, fs.cityBeadStore, fs.sp, "Unsupported")

	req := httptest.NewRequest(http.MethodGet, cityURL(fs, "/session/")+info.ID, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var detail sessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	capability := detail.Capabilities.PermissionMode
	if capability.Supported {
		t.Fatalf("permission mode capability supported = true, want false")
	}
	if strings.TrimSpace(capability.Reason) == "" {
		t.Fatal("permission mode capability reason is empty")
	}
}

func TestSessionReadUsesConfiguredModeForStatefulFallbackWhenLiveModeCannotBeRead(t *testing.T) {
	fs := newSessionFakeState(t)
	configurePermissionModeProvider(fs, "normal")
	srv := New(fs)
	h := newTestCityHandlerWith(t, fs, srv)
	info := createPermissionModeSession(t, fs)
	fs.sp.SetPermissionModeState(info.SessionName, runtime.PermissionModeState{
		Mode:     runtime.PermissionModeDefault,
		Version:  1,
		Verified: true,
	})
	fs.sp.PermissionModeReadErrors[info.SessionName] = runtime.ErrPermissionModeUnsupported

	req := httptest.NewRequest(http.MethodGet, cityURL(fs, "/session/")+info.ID, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var detail sessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	capability := detail.Capabilities.PermissionMode
	if !capability.Supported || !capability.Readable || !capability.LiveSwitch {
		t.Fatalf("permission mode capability = %+v, want supported readable live_switch", capability)
	}
	if !containsPermissionModeValue(capability.Values, "acceptEdits") {
		t.Fatalf("permission mode values = %v, want acceptEdits", capability.Values)
	}
}

func TestSetSessionPermissionModeAppliesAndReturnsConfirmedMode(t *testing.T) {
	fs := newSessionFakeState(t)
	configurePermissionModeProvider(fs, "normal")
	srv := New(fs)
	h := newTestCityHandlerWith(t, fs, srv)
	info := createPermissionModeSession(t, fs)
	fs.sp.SetPermissionModeState(info.SessionName, runtime.PermissionModeState{
		Mode:     runtime.PermissionModeDefault,
		Version:  3,
		Verified: true,
	})

	req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/permission-mode", strings.NewReader(`{"permission_mode":"acceptEdits"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var out struct {
		ID             string `json:"id"`
		PermissionMode string `json:"permission_mode"`
		ModeVersion    uint64 `json:"mode_version"`
		Verified       bool   `json:"verified"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.ID != info.ID {
		t.Fatalf("id = %q, want %q", out.ID, info.ID)
	}
	if out.PermissionMode != "acceptEdits" {
		t.Fatalf("permission_mode = %q, want acceptEdits", out.PermissionMode)
	}
	if out.ModeVersion <= 3 {
		t.Fatalf("mode_version = %d, want greater than 3", out.ModeVersion)
	}
	if !out.Verified {
		t.Fatal("verified = false, want true")
	}
	state, err := fs.sp.PermissionMode(context.Background(), info.SessionName, permissionModeTestProvider)
	if err != nil {
		t.Fatalf("read fake mode: %v", err)
	}
	if state.Mode != runtime.PermissionModeAcceptEdits {
		t.Fatalf("fake mode = %q, want %q", state.Mode, runtime.PermissionModeAcceptEdits)
	}
	evts, err := fs.eventProv.List(events.Filter{Type: events.SessionUpdated})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(evts) == 0 {
		t.Fatal("no session.updated event recorded")
	}
	var payload SessionUpdatedPayload
	if err := json.Unmarshal(evts[len(evts)-1].Payload, &payload); err != nil {
		t.Fatalf("decode session.updated payload: %v", err)
	}
	if payload.PermissionMode != "acceptEdits" {
		t.Fatalf("event permission_mode = %q, want acceptEdits", payload.PermissionMode)
	}
	if payload.ModeVersion != out.ModeVersion {
		t.Fatalf("event mode_version = %d, want %d", payload.ModeVersion, out.ModeVersion)
	}
}

func TestSetSessionPermissionModeUsesStoredProviderFamily(t *testing.T) {
	fs := newSessionFakeState(t)
	configurePermissionModeProvider(fs, "normal")
	srv := New(fs)
	h := newTestCityHandlerWith(t, fs, srv)
	info := createPermissionModeSession(t, fs)
	fs.sp.SetPermissionModeState(info.SessionName, runtime.PermissionModeState{
		Mode:     runtime.PermissionModeDefault,
		Version:  1,
		Verified: true,
	})
	if err := fs.cityBeadStore.SetMetadataBatch(info.ID, map[string]string{
		"builtin_ancestor": "claude",
	}); err != nil {
		t.Fatalf("seed provider family metadata: %v", err)
	}

	req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/permission-mode", strings.NewReader(`{"permission_mode":"acceptEdits"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	for _, call := range fs.sp.Calls {
		if call.Method == "SetPermissionMode" && call.Name == info.SessionName {
			if call.Key != "claude" {
				t.Fatalf("SetPermissionMode provider = %q, want claude; calls=%#v", call.Key, fs.sp.Calls)
			}
			return
		}
	}
	t.Fatalf("SetPermissionMode call missing; calls=%#v", fs.sp.Calls)
}

func TestSetSessionPermissionModeRejectsUnadvertisedMode(t *testing.T) {
	fs := newSessionFakeState(t)
	configurePermissionModeProvider(fs, "normal")
	srv := New(fs)
	h := newTestCityHandlerWith(t, fs, srv)
	info := createPermissionModeSession(t, fs)
	fs.sp.SetPermissionModeState(info.SessionName, runtime.PermissionModeState{
		Mode:     runtime.PermissionModeDefault,
		Version:  1,
		Verified: true,
	})
	fs.sp.SetPermissionModeCapability(info.SessionName, runtime.PermissionModeCapability{
		Supported:  true,
		Readable:   true,
		LiveSwitch: true,
		Values: []runtime.PermissionMode{
			runtime.PermissionModeDefault,
			runtime.PermissionModeAcceptEdits,
		},
	})

	req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/permission-mode", strings.NewReader(`{"permission_mode":"plan"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusNotImplemented, rec.Body.String())
	}
	state, err := fs.sp.PermissionMode(context.Background(), info.SessionName, permissionModeTestProvider)
	if err != nil {
		t.Fatalf("read fake mode: %v", err)
	}
	if state.Mode != runtime.PermissionModeDefault {
		t.Fatalf("fake mode = %q, want default", state.Mode)
	}
}

func TestSetSessionPermissionModeRejectsLaunchOnlyProvider(t *testing.T) {
	fs := newSessionFakeState(t)
	configurePermissionModeProvider(fs, "normal")
	srv := New(fs)
	h := newTestCityHandlerWith(t, fs, srv)
	info := createPermissionModeSession(t, fs)
	fs.sp.SetPermissionModeCapability(info.SessionName, runtime.PermissionModeCapability{
		Supported: true,
		Values:    runtime.CanonicalPermissionModes(),
		Reason:    "permission mode is configured at launch only",
	})

	req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/permission-mode", strings.NewReader(`{"permission_mode":"acceptEdits"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusNotImplemented, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "configured at launch only") {
		t.Fatalf("body missing launch-only reason: %s", rec.Body.String())
	}
}

func TestSetSessionPermissionModeUsesMonotonicStoredVersion(t *testing.T) {
	fs := newSessionFakeState(t)
	configurePermissionModeProvider(fs, "normal")
	srv := New(fs)
	h := newTestCityHandlerWith(t, fs, srv)
	info := createPermissionModeSession(t, fs)
	fs.sp.SetPermissionModeState(info.SessionName, runtime.PermissionModeState{
		Mode:     runtime.PermissionModeDefault,
		Version:  1,
		Verified: true,
	})
	if err := fs.cityBeadStore.SetMetadataBatch(info.ID, map[string]string{
		permissionModeMetadataKey:        string(runtime.PermissionModeDefault),
		permissionModeVersionMetadataKey: "9",
	}); err != nil {
		t.Fatalf("seed mode metadata: %v", err)
	}

	req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/permission-mode", strings.NewReader(`{"permission_mode":"acceptEdits"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var out struct {
		ModeVersion uint64 `json:"mode_version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.ModeVersion != 10 {
		t.Fatalf("mode_version = %d, want 10", out.ModeVersion)
	}
}

func TestSetSessionPermissionModeRejectsInvalidStoredVersion(t *testing.T) {
	fs := newSessionFakeState(t)
	configurePermissionModeProvider(fs, "normal")
	srv := New(fs)
	h := newTestCityHandlerWith(t, fs, srv)
	info := createPermissionModeSession(t, fs)
	fs.sp.SetPermissionModeState(info.SessionName, runtime.PermissionModeState{
		Mode:     runtime.PermissionModeDefault,
		Version:  1,
		Verified: true,
	})
	if err := fs.cityBeadStore.SetMetadataBatch(info.ID, map[string]string{
		permissionModeMetadataKey:        string(runtime.PermissionModeDefault),
		permissionModeVersionMetadataKey: "not-a-version",
	}); err != nil {
		t.Fatalf("seed mode metadata: %v", err)
	}

	req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/permission-mode", strings.NewReader(`{"permission_mode":"acceptEdits"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid permission mode version") {
		t.Fatalf("body missing invalid version diagnostic: %s", rec.Body.String())
	}
}

func TestSetSessionPermissionModeReturnsUnverifiedWhenPostSwitchReadUnsupported(t *testing.T) {
	fs := newSessionFakeState(t)
	configurePermissionModeProvider(fs, "normal")
	srv := New(fs)
	h := newTestCityHandlerWith(t, fs, srv)
	info := createPermissionModeSession(t, fs)
	fs.sp.SetPermissionModeState(info.SessionName, runtime.PermissionModeState{
		Mode:     runtime.PermissionModeDefault,
		Version:  1,
		Verified: true,
	})
	fs.sp.PermissionModeSetErrors[info.SessionName] = fmt.Errorf("%w: %w", runtime.ErrPermissionModeVerificationFailed, runtime.ErrPermissionModeUnsupported)

	req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/permission-mode", strings.NewReader(`{"permission_mode":"acceptEdits"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var out struct {
		ID             string `json:"id"`
		PermissionMode string `json:"permission_mode"`
		ModeVersion    uint64 `json:"mode_version"`
		Verified       bool   `json:"verified"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.ID != info.ID || out.PermissionMode != "acceptEdits" || out.Verified {
		t.Fatalf("permission mode output = %+v, want acceptEdits with verified=false", out)
	}
	if out.ModeVersion == 0 {
		t.Fatalf("mode_version = 0, want nonzero")
	}
}

func TestSetSessionPermissionModeUsesStoredModeWhenLiveReadUnavailable(t *testing.T) {
	fs := newSessionFakeState(t)
	configurePermissionModeProvider(fs, "normal")
	srv := New(fs)
	h := newTestCityHandlerWith(t, fs, srv)
	info := createPermissionModeSession(t, fs)
	fs.sp.SetPermissionModeState(info.SessionName, runtime.PermissionModeState{
		Mode:     runtime.PermissionModeAcceptEdits,
		Version:  4,
		Verified: true,
	})
	if err := fs.cityBeadStore.SetMetadataBatch(info.ID, map[string]string{
		permissionModeMetadataKey:        string(runtime.PermissionModeAcceptEdits),
		permissionModeVersionMetadataKey: "4",
	}); err != nil {
		t.Fatalf("seed mode metadata: %v", err)
	}
	fs.sp.PermissionModeReadErrors[info.SessionName] = runtime.ErrPermissionModeUnsupported

	req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/permission-mode", strings.NewReader(`{"permission_mode":"plan"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var out struct {
		PermissionMode string `json:"permission_mode"`
		ModeVersion    uint64 `json:"mode_version"`
		Verified       bool   `json:"verified"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.PermissionMode != "plan" || out.ModeVersion != 5 || out.Verified {
		t.Fatalf("permission mode output = %+v, want unverified plan version 5", out)
	}
}

func TestSessionReadKeepsStoredModeCapabilityWhenLiveReadUnavailable(t *testing.T) {
	fs := newSessionFakeState(t)
	configurePermissionModeProvider(fs, "normal")
	srv := New(fs)
	h := newTestCityHandlerWith(t, fs, srv)
	info := createPermissionModeSession(t, fs)
	fs.sp.SetPermissionModeState(info.SessionName, runtime.PermissionModeState{
		Mode:     runtime.PermissionModeAcceptEdits,
		Version:  6,
		Verified: true,
	})
	if err := fs.cityBeadStore.SetMetadataBatch(info.ID, map[string]string{
		permissionModeMetadataKey:        string(runtime.PermissionModeAcceptEdits),
		permissionModeVersionMetadataKey: "6",
	}); err != nil {
		t.Fatalf("seed mode metadata: %v", err)
	}
	fs.sp.PermissionModeReadErrors[info.SessionName] = runtime.ErrPermissionModeUnsupported

	req := httptest.NewRequest(http.MethodGet, cityURL(fs, "/session/")+info.ID, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var detail sessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if got := detail.Options["permission_mode"]; got != "acceptEdits" {
		t.Fatalf("options.permission_mode = %q, want acceptEdits", got)
	}
	if detail.ModeVersion != 6 {
		t.Fatalf("mode_version = %d, want 6", detail.ModeVersion)
	}
	capability := detail.Capabilities.PermissionMode
	if !capability.Supported || !capability.Readable || !capability.LiveSwitch {
		t.Fatalf("permission mode capability = %+v, want supported readable live_switch", capability)
	}
	if !containsPermissionModeValue(capability.Values, "plan") {
		t.Fatalf("permission mode values = %v, want plan", capability.Values)
	}
}

func TestSetSessionPermissionModeVerificationMismatchReturnsBadGateway(t *testing.T) {
	fs := newSessionFakeState(t)
	configurePermissionModeProvider(fs, "normal")
	srv := New(fs)
	h := newTestCityHandlerWith(t, fs, srv)
	info := createPermissionModeSession(t, fs)
	fs.sp.SetPermissionModeState(info.SessionName, runtime.PermissionModeState{
		Mode:     runtime.PermissionModeDefault,
		Version:  1,
		Verified: true,
	})
	fs.sp.PermissionModeSetErrors[info.SessionName] = fmt.Errorf("%w: confirmed %q, want %q", runtime.ErrPermissionModeVerificationFailed, runtime.PermissionModeDefault, runtime.PermissionModeAcceptEdits)

	req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/permission-mode", strings.NewReader(`{"permission_mode":"acceptEdits"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadGateway, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "verification_failed") {
		t.Fatalf("body missing verification_failed: %s", rec.Body.String())
	}
}

func TestSessionPermissionModeLockSerializesSameSession(t *testing.T) {
	fs := newSessionFakeState(t)
	srv := New(fs)

	unlock := srv.lockSessionPermissionMode("session-1")
	acquired := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		release := srv.lockSessionPermissionMode("session-1")
		close(acquired)
		release()
	}()

	select {
	case <-acquired:
		t.Fatal("second lock acquired before first lock released")
	case <-time.After(50 * time.Millisecond):
	}

	unlock()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("second lock did not acquire after first lock released")
	}
	wg.Wait()
}

func TestSessionStreamIncludesPermissionMode(t *testing.T) {
	fs := newSessionFakeState(t)
	configurePermissionModeProvider(fs, "normal")
	srv := New(fs)
	h := newTestCityHandlerWith(t, fs, srv)
	info := createPermissionModeSession(t, fs)
	fs.sp.SetPermissionModeState(info.SessionName, runtime.PermissionModeState{
		Mode:     runtime.PermissionModeAcceptEdits,
		Version:  17,
		Verified: true,
	})
	fs.sp.SetPeekOutput(info.SessionName, "hello")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, cityURL(fs, "/session/")+info.ID+"/stream", nil).WithContext(ctx)
	rec := newSyncResponseRecorder()
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(rec, req)
		close(done)
	}()

	body := waitForRecorderSubstring(t, rec, `"permission_mode":"acceptEdits"`, time.Second)
	cancel()
	<-done
	if !strings.Contains(body, `"mode_version":17`) {
		t.Fatalf("stream body missing mode_version: %s", body)
	}
	if !strings.Contains(body, "hello") {
		t.Fatalf("stream body missing output: %s", body)
	}
}

func TestNormalizePermissionModeAliases(t *testing.T) {
	tests := map[string]runtime.PermissionMode{
		"default":           runtime.PermissionModeDefault,
		"normal":            runtime.PermissionModeDefault,
		"acceptEdits":       runtime.PermissionModeAcceptEdits,
		"auto-edit":         runtime.PermissionModeAcceptEdits,
		"auto_edit":         runtime.PermissionModeAcceptEdits,
		"plan":              runtime.PermissionModePlan,
		"bypassPermissions": runtime.PermissionModeBypassPermissions,
		"unrestricted":      runtime.PermissionModeBypassPermissions,
	}
	for input, want := range tests {
		got, ok := runtime.NormalizePermissionMode(input)
		if !ok {
			t.Fatalf("NormalizePermissionMode(%q) unsupported", input)
		}
		if got != want {
			t.Fatalf("NormalizePermissionMode(%q) = %q, want %q", input, got, want)
		}
	}
}

func assertPermissionModeResponse(t *testing.T, resp sessionResponse, wantMode string, wantVersion uint64, wantLiveSwitch bool) {
	t.Helper()
	if resp.Options == nil {
		t.Fatal("options is nil")
	}
	if got := resp.Options["permission_mode"]; got != wantMode {
		t.Fatalf("options.permission_mode = %q, want %q", got, wantMode)
	}
	if resp.ModeVersion != wantVersion {
		t.Fatalf("mode_version = %d, want %d", resp.ModeVersion, wantVersion)
	}
	capability := resp.Capabilities.PermissionMode
	if !capability.Supported {
		t.Fatalf("permission mode capability supported = false, want true: %s", capability.Reason)
	}
	if !capability.Readable {
		t.Fatal("permission mode capability readable = false, want true")
	}
	if capability.LiveSwitch != wantLiveSwitch {
		t.Fatalf("permission mode capability live_switch = %v, want %v", capability.LiveSwitch, wantLiveSwitch)
	}
}

func containsPermissionModeValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
