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
