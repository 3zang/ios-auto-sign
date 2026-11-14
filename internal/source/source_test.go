package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ios-auto-sign/internal/store"
)

// The first import of a source must not flood Bark with the whole catalogue,
// while later genuinely new versions must produce one hook call each.
func TestSyncHookReportsOnlyNewVersions(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		extra := ""
		if calls >= 2 {
			extra = `,{"version":"2.0","date":"2026-02-02","downloadURL":"https://x/2.ipa"}`
		}
		fmt.Fprintf(w, `{"apps":[{"name":"Demo","bundleIdentifier":"com.demo",`+
			`"iconURL":"https://cdn.example.com/demo.png","versions":[`+
			`{"version":"1.0","date":"2026-01-01","downloadURL":"https://x/1.ipa"}%s]}]}`, extra)
	}))
	defer srv.Close()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const id = "src1"
	if err := st.Update(func(d *store.Data) error {
		d.Sources[id] = &store.Source{
			ID: id, Name: "测试源", URL: srv.URL, Enabled: true,
			IntervalMinutes: 5, CreatedAt: time.Now(),
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	svc := New(st, srv.Client())
	var got []AppUpdate
	svc.SetUpdateHook(func(_ context.Context, u AppUpdate) { got = append(got, u) })
	ctx := context.Background()

	if _, err := svc.Sync(ctx, id); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("first import notified %d update(s), want 0", len(got))
	}
	if _, err := svc.Sync(ctx, id); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("new version notified %d update(s), want 1", len(got))
	}
	if got[0].AppName != "Demo" || got[0].Version != "2.0" || got[0].SourceName != "测试源" {
		t.Errorf("unexpected update payload: %+v", got[0])
	}
	if got[0].IconURL != "https://cdn.example.com/demo.png" || got[0].AppID == "" {
		t.Errorf("update must carry the app icon and id for notifications: %+v", got[0])
	}
	if _, err := svc.Sync(ctx, id); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("unchanged source notified again: %d update(s)", len(got))
	}
}

