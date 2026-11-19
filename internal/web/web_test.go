package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"ios-auto-sign/internal/config"
	"ios-auto-sign/internal/store"
)

func TestRequestBaseURL(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://192.168.0.132:9090/api/install/apps", nil)
	if got := requestBaseURL(r); got != "http://192.168.0.132:9090" {
		t.Fatalf("host-derived base = %s", got)
	}
	r.Header.Set("X-Forwarded-Proto", "https, http")
	r.Header.Set("X-Forwarded-Host", "sign.example.com, proxy.local")
	if got := requestBaseURL(r); got != "https://sign.example.com" {
		t.Fatalf("proxied base = %s", got)
	}
}

func newTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return New(config.Default(), st, nil, nil, nil, nil, http.DefaultClient), st
}

func TestInstallAppsAreAccountScopedAndUseRequestHost(t *testing.T) {
	s, st := newTestServer(t)
	if err := st.Update(func(d *store.Data) error {
		d.Artifacts["a1"] = &store.Artifact{ID: "a1", UserID: "u1", DisplayName: "App One", BundleID: "com.one", Version: "1.0"}
		d.Artifacts["a2"] = &store.Artifact{ID: "a2", UserID: "u2", DisplayName: "App Two", BundleID: "com.two", Version: "2.0"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	check := func(u *store.User, want int, wantFirstID string) []map[string]any {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://192.168.0.132:9090/api/install/apps", nil)
		s.installApps(rr, req, u)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		var out []map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if len(out) != want {
			t.Fatalf("user %s role %s: got %d apps, want %d", u.ID, u.Role, len(out), want)
		}
		if want > 0 && wantFirstID != "" {
			if out[0]["id"] != wantFirstID {
				t.Errorf("first artifact = %v, want %s", out[0]["id"], wantFirstID)
			}
			ipa, _ := out[0]["ipa_url"].(string)
			if ipa != "http://192.168.0.132:9090/ipa/"+wantFirstID+".ipa" {
				t.Errorf("ipa_url not derived from request host: %s", ipa)
			}
		}
		return out
	}

	check(&store.User{ID: "u1", Role: "user"}, 1, "a1")
	check(&store.User{ID: "u2", Role: "user"}, 1, "a2")
	check(&store.User{ID: "admin", Role: "admin"}, 2, "")
}

func TestInstallAppsChangelogIsNewestFirstAndFlagsCurrent(t *testing.T) {
	s, st := newTestServer(t)
	if err := st.Update(func(d *store.Data) error {
		d.Users["u1"] = &store.User{ID: "u1", Username: "u1", Role: "user"}
		d.Artifacts["a1"] = &store.Artifact{
			ID: "a1", UserID: "u1", AppID: "app1", VersionID: "v1",
			DisplayName: "App", BundleID: "com.app", Version: "1.0",
		}
		d.Versions["v1"] = &store.AppVersion{ID: "v1", AppID: "app1", Version: "1.0",
			Date: "2026-01-01", DetectedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
		d.Versions["v2"] = &store.AppVersion{ID: "v2", AppID: "app1", Version: "2.0",
			Date: "2026-02-01", LocalizedDescription: "new stuff", DetectedAt: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)}
		d.Versions["other"] = &store.AppVersion{ID: "other", AppID: "app2", Version: "9.0",
			DetectedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://localhost/api/install/apps", nil)
	s.installApps(rr, req, &store.User{ID: "u1", Role: "user"})
	var out []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d apps, want 1", len(out))
	}
	changes, ok := out[0]["changelog"].([]any)
	if !ok || len(changes) != 2 {
		t.Fatalf("changelog = %v, want 2 entries", out[0]["changelog"])
	}
	first := changes[0].(map[string]any)
	second := changes[1].(map[string]any)
	if first["version"] != "2.0" || second["version"] != "1.0" {
		t.Fatalf("changelog not newest first: %v then %v", first["version"], second["version"])
	}
	if first["current"] != false || second["current"] != true {
		t.Fatalf("current flag wrong: v2.current=%v v1.current=%v", first["current"], second["current"])
	}
	if first["description"] != "new stuff" {
		t.Fatalf("description missing: %v", first["description"])
	}
}

// In remote /genPlist mode the install link must carry the whole manifest URL
// as one encoded value, while manifest_url stays the readable target.
func TestInstallURLIsFullyEncodedInPlistServerMode(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Plist.PlistServerURL = "https://api.palera.in/genPlist"
	s := New(cfg, st, nil, nil, nil, nil, http.DefaultClient)
	if err := st.Update(func(d *store.Data) error {
		d.Settings.PlistMode = "plistserver"
		d.Settings.PlistServerURL = "https://api.palera.in/genPlist"
		d.Artifacts["a1"] = &store.Artifact{
			ID: "a1", UserID: "u1", DisplayName: "My App", BundleID: "com.one", Version: "2.0",
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://192.168.0.132:9090/api/install/apps", nil)
	s.installApps(rr, req, &store.User{ID: "u1", Role: "user"})
	var out []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d apps", len(out))
	}
	manifest, _ := out[0]["manifest_url"].(string)
	install, _ := out[0]["install_url"].(string)
	const prefix = "itms-services://?action=download-manifest&url="
	if !strings.HasPrefix(manifest, "https://api.palera.in/genPlist?") {
		t.Fatalf("manifest_url should be the remote endpoint: %s", manifest)
	}
	if !strings.Contains(manifest, "fetchurl=http%3A%2F%2F192.168.0.132%3A9090%2Fipa%2Fa1.ipa") {
		t.Fatalf("fetchurl should use the current request host: %s", manifest)
	}
	if !strings.HasPrefix(install, prefix) {
		t.Fatalf("unexpected install url: %s", install)
	}
	value := strings.TrimPrefix(install, prefix)
	for _, forbidden := range []string{"://", "?", "&", "="} {
		if strings.Contains(value, forbidden) {
			t.Fatalf("url= value contains raw %q: %s", forbidden, value)
		}
	}
	decoded, err := url.QueryUnescape(value)
	if err != nil || decoded != manifest {
		t.Fatalf("decoded = %q, want %q (err %v)", decoded, manifest, err)
	}
}

func TestSettingsAreAdminOnlyForSiteAndPerUserForBark(t *testing.T) {
	s, st := newTestServer(t)
	// Seed the accounts the requests act on.
	if err := st.Update(func(d *store.Data) error {
		d.Users["u1"] = &store.User{ID: "u1", Username: "u1", Role: "user"}
		d.Users["admin"] = &store.User{ID: "admin", Username: "admin", Role: "admin"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// A regular user can save their own Bark target.
	body := `{"bark_enabled":true,"bark_server":"https://api.day.app","bark_key":"secret-key","bark_group":"iOS"}`
	rr := httptest.NewRecorder()
	s.saveSettings(rr, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body)),
		&store.User{ID: "u1", Role: "user"})
	if rr.Code != http.StatusOK {
		t.Fatalf("user bark save status = %d: %s", rr.Code, rr.Body.String())
	}
	var u1 store.User
	st.View(func(d *store.Data) { u1 = *d.Users["u1"] })
	if !u1.Bark.Enabled || u1.Bark.Key != "secret-key" || u1.Bark.Server != "https://api.day.app" {
		t.Fatalf("user bark not persisted: %+v", u1.Bark)
	}

	// A non-admin cannot change the site title, even if they include one.
	rr = httptest.NewRecorder()
	s.saveSettings(rr, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body)),
		&store.User{ID: "u1", Role: "user"})
	if rr.Code != http.StatusOK {
		t.Fatalf("user settings save status = %d", rr.Code)
	}
	st.View(func(d *store.Data) {
		if d.Settings.SiteTitle == "我的签名站" {
			t.Fatal("non-admin changed the site title")
		}
		if d.Users["u1"] == nil || d.Users["u1"].Bark.Key != "secret-key" {
			t.Fatalf("non-admin bark was wiped: %+v", d.Users["u1"])
		}
	})

	// An admin saves the site title plus their own Bark target.
	adminBody := `{"site_title":"我的签名站","bark_enabled":true,"bark_server":"https://api.day.app","bark_key":"admin-key","bark_group":"iOS"}`
	rr = httptest.NewRecorder()
	s.saveSettings(rr, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(adminBody)),
		&store.User{ID: "admin", Role: "admin"})
	if rr.Code != http.StatusOK {
		t.Fatalf("admin save status = %d: %s", rr.Code, rr.Body.String())
	}
	var cfg store.Settings
	var adminUser store.User
	st.View(func(d *store.Data) { cfg = d.Settings; adminUser = *d.Users["admin"] })
	if cfg.SiteTitle != "我的签名站" {
		t.Fatalf("site title = %q", cfg.SiteTitle)
	}
	if !adminUser.Bark.Enabled || adminUser.Bark.Key != "admin-key" {
		t.Fatalf("admin bark not persisted: %+v", adminUser.Bark)
	}

	// A user only sees their own Bark fields, never another account's.
	rr = httptest.NewRecorder()
	var realU1 *store.User
	st.View(func(d *store.Data) { realU1 = d.Users["u1"] })
	if realU1 == nil {
		t.Fatal("u1 missing from store")
	}
	s.getSettings(rr, httptest.NewRequest(http.MethodGet, "/api/settings", nil), realU1)
	var view map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view["bark_key"] != "secret-key" {
		t.Errorf("user should see their own bark key, got %v", view["bark_key"])
	}
	if view["bark_key"] == "admin-key" {
		t.Error("user saw the administrator's bark key")
	}
	if view["site_title"] != "我的签名站" {
		t.Errorf("site title = %v", view["site_title"])
	}

	rr = httptest.NewRecorder()
	s.publicConfig(rr, httptest.NewRequest(http.MethodGet, "http://localhost/api/config/public", nil))
	var pub map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &pub); err != nil {
		t.Fatal(err)
	}
	if pub["site_title"] != "我的签名站" {
		t.Errorf("public site title = %v", pub["site_title"])
	}
}

func TestSetSourceIntervalIsPerUser(t *testing.T) {
	s, st := newTestServer(t)
	if err := st.Update(func(d *store.Data) error {
		d.Users["u1"] = &store.User{ID: "u1", Username: "u1", Role: "user"}
		d.Users["u2"] = &store.User{ID: "u2", Username: "u2", Role: "user"}
		// Owned by an admin; neither u1 nor u2 owns it. Personal intervals must
		// still be settable by both non-owner accounts.
		d.Sources["src1"] = &store.Source{ID: "src1", OwnerID: "admin", Name: "S", URL: "https://x", Enabled: true, IntervalMinutes: 30}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	setInterval := func(uid, body string) int {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/sources/src1/interval", strings.NewReader(body))
		req.SetPathValue("id", "src1")
		s.setSourceInterval(rr, req, &store.User{ID: uid, Role: "user"})
		return rr.Code
	}
	// A non-admin, non-owner can set their own interval (regression: used to 404).
	if code := setInterval("u1", `{"interval_minutes":7}`); code != http.StatusOK {
		t.Fatalf("u1 set interval status = %d (non-owner must be allowed)", code)
	}
	if code := setInterval("u2", `{"interval_minutes":15}`); code != http.StatusOK {
		t.Fatalf("u2 set interval status = %d", code)
	}
	var u1, u2 store.User
	st.View(func(d *store.Data) { u1 = *d.Users["u1"]; u2 = *d.Users["u2"] })
	if u1.SourceIntervals["src1"] != 7 || u2.SourceIntervals["src1"] != 15 {
		t.Fatalf("per-user intervals not stored independently: u1=%+v u2=%+v", u1.SourceIntervals, u2.SourceIntervals)
	}
	if d := getSourceInterval(st, "src1"); d != 30 {
		t.Fatalf("source default interval changed: %d, want 30", d)
	}
	// Reset to default (0) also works for a non-owner.
	if code := setInterval("u1", `{"interval_minutes":0}`); code != http.StatusOK {
		t.Fatalf("reset interval status = %d", code)
	}
	st.View(func(d *store.Data) { u1 = *d.Users["u1"] })
	if _, ok := u1.SourceIntervals["src1"]; ok {
		t.Fatalf("interval override not cleared: %+v", u1.SourceIntervals)
	}
	// Unknown source still returns 404.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/sources/missing/interval", strings.NewReader(`{"interval_minutes":5}`))
	req.SetPathValue("id", "missing")
	s.setSourceInterval(rr, req, &store.User{ID: "u1", Role: "user"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing source status = %d, want 404", rr.Code)
	}
}

func getSourceInterval(st *store.Store, id string) int {
	var v int
	st.View(func(d *store.Data) {
		if s := d.Sources[id]; s != nil {
			v = s.IntervalMinutes
		}
	})
	return v
}

func TestListJobsIsLimitedToFive(t *testing.T) {
	s, st := newTestServer(t)
	if err := st.Update(func(d *store.Data) error {
		for i := 0; i < 8; i++ {
			id := fmt.Sprintf("j%d", i)
			d.Jobs[id] = &store.SignJob{
				ID: id, UserID: "u1", Status: "succeeded",
				CreatedAt: time.Now().Add(time.Duration(i) * time.Minute),
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	s.listJobs(rr, httptest.NewRequest(http.MethodGet, "/api/jobs", nil),
		&store.User{ID: "u1", Role: "user"})
	var out []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 5 {
		t.Fatalf("got %d jobs, want 5", len(out))
	}
	if out[0]["id"] != "j7" {
		t.Errorf("newest job should be first, got %v", out[0]["id"])
	}
}

// The embedded front-end must ship the new brand asset and the latest
// console / install markup. This check needs no network listener.
func TestStaticAssetsAndPageShell(t *testing.T) {
	s, _ := newTestServer(t)
	h := s.Handler()
	get := func(path string) string {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, rr.Code)
		}
		return rr.Body.String()
	}
	check := func(label, body string, wants []string) {
		for _, w := range wants {
			if !strings.Contains(body, w) {
				t.Errorf("%s missing %q", label, w)
			}
		}
	}
	check("favicon.svg", get("/static/favicon.svg"), []string{"<svg", "linearGradient"})
	check("index.html", get("/"), []string{"favicon.svg", "iconDialog", "brand-mark", "github-link", "https://github.com/3zang/ios-auto-sign"})
	install := get("/install")
	check("install.html", install, []string{"favicon.svg", "60vw", ">安装</a>", "github-link", "https://github.com/3zang/ios-auto-sign"})
	if strings.Contains(install, "安装到 iOS") {
		t.Error("install page still uses the old button label")
	}
	check("app.js", get("/static/app.js"), []string{
		"data-app-sort", "data-hero-tab", "data-icon-preview", "toggle-source",
		">下载</a>", ">安装</a>",
	})
	check("style.css", get("/static/style.css"), []string{
		"width: 60vw", ".hero-panel", ".source-actions", ".icon-thumb", ".github-link",
	})
}

// The "All" tab must follow the source catalogue, and every card needs the
// source name for its origin badge. Sources without a snapshot (older stores)
// keep their previous behaviour until the next sync fills it in.
func TestListAppsHidesStaleRowsAndReportsSource(t *testing.T) {
	s, st := newTestServer(t)
	if err := st.Update(func(d *store.Data) error {
		d.Sources["s1"] = &store.Source{ID: "s1", Name: "AltGallery", Bundles: []string{"com.current"}}
		d.Apps["a1"] = &store.App{ID: "a1", SourceID: "s1", BundleID: "com.current", Name: "Current"}
		d.Apps["a2"] = &store.App{ID: "a2", SourceID: "s1", BundleID: "com.old", Name: "Stale"}
		d.Sources["s2"] = &store.Source{ID: "s2", Name: "LegacySource"}
		d.Apps["a3"] = &store.App{ID: "a3", SourceID: "s2", BundleID: "com.legacy", Name: "Legacy"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	s.listApps(rr, httptest.NewRequest(http.MethodGet, "/api/apps", nil), &store.User{ID: "u1", Role: "user"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var out []struct {
		App map[string]any `json:"app"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d rows, want 2 (stale app should be hidden)", len(out))
	}
	for _, row := range out {
		switch row.App["id"] {
		case "a1":
			if row.App["source_name"] != "AltGallery" {
				t.Errorf("source_name = %v, want AltGallery", row.App["source_name"])
			}
		case "a3":
			if row.App["source_name"] != "LegacySource" {
				t.Errorf("source_name = %v, want LegacySource", row.App["source_name"])
			}
		case "a2":
			t.Error("app removed from the catalogue is still listed")
		default:
			t.Errorf("unexpected app %v", row.App["id"])
		}
	}
}

func TestPublicConfigIncludesUptime(t *testing.T) {
	s, _ := newTestServer(t)
	// Move the recorded start back so a non-zero uptime is reported.
	s.started = time.Now().Add(-3*time.Hour - 5*time.Minute)
	rr := httptest.NewRecorder()
	s.publicConfig(rr, httptest.NewRequest(http.MethodGet, "http://localhost/api/config/public", nil))
	var pub map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &pub); err != nil {
		t.Fatal(err)
	}
	up, ok := pub["uptime_seconds"].(float64)
	if !ok {
		t.Fatalf("uptime_seconds missing or not numeric: %v", pub["uptime_seconds"])
	}
	want := (3*time.Hour + 5*time.Minute).Seconds()
	if up < want-5 || up > want+5 {
		t.Errorf("uptime_seconds = %v, want ~%v", up, want)
	}
}

func TestDeleteUser(t *testing.T) {
	s, st := newTestServer(t)
	if err := st.Update(func(d *store.Data) error {
		d.Users["admin"] = &store.User{ID: "admin", Username: "admin", Role: "admin"}
		d.Users["admin2"] = &store.User{ID: "admin2", Username: "admin2", Role: "admin"}
		d.Users["u1"] = &store.User{ID: "u1", Username: "u1", Role: "user"}
		d.Sessions["tok-u1"] = &store.Session{Token: "tok-u1", UserID: "u1"}
		d.Sessions["tok-admin"] = &store.Session{Token: "tok-admin", UserID: "admin"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	admin := &store.User{ID: "admin", Role: "admin"}
	del := func(actor *store.User, id string) int {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodDelete, "/api/users/"+id, nil)
		req.SetPathValue("id", id)
		s.deleteUser(rr, req, actor)
		return rr.Code
	}

	// Non-admin is forbidden.
	if got := del(&store.User{ID: "u1", Role: "user"}, "u1"); got != http.StatusForbidden {
		t.Errorf("non-admin delete = %d, want 403", got)
	}
	// Self-delete is refused.
	if got := del(admin, "admin"); got != http.StatusBadRequest {
		t.Errorf("self delete = %d, want 400", got)
	}
	// Unknown user is 404.
	if got := del(admin, "missing"); got != http.StatusNotFound {
		t.Errorf("missing user delete = %d, want 404", got)
	}
	// A regular user is deleted along with its sessions.
	if got := del(admin, "u1"); got != http.StatusOK {
		t.Fatalf("delete u1 = %d, want 200", got)
	}
	st.View(func(d *store.Data) {
		if d.Users["u1"] != nil {
			t.Error("u1 still present after delete")
		}
		if d.Sessions["tok-u1"] != nil {
			t.Error("u1 session still present after delete")
		}
		if d.Sessions["tok-admin"] == nil {
			t.Error("admin session was removed")
		}
	})

	// The last administrator cannot be deleted.
	if err := st.Update(func(d *store.Data) error {
		delete(d.Users, "admin2")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := del(&store.User{ID: "admin2", Role: "admin"}, "admin"); got != http.StatusConflict {
		t.Errorf("last admin delete = %d, want 409", got)
	}
}

// The effective plist mode must come from the persisted store setting and fall
// back to the startup config only when the store has no override.
func TestEffectivePlistRuntimeSwitch(t *testing.T) {
	cfg := config.Default()
	cfg.Plist.Mode = "plistserver"
	cfg.Plist.PlistServerURL = "https://config.example.com/genPlist"
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := New(cfg, st, nil, nil, nil, nil, http.DefaultClient)

	// No store override: config values win.
	if mode, url := s.effectivePlist(); mode != "plistserver" || url != "https://config.example.com/genPlist" {
		t.Fatalf("fallback = (%s, %s)", mode, url)
	}

	// Store override wins immediately, including switching back to builtin.
	if err := st.Update(func(d *store.Data) error {
		d.Settings.PlistMode = "builtin"
		d.Settings.PlistServerURL = ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if mode, _ := s.effectivePlist(); mode != "builtin" {
		t.Fatalf("runtime mode = %s, want builtin", mode)
	}

	// healthz / publicConfig reflect the switched mode without a restart.
	rr := httptest.NewRecorder()
	s.publicConfig(rr, httptest.NewRequest(http.MethodGet, "http://localhost/api/config/public", nil))
	var pub map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &pub); err != nil {
		t.Fatal(err)
	}
	if pub["plist_mode"] != "builtin" {
		t.Errorf("public plist_mode = %v", pub["plist_mode"])
	}
}

// Only an administrator may change the plist mode, values are validated, and
// the change is persisted to the store.
func TestPlistModeSaveIsAdminOnlyAndValidated(t *testing.T) {
	s, st := newTestServer(t)
	if err := st.Update(func(d *store.Data) error {
		d.Users["u1"] = &store.User{ID: "u1", Username: "u1", Role: "user"}
		d.Users["admin"] = &store.User{ID: "admin", Username: "admin", Role: "admin"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	save := func(body string, u *store.User) int {
		rr := httptest.NewRecorder()
		s.saveSettings(rr, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body)), u)
		return rr.Code
	}
	user := &store.User{ID: "u1", Role: "user"}
	admin := &store.User{ID: "admin", Role: "admin"}

	// A regular user cannot change the plist mode even if they send the field.
	body := `{"plist_mode":"plistserver","plist_server_url":"https://evil.example.com/genPlist"}`
	if got := save(body, user); got != http.StatusOK {
		t.Fatalf("user save status = %d", got)
	}
	if mode, _ := s.effectivePlist(); mode == "plistserver" {
		t.Fatal("non-admin changed the plist mode")
	}

	// Admin: invalid mode is rejected.
	if got := save(`{"plist_mode":"bogus"}`, admin); got != http.StatusBadRequest {
		t.Errorf("invalid mode status = %d, want 400", got)
	}
	// Admin: remote mode without a URL is rejected.
	if got := save(`{"plist_mode":"plistserver","plist_server_url":""}`, admin); got != http.StatusBadRequest {
		t.Errorf("remote without URL status = %d, want 400", got)
	}
	// Admin: a bad URL is rejected.
	if got := save(`{"plist_mode":"plistserver","plist_server_url":"not-a-url"}`, admin); got != http.StatusBadRequest {
		t.Errorf("bad URL status = %d, want 400", got)
	}

	// Admin: valid switch is persisted and takes effect.
	if got := save(`{"site_title":"T","keep_versions":2,"plist_mode":"plistserver","plist_server_url":"https://api.example.com/genPlist"}`, admin); got != http.StatusOK {
		t.Fatalf("admin save status = %d", got)
	}
	mode, url := s.effectivePlist()
	if mode != "plistserver" || url != "https://api.example.com/genPlist" {
		t.Fatalf("after save = (%s, %s)", mode, url)
	}
	st.View(func(d *store.Data) {
		if d.Settings.PlistMode != "plistserver" || d.Settings.PlistServerURL != "https://api.example.com/genPlist" {
			t.Fatalf("not persisted: %+v", d.Settings)
		}
	})

	// settingsView exposes the active mode to the UI.
	rr := httptest.NewRecorder()
	s.getSettings(rr, httptest.NewRequest(http.MethodGet, "/api/settings", nil), admin)
	var view map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view["plist_mode"] != "plistserver" || view["plist_server_url"] != "https://api.example.com/genPlist" {
		t.Errorf("settings view = %v", view)
	}
}

// Switching the runtime mode to builtin must still yield a valid local
// manifest, even though the process started in plistserver mode.
func TestLocalManifestWorksAfterSwitchingToBuiltin(t *testing.T) {
	cfg := config.Default()
	cfg.Plist.Mode = "plistserver"
	cfg.Plist.PlistServerURL = "https://config.example.com/genPlist"
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(d *store.Data) error {
		d.Settings.PlistMode = "builtin"
		d.Artifacts["a1"] = &store.Artifact{
			ID: "a1", BundleID: "com.one", DisplayName: "My App", Version: "2.0",
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s := New(cfg, st, nil, nil, nil, nil, http.DefaultClient)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://localhost/manifest/a1.plist", nil)
	req.SetPathValue("id", "a1.plist")
	s.manifest(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("manifest status = %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "<plist") || !strings.Contains(rr.Body.String(), "com.one") {
		t.Fatalf("manifest body is not a local plist: %s", rr.Body.String())
	}
}
