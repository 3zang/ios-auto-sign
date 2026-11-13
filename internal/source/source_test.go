package source

import (
	"context"
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
		fmt.Fprintf(w, `{"apps":[{"name":"Demo","bundleIdentifier":"com.demo","versions":[`+
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
	if _, err := svc.Sync(ctx, id); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("unchanged source notified again: %d update(s)", len(got))
	}
}
