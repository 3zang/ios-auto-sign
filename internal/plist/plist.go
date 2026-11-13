// Package plist builds iOS OTA install manifests. The builtin mode renders a
// plist locally; plistserver mode points iOS directly at a remote /genPlist
// endpoint, so the local server neither proxies nor generates the manifest.
// The mode is selected in config.json.
package plist

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type Params struct {
	BundleID         string
	Name             string
	Version          string
	IPAURL           string
	IconURL          string
	FullSizeImageURL string
}

type Generator interface {
	Mode() string
	Generate(ctx context.Context, p Params) (string, error)
}

func New(mode, serverURL string, client *http.Client) (Generator, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "builtin", "":
		return builtin{}, nil
	case "plistserver":
		if serverURL == "" {
			return nil, fmt.Errorf("plistserver_url is required for plistserver mode")
		}
		return &remote{base: strings.TrimRight(serverURL, "/"), client: client}, nil
	default:
		return nil, fmt.Errorf("unknown plist mode %q", mode)
	}
}

func esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func asset(kind, u string, shine bool) string {
	var sb strings.Builder
	sb.WriteString("        <dict>\n")
	sb.WriteString("          <key>kind</key>\n")
	sb.WriteString("          <string>" + esc(kind) + "</string>\n")
	if shine {
		sb.WriteString("          <key>needs-shine</key>\n          <false/>\n")
	}
	sb.WriteString("          <key>url</key>\n")
	sb.WriteString("          <string>" + esc(u) + "</string>\n")
	sb.WriteString("        </dict>\n")
	return sb.String()
}

type builtin struct{}

func (builtin) Mode() string { return "builtin" }

func (builtin) Generate(_ context.Context, p Params) (string, error) {
	if p.BundleID == "" || p.IPAURL == "" || p.Name == "" {
		return "", fmt.Errorf("bundle id, name and IPA url are required")
	}
	var sb strings.Builder
	sb.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	sb.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
	sb.WriteString("<plist version=\"1.0\">\n<dict>\n  <key>items</key>\n  <array>\n    <dict>\n")
	sb.WriteString("      <key>assets</key>\n      <array>\n")
	sb.WriteString(asset("software-package", p.IPAURL, false))
	if p.IconURL != "" {
		sb.WriteString(asset("display-image", p.IconURL, true))
	}
	if p.FullSizeImageURL != "" {
		sb.WriteString(asset("full-size-image", p.FullSizeImageURL, true))
	}
	sb.WriteString("      </array>\n")
	sb.WriteString("      <key>metadata</key>\n      <dict>\n")
	sb.WriteString("        <key>bundle-identifier</key>\n        <string>" + esc(p.BundleID) + "</string>\n")
	sb.WriteString("        <key>bundle-version</key>\n        <string>" + esc(p.Version) + "</string>\n")
	sb.WriteString("        <key>kind</key>\n        <string>software</string>\n")
	sb.WriteString("        <key>title</key>\n        <string>" + esc(p.Name) + "</string>\n")
	sb.WriteString("      </dict>\n    </dict>\n  </array>\n</dict>\n</plist>\n")
	return sb.String(), nil
}

// remote adapts plistserver's /genPlist endpoint. plistserver emits only a
// software-package asset, so the adapter injects icon assets into the returned
// XML. This post-processing does not modify plistserver's GPLv3 source.
type remote struct {
	base   string
	client *http.Client
}

func (r *remote) Mode() string { return "plistserver" }

func (r *remote) Generate(ctx context.Context, p Params) (string, error) {
	// Reuse the single URL builder so a configured full /genPlist endpoint
	// is never turned into /genPlist/genPlist (HTTP 404).
	endpoint, err := DirectManifestURL(r.base, p)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("call plistserver: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("plistserver returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	out := string(body)
	if !strings.Contains(out, "<plist") {
		return "", fmt.Errorf("plistserver returned a non-plist response")
	}
	var extra strings.Builder
	if p.IconURL != "" {
		extra.WriteString(asset("display-image", p.IconURL, true))
	}
	if p.FullSizeImageURL != "" {
		extra.WriteString(asset("full-size-image", p.FullSizeImageURL, true))
	}
	if extra.Len() > 0 {
		if idx := strings.Index(out, "</array>"); idx >= 0 {
			out = out[:idx] + extra.String() + out[idx:]
		}
	}
	return out, nil
}

// encodeWholeValue escapes an entire value so it can be embedded in a query
// string without any of its characters being interpreted as separators.
// url.QueryEscape encodes spaces as '+', which some /genPlist servers decode
// literally instead of as a space, so '+' is normalised to '%20' to match
// encodeURIComponent. ':', '/', '?', '&', '#' and '=' are all percent-encoded.
func encodeWholeValue(raw string) string {
	return strings.ReplaceAll(url.QueryEscape(raw), "+", "%20")
}

func encodeFetchURL(raw string) string { return encodeWholeValue(raw) }

// DirectManifestURL builds the /genPlist URL that iOS is pointed at directly.
// Values are escaped individually, while the separators stay literal, matching
// the link format emitted by plistserver-based install pages.
func DirectManifestURL(serverURL string, p Params) (string, error) {
	if p.BundleID == "" || p.IPAURL == "" || p.Name == "" {
		return "", fmt.Errorf("bundle id, name and IPA url are required")
	}
	base := strings.TrimRight(strings.TrimSpace(serverURL), "/")
	if base == "" {
		return "", fmt.Errorf("plistserver_url is required")
	}
	if !strings.HasSuffix(base, "/genPlist") {
		base += "/genPlist"
	}
	return base + "?bundleid=" + encodeWholeValue(p.BundleID) +
		"&fetchurl=" + encodeFetchURL(p.IPAURL) +
		"&name=" + encodeWholeValue(p.Name) +
		"&version=" + encodeWholeValue(p.Version), nil
}

// ItmsServicesURL wraps a manifest URL in the OTA install scheme. When
// encodeManifest is true everything after "url=" is percent-encoded as one
// opaque value, including the manifest's own query separators. The remote
// /genPlist mode always uses this form so the nested query cannot be split.
func ItmsServicesURL(manifest string, encodeManifest bool) string {
	if encodeManifest {
		manifest = encodeWholeValue(manifest)
	}
	return "itms-services://?action=download-manifest&url=" + manifest
}
