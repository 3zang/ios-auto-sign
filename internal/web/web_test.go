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
	"ios-auto-sign/internal/plist"
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
	gen, err := plist.New("builtin", "", http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	return New(config.Default(), st, nil, nil, nil, gen, nil, http.DefaultClient), st
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

// In remote /genPlist mode the install link must carry the whole manifest URL
// as one encoded value, while manifest_url stays the readable target.
func TestInstallURLIsFullyEncodedInPlistServerMode(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gen, err := plist.New("plistserver", "https://api.palera.in/genPlist", http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Plist.PlistServerURL = "https://api.palera.in/genPlist"
	s := New(cfg, st, nil, nil, nil, gen, nil, http.DefaultClient)
	if err := st.Update(func(d *store.Data) error {
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

func TestSettingsAreAdminOnlyAndExposeTitlePublicly(t *testing.T) {
	s, st := newTestServer(t)
	body := `{"site_title":"我的签名站","bark_enabled":true,"bark_server":"https://api.day.app","bark_key":"secret-key","bark_group":"iOS"}`

	rr := httptest.NewRecorder()
	s.saveSettings(rr, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body)),
		&store.User{ID: "u1", Role: "user"})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("non-admin save status = %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	s.saveSettings(rr, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body)),
		&store.User{ID: "admin", Role: "admin"})
	if rr.Code != http.StatusOK {
		t.Fatalf("admin save status = %d: %s", rr.Code, rr.Body.String())
	}
	var cfg store.Settings
	st.View(func(d *store.Data) { cfg = d.Settings })
	if cfg.SiteTitle != "我的签名站" || !cfg.BarkEnabled || cfg.BarkKey != "secret-key" {
		t.Fatalf("settings not persisted: %+v", cfg)
	}

	rr = httptest.NewRecorder()
	s.getSettings(rr, httptest.NewRequest(http.MethodGet, "/api/settings", nil),
		&store.User{ID: "u1", Role: "user"})
	var view map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if _, leaked := view["bark_key"]; leaked {
		t.Fatal("bark device key leaked to a regular user")
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
	check("index.html", get("/"), []string{"favicon.svg", "iconDialog", "brand-mark"})
	install := get("/install")
	check("install.html", install, []string{"favicon.svg", "60vw", ">安装</a>"})
	if strings.Contains(install, "安装到 iOS") {
		t.Error("install page still uses the old button label")
	}
	check("app.js", get("/static/app.js"), []string{
		"data-app-sort", "data-hero-tab", "data-icon-preview", "toggle-source",
		">下载</a>", ">安装</a>",
	})
	check("style.css", get("/static/style.css"), []string{
		"width: 60vw", ".hero-panel", ".source-actions", ".icon-thumb",
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