func newSyncStore(t *testing.T, url string) (*store.Store, string) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const id = "src1"
	if err := st.Update(func(d *store.Data) error {
		d.Sources[id] = &store.Source{
			ID: id, Name: "测试源", URL: url, Enabled: true,
			IntervalMinutes: 5, CreatedAt: time.Now(),
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return st, id
}

// A publisher changing the bundle identifier must not leave the old row behind
// in the "All" list, and must not orphan the user's signing configuration.
func TestSyncMigratesRenamedBundleAndKeepsConfig(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		bundle := "com.old"
		if calls > 1 {
			bundle = "com.new"
		}
		fmt.Fprintf(w, `{"apps":[{"name":"Demo","bundleIdentifier":%q,`+
			`"versions":[{"version":"1.0","date":"2026-01-01","downloadURL":"https://x/1.ipa"}]}]}`, bundle)
	}))
	defer srv.Close()

	st, id := newSyncStore(t, srv.URL)
	svc := New(st, srv.Client())
	if _, err := svc.Sync(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	var appID string
	st.View(func(d *store.Data) {
		for aid := range d.Apps {
			appID = aid
		}
	})
	if appID == "" {
		t.Fatal("first sync created no app")
	}
	if err := st.Update(func(d *store.Data) error {
		d.AppConfigs[store.ConfigKey("u1", appID)] = &store.AppConfig{
			ID: "c1", UserID: "u1", AppID: appID, Enabled: true,
		}
		d.Artifacts["art1"] = &store.Artifact{ID: "art1", UserID: "u1", AppID: appID}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	res, err := svc.Sync(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if res.RenamedApps != 1 || res.RemovedApps != 0 {
		t.Errorf("renamed=%d removed=%d, want 1/0", res.RenamedApps, res.RemovedApps)
	}
	st.View(func(d *store.Data) {
		if len(d.Apps) != 1 {
			t.Errorf("got %d apps after rename, want 1", len(d.Apps))
		}
		a := d.Apps[appID]
		if a == nil {
			t.Fatal("app ID changed, config and artifacts would be orphaned")
		}
		if a.BundleID != "com.new" {
			t.Errorf("bundle = %q, want com.new", a.BundleID)
		}
		if d.AppConfigs[store.ConfigKey("u1", appID)] == nil {
			t.Error("per-app config was dropped during rename")
		}
		if d.Artifacts["art1"] == nil {
			t.Error("published artifact was dropped during rename")
		}
	})
}

// Apps that disappear from the catalogue are removed with their versions and
// configs, but already published IPAs must stay installable.
func TestSyncPrunesRemovedAppsButKeepsArtifacts(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		extra := ""
		if calls == 1 {
			extra = `,{"name":"Gone","bundleIdentifier":"com.gone",` +
				`"versions":[{"version":"1.0","date":"2026-01-01","downloadURL":"https://x/gone.ipa"}]}`
		}
		fmt.Fprintf(w, `{"apps":[{"name":"Kept","bundleIdentifier":"com.kept",`+
			`"versions":[{"version":"1.0","date":"2026-01-01","downloadURL":"https://x/kept.ipa"}]}%s]}`, extra)
	}))
	defer srv.Close()

	st, id := newSyncStore(t, srv.URL)
	svc := New(st, srv.Client())
	if _, err := svc.Sync(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	var goneID string
	st.View(func(d *store.Data) {
		for aid, a := range d.Apps {
			if a.BundleID == "com.gone" {
				goneID = aid
			}
		}
	})
	if goneID == "" {
		t.Fatal("second app was not imported")
	}
	if err := st.Update(func(d *store.Data) error {
		d.AppConfigs[store.ConfigKey("u1", goneID)] = &store.AppConfig{
			ID: "c1", UserID: "u1", AppID: goneID, Enabled: true,
		}
		d.Artifacts["art1"] = &store.Artifact{ID: "art1", UserID: "u1", AppID: goneID}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	res, err := svc.Sync(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if res.RemovedApps != 1 {
		t.Errorf("removed=%d, want 1", res.RemovedApps)
	}
	st.View(func(d *store.Data) {
		if len(d.Apps) != 1 {
			t.Errorf("got %d apps, want 1", len(d.Apps))
		}
		for _, a := range d.Apps {
			if a.BundleID != "com.kept" {
				t.Errorf("unexpected app left: %s", a.BundleID)
			}
		}
		for _, v := range d.Versions {
			if v.AppID == goneID {
				t.Error("version of removed app still stored")
			}
		}
		if d.AppConfigs[store.ConfigKey("u1", goneID)] != nil {
			t.Error("config of removed app still stored")
		}
		if d.Artifacts["art1"] == nil {
			t.Error("published artifact must survive app removal")
		}
	})
}

// Upgrading from an older build leaves stale apps in the store even when the
// upstream body is byte-for-byte unchanged. That sync must still reconcile.
func TestSyncUnchangedBodyStillPrunesLegacyApps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"apps":[{"name":"Kept","bundleIdentifier":"com.kept",`+
			`"versions":[{"version":"1.0","date":"2026-01-01","downloadURL":"https://x/kept.ipa"}]}]}`)
	}))
	defer srv.Close()

	st, id := newSyncStore(t, srv.URL)
	svc := New(st, srv.Client())
	if _, err := svc.Sync(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(d *store.Data) error {
		d.Apps["legacy"] = &store.App{ID: "legacy", SourceID: id, BundleID: "com.legacy", Name: "Old"}
		d.Sources[id].Bundles = nil // simulate a store written by an older build
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	res, err := svc.Sync(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed {
		t.Error("unchanged body must not report a change")
	}
	if res.RemovedApps != 1 {
		t.Errorf("removed=%d, want 1", res.RemovedApps)
	}
	st.View(func(d *store.Data) {
		if d.Apps["legacy"] != nil {
			t.Error("legacy app still stored after unchanged-body sync")
		}
		got := d.Sources[id].Bundles
		if len(got) != 1 || got[0] != "com.kept" {
			t.Errorf("catalogue snapshot = %v, want [com.kept]", got)
		}
	})
}

func TestSyncEmptyCatalogueDoesNotWipeApps(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			fmt.Fprint(w, `{"apps":[{"name":"Kept","bundleIdentifier":"com.kept",`+
				`"versions":[{"version":"1.0","date":"2026-01-01","downloadURL":"https://x/kept.ipa"}]}]}`)
			return
		}
		fmt.Fprint(w, `{"apps":[]}`)
	}))
	defer srv.Close()

	st, id := newSyncStore(t, srv.URL)
	svc := New(st, srv.Client())
	if _, err := svc.Sync(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Sync(context.Background(), id); err == nil {
		t.Fatal("empty catalogue should fail so the next poll can retry")
	}
	st.View(func(d *store.Data) {
		if len(d.Apps) != 1 {
			t.Errorf("got %d apps, want 1 (empty feed must not wipe the store)", len(d.Apps))
		}
		if d.Sources[id].LastError == "" {
			t.Error("empty catalogue should be reported as a source error")
		}
	})
}

// A 304 carries no body, so cleanup has to use the persisted catalogue.
func TestSync304PrunesUsingStoredCatalogue(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("If-None-Match") != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		fmt.Fprint(w, `{"apps":[{"name":"Kept","bundleIdentifier":"com.kept",`+
			`"versions":[{"version":"1.0","date":"2026-01-01","downloadURL":"https://x/kept.ipa"}]}]}`)
	}))
	defer srv.Close()

	st, id := newSyncStore(t, srv.URL)
	svc := New(st, srv.Client())
	if _, err := svc.Sync(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(d *store.Data) error {
		d.Apps["legacy"] = &store.App{ID: "legacy", SourceID: id, BundleID: "com.legacy", Name: "Old"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	res, err := svc.Sync(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !res.SkippedByTag || res.RemovedApps != 1 {
		t.Errorf("skipped=%v removed=%d, want true/1", res.SkippedByTag, res.RemovedApps)
	}
	st.View(func(d *store.Data) {
		if d.Apps["legacy"] != nil {
			t.Error("legacy app survived a 304 sync")
		}
	})
}

// A partial upstream response must not destroy stored configs. The rows are
// only hidden via Bundles, and once the smaller catalogue proves stable the
// following sync performs the real cleanup.
func TestSyncDefersBulkPruneOnPartialCatalogue(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		count := 4
		if calls > 1 {
			count = 1
		}
		apps := make([]map[string]any, 0, count)
		for i := 0; i < count; i++ {
			apps = append(apps, map[string]any{
				"name":             fmt.Sprintf("App%d", i),
				"bundleIdentifier": fmt.Sprintf("com.app%d", i),
				"versions":         []any{},
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"apps": apps})
	}))
	defer srv.Close()

	st, id := newSyncStore(t, srv.URL)
	svc := New(st, srv.Client())
	if _, err := svc.Sync(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	res2, err := svc.Sync(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if res2.RemovedApps != 0 {
		t.Errorf("partial catalogue removed %d apps, want 0", res2.RemovedApps)
	}
	st.View(func(d *store.Data) {
		if len(d.Apps) != 4 {
			t.Errorf("got %d apps after partial response, want 4", len(d.Apps))
		}
		if got := d.Sources[id].Bundles; len(got) != 1 || got[0] != "com.app0" {
			t.Errorf("Bundles = %v, want [com.app0]", got)
		}
	})

	res3, err := svc.Sync(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if res3.RemovedApps != 3 {
		t.Errorf("stable smaller catalogue removed %d apps, want 3", res3.RemovedApps)
	}
	st.View(func(d *store.Data) {
		if len(d.Apps) != 1 {
			t.Errorf("got %d apps, want 1", len(d.Apps))
		}
	})
}
