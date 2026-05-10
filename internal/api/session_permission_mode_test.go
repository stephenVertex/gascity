package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
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
	if got := list.Items[0].Options["permission_mode"]; got != "unrestricted" {
		t.Fatalf("list options.permission_mode = %q, want schema-backed unrestricted", got)
	}

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
	if got := detail.Options["permission_mode"]; got != "unrestricted" {
		t.Fatalf("detail options.permission_mode = %q, want schema-backed unrestricted", got)
	}
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
	if got := detail.Options["permission_mode"]; got != "unrestricted" {
		t.Fatalf("options.permission_mode = %q, want schema-backed unrestricted", got)
	}
	if detail.Runtime == nil {
		t.Fatal("runtime is nil")
	}
	if got := string(detail.Runtime.PermissionMode); got != "bypassPermissions" {
		t.Fatalf("runtime.permission_mode = %q, want bypassPermissions", got)
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
	if got := detail.Options["permission_mode"]; got != "unrestricted" {
		t.Fatalf("options.permission_mode = %q, want schema-backed unrestricted", got)
	}
	if detail.Runtime == nil {
		t.Fatal("runtime is nil")
	}
	if got := string(detail.Runtime.PermissionMode); got != "bypassPermissions" {
		t.Fatalf("runtime.permission_mode = %q, want bypassPermissions", got)
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

func TestSelectPermissionModeReadCapabilityUsesStatefulKnownMode(t *testing.T) {
	f := runtime.NewFake()
	if err := f.Start(context.Background(), "session-a", runtime.Config{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	f.PermissionModeReadErrors["session-a"] = runtime.ErrPermissionModeUnsupported

	selection := selectPermissionModeReadCapability(
		f,
		"session-a",
		"claude",
		session.StateActive,
		runtime.PermissionModeDefault,
		true,
		sessionPermissionModeCapability{Supported: true},
	)
	if !selection.UsedStatefulCapability {
		t.Fatalf("UsedStatefulCapability = false, want true; selection=%+v", selection)
	}
	if !selection.Capability.Supported || !selection.Capability.Readable || !selection.Capability.LiveSwitch {
		t.Fatalf("Capability = %+v, want supported readable live_switch", selection.Capability)
	}
	if !permissionModeCapabilityAllows(selection.Capability, runtime.PermissionModeAcceptEdits) {
		t.Fatalf("Capability values = %v, want acceptEdits", selection.Capability.Values)
	}
}

func TestSessionReadLogsInvalidStoredPermissionModeVersion(t *testing.T) {
	fs := newSessionFakeState(t)
	configurePermissionModeProvider(fs, "normal")
	srv := New(fs)
	h := newTestCityHandlerWith(t, fs, srv)
	info := createPermissionModeSession(t, fs)
	if err := fs.cityBeadStore.SetMetadataBatch(info.ID, map[string]string{
		permissionModeMetadataKey:        string(runtime.PermissionModeAcceptEdits),
		permissionModeVersionMetadataKey: "not-a-version",
	}); err != nil {
		t.Fatalf("seed mode metadata: %v", err)
	}
	fs.sp.PermissionModeReadErrors[info.SessionName] = runtime.ErrPermissionModeUnsupported

	var logs bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previousWriter)

	req := httptest.NewRequest(http.MethodGet, cityURL(fs, "/session/")+info.ID, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := logs.String(); !strings.Contains(got, "invalid permission mode version") {
		t.Fatalf("log output missing invalid version warning:\n%s", got)
	}
}

func TestPermissionModeStoreLoadsAndSavesRuntimeMode(t *testing.T) {
	fs := newSessionFakeState(t)
	configurePermissionModeProvider(fs, "unrestricted")
	info := createPermissionModeSession(t, fs)
	var warnings []string
	store := newSessionPermissionModeStore(fs.cityBeadStore, fs.cfg, func(key, format string, args ...any) {
		warnings = append(warnings, key+": "+fmt.Sprintf(format, args...))
	})

	configured, err := store.LoadConfigured(info.ID, info)
	if err != nil {
		t.Fatalf("LoadConfigured: %v", err)
	}
	if !configured.Known || configured.Mode != "bypassPermissions" || configured.Version != 0 {
		t.Fatalf("configured snapshot = %+v, want bypassPermissions version 0", configured)
	}

	version, err := store.SaveNext(info.ID, runtime.PermissionModeAcceptEdits, 4)
	if err != nil {
		t.Fatalf("SaveNext provider version: %v", err)
	}
	if version != 4 {
		t.Fatalf("SaveNext provider version = %d, want 4", version)
	}
	version, err = store.SaveNext(info.ID, runtime.PermissionModePlan, 2)
	if err != nil {
		t.Fatalf("SaveNext stored version: %v", err)
	}
	if version != 5 {
		t.Fatalf("SaveNext stored version = %d, want 5", version)
	}
	stored, err := store.LoadStored(info.ID)
	if err != nil {
		t.Fatalf("LoadStored: %v", err)
	}
	if !stored.Known || stored.Mode != "plan" || stored.Version != 5 {
		t.Fatalf("stored snapshot = %+v, want plan version 5", stored)
	}

	if err := fs.cityBeadStore.SetMetadata(info.ID, permissionModeVersionMetadataKey, "bogus"); err != nil {
		t.Fatalf("seed invalid version: %v", err)
	}
	stored, err = store.LoadStored(info.ID)
	if err == nil {
		t.Fatal("LoadStored invalid version err = nil, want error")
	}
	if !stored.Known || stored.Mode != "plan" {
		t.Fatalf("invalid-version snapshot = %+v, want known plan", stored)
	}
	if len(warnings) == 0 || !strings.Contains(warnings[len(warnings)-1], "invalid permission mode version") {
		t.Fatalf("warnings = %#v, want invalid version warning", warnings)
	}
}

func TestSessionPermissionModeProjectionAppliesSnapshotConsistently(t *testing.T) {
	projection := sessionPermissionModeProjectionFromSnapshot(sessionPermissionModeSnapshot{
		Mode:    "acceptEdits",
		Version: 12,
		Known:   true,
	})

	message := SessionStreamMessageEvent{}
	projection.ApplyMessage(&message)
	if message.PermissionMode != "acceptEdits" || message.ModeVersion != 12 {
		t.Fatalf("message projection = %+v, want acceptEdits version 12", message)
	}
	raw := SessionStreamRawMessageEvent{}
	projection.ApplyRawMessage(&raw)
	if raw.PermissionMode != "acceptEdits" || raw.ModeVersion != 12 {
		t.Fatalf("raw projection = %+v, want acceptEdits version 12", raw)
	}
	activity := projection.ActivityPayload("idle")
	if activity.Activity != "idle" || activity.PermissionMode != "acceptEdits" || activity.ModeVersion != 12 {
		t.Fatalf("activity payload = %+v, want idle acceptEdits version 12", activity)
	}
	event := projection.ActivityEvent("idle")
	if event.Activity != "idle" || event.PermissionMode != "acceptEdits" || event.ModeVersion != 12 {
		t.Fatalf("activity event = %+v, want idle acceptEdits version 12", event)
	}
	mode, version := projection.HeaderValues()
	if mode != "acceptEdits" || version != "12" {
		t.Fatalf("headers = (%q, %q), want acceptEdits, 12", mode, version)
	}

	unknown := sessionPermissionModeProjectionFromSnapshot(sessionPermissionModeSnapshot{})
	mode, version = unknown.HeaderValues()
	if mode != "" || version != "" {
		t.Fatalf("unknown headers = (%q, %q), want empty", mode, version)
	}
}

func TestSessionUpdatedPayloadSchemaOmitsOptions(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)

	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /openapi.json status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]any `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
		t.Fatalf("decode openapi: %v", err)
	}
	properties := spec.Components.Schemas["SessionUpdatedPayload"].Properties
	if _, ok := properties["options"]; ok {
		t.Fatalf("SessionUpdatedPayload schema still exposes options: %v", properties)
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
	if payload.PermissionMode != runtime.PermissionModeAcceptEdits {
		t.Fatalf("event permission_mode = %q, want acceptEdits", payload.PermissionMode)
	}
	if payload.ModeVersion != out.ModeVersion {
		t.Fatalf("event mode_version = %d, want %d", payload.ModeVersion, out.ModeVersion)
	}
	var rawPayload map[string]json.RawMessage
	if err := json.Unmarshal(evts[len(evts)-1].Payload, &rawPayload); err != nil {
		t.Fatalf("decode raw session.updated payload: %v", err)
	}
	if _, ok := rawPayload["options"]; ok {
		t.Fatalf("event payload includes options, want live state outside options: %s", evts[len(evts)-1].Payload)
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
	if got := detail.Options["permission_mode"]; got != "normal" {
		t.Fatalf("options.permission_mode = %q, want schema-backed normal", got)
	}
	if detail.Runtime == nil {
		t.Fatal("runtime is nil")
	}
	if got := string(detail.Runtime.PermissionMode); got != "acceptEdits" {
		t.Fatalf("runtime.permission_mode = %q, want acceptEdits", got)
	}
	if detail.Runtime.ModeVersion != 6 {
		t.Fatalf("runtime.mode_version = %d, want 6", detail.Runtime.ModeVersion)
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

func TestSetSessionPermissionModeConcurrentRequestsProduceUniqueMonotonicVersions(t *testing.T) {
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
	const requests = 6
	modes := []string{"acceptEdits", "plan", "bypassPermissions", "default", "acceptEdits", "plan"}
	type result struct {
		Status  int
		Body    string
		Version uint64
	}
	results := make([]result, requests)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(requests)
	for i := 0; i < requests; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-start
			req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/permission-mode", strings.NewReader(`{"permission_mode":"`+modes[i]+`"}`))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			results[i].Status = rec.Code
			results[i].Body = rec.Body.String()
			if rec.Code == http.StatusOK {
				var out struct {
					ModeVersion uint64 `json:"mode_version"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &out); err == nil {
					results[i].Version = out.ModeVersion
				}
			}
		}()
	}
	close(start)
	wg.Wait()

	seen := make(map[uint64]bool, requests)
	for i, result := range results {
		if result.Status != http.StatusOK {
			t.Fatalf("request %d status = %d, want %d; body: %s", i, result.Status, http.StatusOK, result.Body)
		}
		if result.Version < 2 {
			t.Fatalf("request %d version = %d, want >= 2", i, result.Version)
		}
		if seen[result.Version] {
			t.Fatalf("duplicate mode_version %d in results: %+v", result.Version, results)
		}
		seen[result.Version] = true
	}
	for version := uint64(2); version < uint64(2+requests); version++ {
		if !seen[version] {
			t.Fatalf("missing mode_version %d in results: %+v", version, results)
		}
	}
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
	if resp.Runtime == nil {
		t.Fatal("runtime is nil")
	}
	if got := string(resp.Runtime.PermissionMode); got != wantMode {
		t.Fatalf("runtime.permission_mode = %q, want %q", got, wantMode)
	}
	if resp.Runtime.ModeVersion != wantVersion {
		t.Fatalf("runtime.mode_version = %d, want %d", resp.Runtime.ModeVersion, wantVersion)
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
