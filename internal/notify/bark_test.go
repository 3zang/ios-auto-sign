package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ios-auto-sign/internal/store"
)

func TestNotifyAppUpdateSendsPerAppMessage(t *testing.T) {
	var path string
	var got payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(d *store.Data) error {
		d.Settings = store.Settings{
			SiteTitle: "T", BarkEnabled: true, BarkServer: srv.URL,
			BarkKey: "device-key", BarkGroup: "签名", BarkSound: "default",
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s := New(st, srv.Client())
	if err := s.NotifyAppUpdate(context.Background(), "微信", "8.0.50", "braless 软件源"); err != nil {
		t.Fatal(err)
	}
	if path != "/device-key" {
		t.Fatalf("endpoint path = %s, want /device-key", path)
	}
	if got.Title != "微信 有更新" {
		t.Errorf("title = %q, want the app name", got.Title)
	}
	if !strings.Contains(got.Body, "8.0.50") || !strings.Contains(got.Body, "braless 软件源") {
		t.Errorf("body must name the version and source: %q", got.Body)
	}
	if got.Group != "签名" || got.Sound != "default" {
		t.Errorf("group/sound not forwarded: %+v", got)
	}
}

func TestPushDisabledIsSilentButTestAlwaysSends(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(d *store.Data) error {
		d.Settings = store.Settings{BarkServer: srv.URL, BarkKey: "k", BarkEnabled: false}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s := New(st, srv.Client())
	if err := s.Push(context.Background(), "a", "b"); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("disabled notifications sent %d request(s)", calls)
	}
	if err := s.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("test push sent %d request(s), want 1", calls)
	}
}

// iconHarness captures the JSON body Bark receives for a single update push.
func iconHarness(t *testing.T, setup func(*store.Data)) (*Sender, *payload, func()) {
	t.Helper()
	var got payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		w.WriteHeader(http.StatusOK)
	}))
	st, err := store.Open(t.TempDir())
	if err != nil {
		srv.Close()
		t.Fatal(err)
	}
	if err := st.Update(func(d *store.Data) error {
		d.Settings = store.Settings{
			SiteTitle: "T", BarkEnabled: true, BarkServer: srv.URL, BarkKey: "key",
		}
		setup(d)
		return nil
	}); err != nil {
		srv.Close()
		t.Fatal(err)
	}
	return New(st, srv.Client()), &got, srv.Close
}

func TestNotifyUpdateAttachesTheAppIcon(t *testing.T) {
	const original = "https://cdn.example.com/original.png"
	s, got, done := iconHarness(t, func(d *store.Data) {
		d.Apps["app1"] = &store.App{
			ID: "app1", BundleID: "com.demo", Name: "Demo", IconURL: original,
		}
	})
	defer done()

	if err := s.NotifyUpdate(context.Background(), Update{
		AppName: "Demo", BundleID: "com.demo", AppID: "app1", Version: "2.0",
		IconURL: original,
	}); err != nil {
		t.Fatal(err)
	}
	if got.Icon != original {
		t.Errorf("icon = %q, want the app's own icon %q", got.Icon, original)
	}
	if got.Title != "Demo 有更新" {
		t.Errorf("title = %q, want the app name", got.Title)
	}
}

func TestNotifyUpdatePrefersConfiguredIcon(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(*store.Data)
		expect string
	}{
		{
			name: "icon library",
			setup: func(d *store.Data) {
				d.Icons["ic1"] = &store.Icon{ID: "ic1", URL: "https://cdn.example.com/library.png", Enabled: true}
				d.AppConfigs["u1:app1"] = &store.AppConfig{
					UserID: "u1", AppID: "app1", Enabled: true,
					IconSource: "library", IconLibraryID: "ic1",
				}
			},
			expect: "https://cdn.example.com/library.png",
		},
		{
			name: "custom URL",
			setup: func(d *store.Data) {
				d.AppConfigs["u1:app1"] = &store.AppConfig{
					UserID: "u1", AppID: "app1", Enabled: true,
					IconSource: "custom", IconCustomURL: "https://cdn.example.com/custom.png",
				}
			},
			expect: "https://cdn.example.com/custom.png",
		},
		{
			name: "disabled config falls back to source",
			setup: func(d *store.Data) {
				d.AppConfigs["u1:app1"] = &store.AppConfig{
					UserID: "u1", AppID: "app1", Enabled: false,
					IconSource: "custom", IconCustomURL: "https://cdn.example.com/ignored.png",
				}
			},
			expect: "https://cdn.example.com/original.png",
		},
		{
			name: "relative custom URL falls back to source",
			setup: func(d *store.Data) {
				d.AppConfigs["u1:app1"] = &store.AppConfig{
					UserID: "u1", AppID: "app1", Enabled: true,
					IconSource: "custom", IconCustomURL: "/assets/local.png",
				}
			},
			expect: "https://cdn.example.com/original.png",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, got, done := iconHarness(t, func(d *store.Data) {
				d.Apps["app1"] = &store.App{
					ID: "app1", BundleID: "com.demo", Name: "Demo",
					IconURL: "https://cdn.example.com/original.png",
				}
				tc.setup(d)
			})
			defer done()
			if err := s.NotifyUpdate(context.Background(), Update{
				AppName: "Demo", BundleID: "com.demo", AppID: "app1", Version: "2.0",
			}); err != nil {
				t.Fatal(err)
			}
			if got.Icon != tc.expect {
				t.Errorf("icon = %q, want %q", got.Icon, tc.expect)
			}
		})
	}
}

func TestNotifyUpdatePrefersAdminConfiguredIcon(t *testing.T) {
	s, got, done := iconHarness(t, func(d *store.Data) {
		d.Users["admin1"] = &store.User{ID: "admin1", Username: "admin", Role: "admin"}
		d.Users["user1"] = &store.User{ID: "user1", Username: "member", Role: "user"}
		d.Apps["app1"] = &store.App{ID: "app1", BundleID: "com.demo", IconURL: "https://cdn.example.com/original.png"}
		d.AppConfigs["user1:app1"] = &store.AppConfig{
			UserID: "user1", AppID: "app1", Enabled: true,
			IconSource: "custom", IconCustomURL: "https://cdn.example.com/member.png",
		}
		d.AppConfigs["admin1:app1"] = &store.AppConfig{
			UserID: "admin1", AppID: "app1", Enabled: true,
			IconSource: "custom", IconCustomURL: "https://cdn.example.com/admin.png",
		}
	})
	defer done()

	if err := s.NotifyUpdate(context.Background(), Update{
		AppName: "Demo", AppID: "app1", Version: "2.0",
	}); err != nil {
		t.Fatal(err)
	}
	if got.Icon != "https://cdn.example.com/admin.png" {
		t.Errorf("icon = %q, want the administrator's configured icon", got.Icon)
	}
}

func TestNotifyUpdateOmitsIconWhenNoAbsoluteURL(t *testing.T) {
	s, got, done := iconHarness(t, func(d *store.Data) {
		d.Apps["app1"] = &store.App{ID: "app1", BundleID: "com.demo", IconURL: "data:image/png;base64,AAAA"}
	})
	defer done()

	if err := s.NotifyUpdate(context.Background(), Update{
		AppName: "Demo", AppID: "app1", Version: "2.0",
	}); err != nil {
		t.Fatal(err)
	}
	if got.Icon != "" {
		t.Errorf("icon = %q, want an omitted field", got.Icon)
	}
}
