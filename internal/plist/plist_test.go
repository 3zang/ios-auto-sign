package plist

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestBuiltinGenerator(t *testing.T) {
	g, err := New("builtin", "", http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	out, err := g.Generate(context.Background(), Params{
		BundleID: "com.example.app", Name: "A & B", Version: "1.2.3",
		IPAURL: "https://example.com/a.ipa", IconURL: "https://example.com/i.png",
		FullSizeImageURL: "https://example.com/i.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<plist version=\"1.0\"", "software-package", "display-image", "full-size-image",
		"com.example.app", "A &amp; B", "1.2.3", "https://example.com/a.ipa",
		"needs-shine",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("builtin plist missing %q", want)
		}
	}
}

func TestBuiltinRequiresFields(t *testing.T) {
	g, _ := New("builtin", "", http.DefaultClient)
	if _, err := g.Generate(context.Background(), Params{}); err == nil {
		t.Fatal("expected validation error for empty params")
	}
}

const minimalPlistServerXML = `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict><key>items</key><array><dict>
<key>assets</key><array><dict><key>kind</key><string>software-package</string>
<key>url</key><string>https://example.com/a.ipa</string></dict></array>
<key>metadata</key><dict><key>bundle-identifier</key><string>com.example.app</string>
<key>bundle-version</key><string>1.2.3</string><key>kind</key><string>software</string>
<key>title</key><string>Demo</string></dict></dict></array></dict></plist>`

func TestPlistServerAdapter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/genPlist" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("bundleid") != "com.example.app" || q.Get("name") != "Demo" ||
			q.Get("version") != "1.2.3" || q.Get("fetchurl") != "https://example.com/a.ipa" {
			t.Errorf("unexpected query: %v", q)
		}
		_, _ = io.WriteString(w, minimalPlistServerXML)
	}))
	defer srv.Close()

	g, err := New("plistserver", srv.URL, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if g.Mode() != "plistserver" {
		t.Fatalf("mode = %s", g.Mode())
	}
	out, err := g.Generate(context.Background(), Params{
		BundleID: "com.example.app", Name: "Demo", Version: "1.2.3",
		IPAURL: "https://example.com/a.ipa", IconURL: "https://example.com/i.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "display-image") {
		t.Fatal("adapter did not inject icon asset")
	}
	iconIdx := strings.Index(out, "display-image")
	firstArrayEnd := strings.Index(out, "</array>")
	if iconIdx > firstArrayEnd {
		t.Fatal("icon asset injected outside the assets array")
	}
}

func TestUnknownMode(t *testing.T) {
	if _, err := New("other", "", http.DefaultClient); err == nil {
		t.Fatal("expected error for unknown mode")
	}
}

// A configured full /genPlist endpoint must not be turned into
// /genPlist/genPlist (which is what caused the 404 in signing jobs).
func TestPlistServerAdapterFullEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/genPlist" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, minimalPlistServerXML)
	}))
	defer srv.Close()

	g, err := New("plistserver", srv.URL+"/genPlist", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Generate(context.Background(), Params{
		BundleID: "com.example.app", Name: "Demo", Version: "1.2.3",
		IPAURL: "https://example.com/a.ipa",
	}); err != nil {
		t.Fatalf("full endpoint should not be double-appended: %v", err)
	}
}

