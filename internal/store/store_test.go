package store

import (
	"testing"
	"time"
)

func TestPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(d *Data) error {
		d.Users["u1"] = &User{ID: "u1", Username: "alice", Role: "admin", CreatedAt: time.Now()}
		d.Sources["s1"] = &Source{ID: "s1", Name: "source", URL: "https://example.com/all-apps.json", Enabled: true}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	reopened.View(func(d *Data) {
		if d.Users["u1"] == nil || d.Users["u1"].Username != "alice" {
			t.Fatal("user not persisted")
		}
		if d.Sources["s1"] == nil || !d.Sources["s1"].Enabled {
			t.Fatal("source not persisted")
		}
	})
}

func TestUpdateRollsBackOnError(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(d *Data) error {
		d.Users["keep"] = &User{ID: "keep", Username: "keep"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(d *Data) error {
		d.Users["temp"] = &User{ID: "temp"}
		delete(d.Users, "keep")
		return errSentinel{}
	}); err == nil {
		t.Fatal("expected error")
	}
	st.View(func(d *Data) {
		if d.Users["temp"] != nil {
			t.Error("failed update left dirty state in memory")
		}
		if d.Users["keep"] == nil {
			t.Error("failed update discarded committed data")
		}
	})
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	reopened.View(func(d *Data) {
		if d.Users["temp"] != nil || d.Users["keep"] == nil {
			t.Error("failed update was persisted")
		}
	})
}

type errSentinel struct{}

func (errSentinel) Error() string { return "sentinel" }

func TestConfigKey(t *testing.T) {
	if ConfigKey("u1", "a1") != "u1:a1" {
		t.Fatal("unexpected config key")
	}
}
