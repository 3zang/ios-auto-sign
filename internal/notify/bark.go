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
	DeviceKey string `json:"device_key,omitempty"`
}

// Push is a no-op when Bark is disabled or not configured. Callers can invoke
// it unconditionally; a delivery failure never aborts source synchronisation.
func (s *Sender) Push(ctx context.Context, title, body string) error {
	cfg := s.settings()
	if !cfg.BarkEnabled {
		return nil
	}
	return s.pushWith(ctx, cfg, title, body)
}

// Test always sends, even while notifications are disabled, so an
// administrator can verify the server/key before turning them on.
func (s *Sender) Test(ctx context.Context) error {
	cfg := s.settings()
	return s.pushWith(ctx, cfg, "Bark 测试通知", "配置成功，之后检测到应用更新会推送到这里。")
}

// NotifyAppUpdate pushes one message per app, naming the software and its
// newly detected version.
func (s *Sender) NotifyAppUpdate(ctx context.Context, appName, version, sourceName string) error {
	name := strings.TrimSpace(appName)
	if name == "" {
		name = "应用"
	}
	ver := strings.TrimSpace(version)
	if ver == "" {
		ver = "新版本"
	}
	body := "发现新版本 " + ver + "，已加入自动签名队列。"
	if strings.TrimSpace(sourceName) != "" {
		body += "（来源：" + strings.TrimSpace(sourceName) + "）"
	}
	return s.Push(ctx, name+" 有更新", body)
}

func (s *Sender) pushWith(ctx context.Context, cfg store.Settings, title, body string) error {
	server, err := normalizeServer(cfg.BarkServer)
	if err != nil {
		return err
	}
	key := strings.Trim(strings.TrimSpace(cfg.BarkKey), "/")
	p := payload{
		Title: title, Body: body,
		Group: strings.TrimSpace(cfg.BarkGroup),
		Sound: strings.TrimSpace(cfg.BarkSound),
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