func TestDirectManifestURLFormat(t *testing.T) {
	p := Params{
		BundleID: "com.atebits.Tweetie2", Name: "X", Version: "11.0",
		IPAURL: "http://192.168.0.132:9000/ipa/d1f0c6f47e57db22.ipa",
	}
	want := "https://api.palera.in/genPlist?bundleid=com.atebits.Tweetie2" +
		"&fetchurl=http%3A%2F%2F192.168.0.132%3A9000%2Fipa%2Fd1f0c6f47e57db22.ipa" +
		"&name=X&version=11.0"
	got, err := DirectManifestURL("https://api.palera.in", p)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	// A configured full endpoint must not get a duplicated /genPlist.
	got2, err := DirectManifestURL("https://api.palera.in/genPlist", p)
	if err != nil || got2 != want {
		t.Fatalf("full endpoint: got %s, err %v", got2, err)
	}
	if _, err := DirectManifestURL("https://api.palera.in", Params{}); err == nil {
		t.Fatal("expected validation error for empty params")
	}
	// Value escaping: spaces and non-ASCII characters are encoded per value.
	escaped, err := DirectManifestURL("https://api.palera.in", Params{
		BundleID: "com.x.app", Name: "我的 App", Version: "1.0",
		IPAURL: "https://x.example/a.ipa",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(escaped, "&name=%E6%88%91%E7%9A%84%20App&") {
		t.Fatalf("name not escaped with %%20: %s", escaped)
	}
	install := ItmsServicesURL(want, false)
	if install != "itms-services://?action=download-manifest&url="+want {
		t.Fatalf("unexpected install url %s", install)
	}
	encoded := ItmsServicesURL("https://x.example/manifest/a.plist", true)
	if encoded != "itms-services://?action=download-manifest&url=https%3A%2F%2Fx.example%2Fmanifest%2Fa.plist" {
		t.Fatalf("unexpected encoded url %s", encoded)
	}
}

// The remote /genPlist install link must encode everything after url= as a
// single opaque value: the nested ? & = characters belong to the manifest URL,
// not to the itms-services query string.
func TestItmsServicesURLEncodesEntireManifest(t *testing.T) {
	p := Params{
		BundleID: "com.atebits.Tweetie2", Name: "我的 App", Version: "11.0",
		IPAURL: "http://192.168.0.132:9000/ipa/a b.ipa",
	}
	remote, err := DirectManifestURL("https://api.palera.in/genPlist", p)
	if err != nil {
		t.Fatal(err)
	}
	install := ItmsServicesURL(remote, true)
	const prefix = "itms-services://?action=download-manifest&url="
	if !strings.HasPrefix(install, prefix) {
		t.Fatalf("unexpected install scheme: %s", install)
	}
	value := strings.TrimPrefix(install, prefix)
	for _, forbidden := range []string{"://", "?", "&", "=", "#", "+", " "} {
		if strings.Contains(value, forbidden) {
			t.Fatalf("url= value still contains raw %q: %s", forbidden, value)
		}
	}
	decoded, err := url.QueryUnescape(value)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != remote {
		t.Fatalf("decoded  %s\nwant    %s", decoded, remote)
	}
	// The inner fetchurl value already uses %20; after the second encoding it
	// becomes %2520, so no '+' or literal %20 should remain in the outer value.
	if !strings.Contains(remote, "%20") {
		t.Fatalf("inner fetchurl should use %%20: %s", remote)
	}
	if strings.Contains(value, "+") {
		t.Fatalf("encoded value must not contain '+': %s", value)
	}
	// The unescaped form is still available for the builtin manifest mode.
	if got := ItmsServicesURL(remote, false); got != prefix+remote {
		t.Fatalf("unexpected unescaped install url: %s", got)
	}
}

// The value after fetchurl= must be a single fully URL-encoded query value:
// no raw scheme, no separators, and it must decode back to the original IPA
// address. This is the part iOS hands to /genPlist.
func TestFetchURLIsURLEncoded(t *testing.T) {
	cases := []string{
		"http://192.168.0.132:9000/ipa/d1f0c6f47e57db22.ipa",
		"https://cdn.example.com/apps/My App/v2.ipa?token=a%2Fb&x=1#frag",
	}
	for _, ipa := range cases {
		got, err := DirectManifestURL("https://api.palera.in/genPlist", Params{
			BundleID: "com.example.app", Name: "Demo", Version: "2.0", IPAURL: ipa,
		})
		if err != nil {
			t.Fatal(err)
		}
		raw := got[strings.Index(got, "&fetchurl=")+len("&fetchurl=") : strings.Index(got, "&name=")]
		for _, forbidden := range []string{"://", "?", "&", "#", " "} {
			if strings.Contains(raw, forbidden) {
				t.Fatalf("fetchurl not fully encoded (%q found in %q)", forbidden, raw)
			}
		}
		parsed, err := url.Parse(got)
		if err != nil {
			t.Fatal(err)
		}
		if decoded := parsed.Query().Get("fetchurl"); decoded != ipa {
			t.Fatalf("fetchurl decoded to %q, want %q", decoded, ipa)
		}
	}
	// Spaces must use %20, never '+', so /genPlist sees the exact URL.
	got, err := DirectManifestURL("https://api.palera.in", Params{
		BundleID: "com.x", Name: "X", Version: "1", IPAURL: "https://x.example/a b.ipa",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "&fetchurl=https%3A%2F%2Fx.example%2Fa%20b.ipa&") {
		t.Fatalf("space in fetchurl should be %%20: %s", got)
	}
}
