// Package notify sends Bark push notifications when a subscription source
// reports a new app version. Settings are read from the shared store on every
// send, so changes made in the settings centre take effect without a restart.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"ios-auto-sign/internal/store"
)

type Sender struct {
	st     *store.Store
	client *http.Client
}

func New(st *store.Store, client *http.Client) *Sender {
	if client == nil {
		client = http.DefaultClient
	}
	return &Sender{st: st, client: client}
}

func (s *Sender) settings() store.Settings {
	var out store.Settings
	s.st.View(func(d *store.Data) { out = d.Settings })
	if strings.TrimSpace(out.SiteTitle) == "" {
		out.SiteTitle = store.DefaultSiteTitle
	}
	return out
}

type payload struct {
	Title     string `json:"title"`
	Body      string `json:"body"`
	Group     string `json:"group,omitempty"`
	Sound     string `json:"sound,omitempty"`
	Icon      string `json:"icon,omitempty"`
	DeviceKey string `json:"device_key,omitempty"`
}

// Update describes one newly discovered app version. IconURL carries the
// subscription's original icon; the sender resolves the effective icon from
// the store so a user-configured icon wins when one exists.
type Update struct {
	AppName    string
	BundleID   string
	AppID      string
	Version    string
	SourceName string
	IconURL    string
}

// Push is a no-op when Bark is disabled or not configured. Callers can invoke
// it unconditionally; a delivery failure never aborts source synchronisation.
func (s *Sender) Push(ctx context.Context, title, body string) error {
	return s.PushWithIcon(ctx, title, body, "")
}

// PushWithIcon behaves like Push but attaches a custom notification icon.
// Bark renders it on iOS 15 and above; older clients ignore the field.
func (s *Sender) PushWithIcon(ctx context.Context, title, body, iconURL string) error {
	cfg := s.settings()
	if !cfg.BarkEnabled {
		return nil
	}
	return s.pushWith(ctx, cfg, title, body, iconURL)
}

// Test always sends, even while notifications are disabled, so an
// administrator can verify the server/key before turning them on.
func (s *Sender) Test(ctx context.Context) error {
	cfg := s.settings()
	return s.pushWith(ctx, cfg, "Bark 测试通知", "配置成功，之后检测到应用更新会推送到这里。", "")
}

// NotifyAppUpdate is kept for callers that have no icon metadata.
func (s *Sender) NotifyAppUpdate(ctx context.Context, appName, version, sourceName string) error {
	return s.NotifyUpdate(ctx, Update{
		AppName: appName, Version: version, SourceName: sourceName,
	})
}

// NotifyUpdate pushes one message per app, using that app's own icon as the
// Bark notification icon whenever a usable absolute URL is available.
func (s *Sender) NotifyUpdate(ctx context.Context, up Update) error {
	name := strings.TrimSpace(up.AppName)
	if name == "" {
		name = "应用"
	}
	ver := strings.TrimSpace(up.Version)
	if ver == "" {
		ver = "新版本"
	}
	body := "发现新版本 " + ver + "，已加入自动签名队列。"
	if strings.TrimSpace(up.SourceName) != "" {
		body += "（来源：" + strings.TrimSpace(up.SourceName) + "）"
	}
	return s.PushWithIcon(ctx, name+" 有更新", body, s.iconFor(up))
}

// iconFor resolves the icon representing this app: a user-configured icon
// (icon library / custom URL) takes precedence, then the subscription's
// original icon. Bark's iOS client downloads the image itself, so relative or
// otherwise unreachable URLs are dropped instead of being sent as broken
// parameters.
func (s *Sender) iconFor(up Update) string {
	var fallback, configured, adminConfigured string
	s.st.View(func(d *store.Data) {
		appID := up.AppID
		if appID == "" && up.BundleID != "" {
			for _, a := range d.Apps {
				if a != nil && a.BundleID == up.BundleID {
					appID = a.ID
					break
				}
			}
		}
		if a := d.Apps[appID]; a != nil && a.IconURL != "" {
			fallback = a.IconURL
		}
		if appID == "" {
			return
		}
		var keys []string
		for k, c := range d.AppConfigs {
			if c != nil && c.Enabled && c.AppID == appID {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			c := d.AppConfigs[k]
			var picked string
			switch c.IconSource {
			case "library":
				if ic := d.Icons[c.IconLibraryID]; ic != nil && ic.Enabled {
					picked = ic.URL
				}
			case "custom":
				picked = c.IconCustomURL
			}
			if picked == "" {
				continue
			}
			if configured == "" {
				configured = picked
			}
			if u := d.Users[c.UserID]; u != nil && u.Role == "admin" {
				adminConfigured = picked
				break
			}
		}
	})
	if fallback == "" {
		fallback = up.IconURL
	}
	for _, raw := range []string{adminConfigured, configured, fallback} {
		if icon := absoluteIconURL(raw); icon != "" {
			return icon
		}
	}
	return ""
}

// absoluteIconURL accepts only absolute http(s) URLs, which Bark clients can
// fetch. Invalid values are omitted from the payload via omitempty.
func absoluteIconURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return u.String()
}

func (s *Sender) pushWith(ctx context.Context, cfg store.Settings, title, body, iconURL string) error {
	server, err := normalizeServer(cfg.BarkServer)
	if err != nil {
		return err
	}
	key := strings.Trim(strings.TrimSpace(cfg.BarkKey), "/")
	p := payload{
		Title: title, Body: body,
		Group: strings.TrimSpace(cfg.BarkGroup),
		Sound: strings.TrimSpace(cfg.BarkSound),
		Icon:  absoluteIconURL(iconURL),
	}
	endpoint := server
	if key != "" {
		endpoint = server + "/" + url.PathEscape(key)
	}
	firstErr := s.post(ctx, endpoint, p)
	if firstErr == nil {
		return nil
	}
	// Bark 2.x also accepts POST /push with device_key in the body. Some
	// self-hosted instances only expose that route, so retry once.
	p.DeviceKey = key
	if key != "" {
		if err := s.post(ctx, server+"/push", p); err == nil {
			return nil
		} else {
			return fmt.Errorf("bark: %v; /push fallback: %w", firstErr, err)
		}
	}
	return firstErr
}

func (s *Sender) post(ctx context.Context, endpoint string, p payload) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(c, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, endpoint)
	}
	return nil
}

func normalizeServer(raw string) (string, error) {
	server := strings.TrimRight(strings.TrimSpace(raw), "/")
	if server == "" {
		return "", fmt.Errorf("请先填写 Bark 服务器地址")
	}
	u, err := url.Parse(server)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("Bark 服务器地址必须是完整的 http(s) URL")
	}
	return server, nil
}
