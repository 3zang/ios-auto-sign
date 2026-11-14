// Package web exposes the management API, the public install page, and the
// OTA manifest / IPA / icon endpoints. Everything is served from one binary.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"ios-auto-sign/internal/auth"
	"ios-auto-sign/internal/config"
	"ios-auto-sign/internal/notify"
	"ios-auto-sign/internal/plist"
	"ios-auto-sign/internal/secure"
	"ios-auto-sign/internal/signing"
	"ios-auto-sign/internal/source"
	"ios-auto-sign/internal/store"
)

//go:embed static/*
var staticFS embed.FS

// staticAssetRE matches a /static/... path inside an HTML attribute so the
// server can append the build version as a cache-busting query parameter.
var staticAssetRE = regexp.MustCompile(`(/static/[A-Za-z0-9._/-]+)`)

// Version is injected at build time with
//   -ldflags "-X ios-auto-sign/internal/web.Version=v1.0.5"
// It is shown in the top bar and used as a cache-busting key for static
// assets, so a newly deployed binary never serves a stale cached app.js.
var Version = "dev"

// versionKey returns a HTML/URL-safe form of Version for use in attribute
// values and query strings.
func versionKey() string {
	var b strings.Builder
	for _, r := range Version {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	if b.Len() == 0 {
		return "dev"
	}
	return b.String()
}

type Server struct {
	cfg      config.Config
	st       *store.Store
	auth     *auth.Manager
	sources  *source.Service
	signing  *signing.Manager
	gen      plist.Generator
	key      *[32]byte
	client   *http.Client
	notifier *notify.Sender
}

func New(cfg config.Config, st *store.Store, a *auth.Manager, src *source.Service,
	sign *signing.Manager, gen plist.Generator, key *[32]byte, client *http.Client) *Server {
	return &Server{cfg: cfg, st: st, auth: a, sources: src, signing: sign,
		gen: gen, key: key, client: client, notifier: notify.New(st, client)}
}

// requestBaseURL derives the externally visible base from the current
// request, so LAN IPs, localhost, domains and reverse proxies all work
// without a configured address.
func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := firstHeaderValue(r.Header.Get("X-Forwarded-Proto")); p == "http" || p == "https" {
		scheme = p
	}
	host := firstHeaderValue(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if host == "" {
		host = "localhost"
	}
	return scheme + "://" + strings.TrimRight(host, "/")
}

func firstHeaderValue(v string) string {
	if i := strings.IndexByte(v, ','); i >= 0 {
		v = v[:i]
	}
	return strings.ToLower(strings.TrimSpace(v))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

func publicUser(u *store.User) map[string]any {
	return map[string]any{"id": u.ID, "username": u.Username, "role": u.Role}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	fileServer := http.StripPrefix("/static/", http.FileServer(http.FS(sub)))
	mux.Handle("GET /static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Assets requested with an explicit version key are immutable: a new
		// release changes the key, so the browser fetches fresh files instead
		// of reusing a stale cached app.js / style.css.
		if r.URL.Query().Get("v") != "" {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(w, r)
	}))
	mux.HandleFunc("GET /", s.servePage("index.html"))
	mux.HandleFunc("GET /install", s.servePage("install.html"))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "plist_mode": s.gen.Mode()})
	})

	mux.HandleFunc("POST /api/auth/register", s.register)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	mux.HandleFunc("GET /api/me", s.auth.Require(s.me))
	mux.HandleFunc("GET /api/config/public", s.publicConfig)

	mux.HandleFunc("GET /api/sources", s.auth.Require(s.listSources))
	mux.HandleFunc("POST /api/sources", s.auth.Require(s.createSource))
	mux.HandleFunc("PUT /api/sources/{id}", s.auth.Require(s.updateSource))
	mux.HandleFunc("DELETE /api/sources/{id}", s.auth.Require(s.deleteSource))
	mux.HandleFunc("POST /api/sources/{id}/refresh", s.auth.Require(s.refreshSource))

	mux.HandleFunc("GET /api/apps", s.auth.Require(s.listApps))
	mux.HandleFunc("PUT /api/apps/{id}/config", s.auth.Require(s.saveAppConfig))
	mux.HandleFunc("DELETE /api/apps/{id}/config", s.auth.Require(s.deleteAppConfig))
	mux.HandleFunc("POST /api/apps/{id}/sign", s.auth.Require(s.signApp))

	mux.HandleFunc("GET /api/icons", s.auth.Require(s.listIcons))
	mux.HandleFunc("POST /api/icons", s.auth.Require(s.createIcon))
	mux.HandleFunc("DELETE /api/icons/{id}", s.auth.Require(s.deleteIcon))

	mux.HandleFunc("GET /api/certificates", s.auth.Require(s.listCertificates))
	mux.HandleFunc("POST /api/certificates", s.auth.Require(s.createCertificate))
	mux.HandleFunc("DELETE /api/certificates/{id}", s.auth.Require(s.deleteCertificate))

	mux.HandleFunc("GET /api/jobs", s.auth.Require(s.listJobs))
	mux.HandleFunc("GET /api/artifacts", s.auth.Require(s.listArtifacts))
	mux.HandleFunc("DELETE /api/artifacts/{id}", s.auth.Require(s.deleteArtifact))

	mux.HandleFunc("GET /api/settings", s.auth.Require(s.getSettings))
	mux.HandleFunc("PUT /api/settings", s.auth.Require(s.saveSettings))
	mux.HandleFunc("POST /api/settings/bark/test", s.auth.Require(s.testBark))
	mux.HandleFunc("POST /api/sync/run", s.auth.Require(s.runNow))

	// Installation data is account-scoped. The legacy public path is kept as
	// an alias but now uses the same authenticated, filtered handler.
	mux.HandleFunc("GET /api/install/apps", s.auth.Require(s.installApps))
	mux.HandleFunc("GET /api/public/apps", s.auth.Require(s.installApps))
	mux.HandleFunc("GET /manifest/{id}", s.manifest)
	mux.HandleFunc("GET /ipa/{id}", s.ipa)
	mux.HandleFunc("GET /assets/{name}", s.asset)
	return mux
}

func (s *Server) servePage(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			http.Error(w, "page not found", http.StatusNotFound)
			return
		}
		// Append the build version to every static asset URL so a newly
		// deployed binary can never be paired with a stale cached app.js.
		// __VERSION__ is also used for the visible version badge.
		v := versionKey()
		page := staticAssetRE.ReplaceAllString(string(b), "${1}?v="+v)
		page = strings.ReplaceAll(page, "__VERSION__", v)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = io.WriteString(w, page)
	}
}

func (s *Server) siteTitle() string {
	var t string
	s.st.View(func(d *store.Data) { t = d.Settings.SiteTitle })
	if strings.TrimSpace(t) == "" {
		return store.DefaultSiteTitle
	}
	return t
}

// keepVersions returns the effective retention count for the public config:
// the administrator setting wins over the config-file default.
func (s *Server) keepVersions() int {
	var n int
	s.st.View(func(d *store.Data) { n = d.Settings.KeepVersions })
	if n < 1 {
		n = s.cfg.KeepVersions
	}
	if n < 1 {
		n = 1
	}
	return n
}

func (s *Server) publicConfig(w http.ResponseWriter, r *http.Request) {
	base := requestBaseURL(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"site_title":      s.siteTitle(),
		"version":         versionKey(),
		"public_base_url": base,
		"plist_mode":      s.gen.Mode(),
		"keep_versions":   s.keepVersions(),
		"default_source":  s.cfg.DefaultSourceURL,
		"poll_minutes":    s.cfg.PollIntervalMinutes,
		"zsign_path":      s.cfg.ZsignPath,
		"https_required":  strings.HasPrefix(base, "https://"),
		"plist_server":    s.cfg.Plist.PlistServerURL,
		"ipa_base_url":    base,
	})
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var p struct{ Username, Password string }
	if err := decodeJSON(r, &p); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	u, err := s.auth.Register(p.Username, p.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	token, err := s.auth.Login(p.Username, p.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.auth.SetCookie(w, token)
	writeJSON(w, http.StatusCreated, publicUser(u))
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var p struct{ Username, Password string }
	if err := decodeJSON(r, &p); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	token, err := s.auth.Login(p.Username, p.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	u, _ := s.st.UserByUsername(strings.ToLower(strings.TrimSpace(p.Username)))
	s.auth.SetCookie(w, token)
	writeJSON(w, http.StatusOK, publicUser(u))
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("ias_session"); err == nil {
		_ = s.auth.Logout(c.Value)
	}
	s.auth.ClearCookie(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, _ *http.Request, u *store.User) {
	writeJSON(w, http.StatusOK, publicUser(u))
}

// ---- subscription sources ----

func sourceView(v *store.Source) map[string]any {
	return map[string]any{
		"id": v.ID, "owner_id": v.OwnerID, "name": v.Name, "url": v.URL,
		"enabled": v.Enabled, "interval_minutes": v.IntervalMinutes,
		"etag": v.ETag, "last_checked_at": v.LastCheckedAt,
		"last_changed_at": v.LastChangedAt, "last_error": v.LastError,
	}
}

func (s *Server) listSources(w http.ResponseWriter, _ *http.Request, _ *store.User) {
	out := []map[string]any{}
	s.st.View(func(d *store.Data) {
		for _, v := range d.Sources {
			out = append(out, sourceView(v))
		}
	})
	writeJSON(w, http.StatusOK, out)
}

type sourcePayload struct {
	Name            string `json:"name"`
	URL             string `json:"url"`
	Enabled         bool   `json:"enabled"`
	IntervalMinutes int    `json:"interval_minutes"`
}

func (s *Server) createSource(w http.ResponseWriter, r *http.Request, u *store.User) {
	var p sourcePayload
	if err := decodeJSON(r, &p); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(p.Name) == "" {
		p.Name = "软件源"
	}
	if err := source.ValidateURL(p.URL); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if p.IntervalMinutes < 1 {
		p.IntervalMinutes = s.cfg.PollIntervalMinutes
	}
	id := store.NewID()
	src := &store.Source{
		ID: id, OwnerID: u.ID, Name: p.Name, URL: strings.TrimSpace(p.URL),
		Enabled: p.Enabled, IntervalMinutes: p.IntervalMinutes, CreatedAt: time.Now(),
	}
	if err := s.st.Update(func(d *store.Data) error {
		d.Sources[id] = src
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	go s.refreshAsync(id)
	writeJSON(w, http.StatusCreated, sourceView(src))
}

func (s *Server) canEditSource(id string, u *store.User) (*store.Source, bool) {
	src, ok := s.st.Source(id)
	if !ok {
		return nil, false
	}
	return src, u.Role == "admin" || src.OwnerID == u.ID
}

func (s *Server) updateSource(w http.ResponseWriter, r *http.Request, u *store.User) {
	id := r.PathValue("id")
	src, ok := s.canEditSource(id, u)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("source not found or not permitted"))
		return
	}
	var p sourcePayload
	if err := decodeJSON(r, &p); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := source.ValidateURL(p.URL); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if p.IntervalMinutes < 1 {
		p.IntervalMinutes = src.IntervalMinutes
	}
	if err := s.st.Update(func(d *store.Data) error {
		v := d.Sources[id]
		v.Name, v.URL, v.Enabled = p.Name, strings.TrimSpace(p.URL), p.Enabled
		v.IntervalMinutes = p.IntervalMinutes
		if v.URL != src.URL {
			v.ETag, v.LastHash = "", ""
		}
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	updated, _ := s.st.Source(id)
	writeJSON(w, http.StatusOK, sourceView(updated))
}

func (s *Server) deleteSource(w http.ResponseWriter, r *http.Request, u *store.User) {
	id := r.PathValue("id")
	if _, ok := s.canEditSource(id, u); !ok {
		writeError(w, http.StatusNotFound, errors.New("source not found or not permitted"))
		return
	}
	if err := s.st.Update(func(d *store.Data) error {
		delete(d.Sources, id)
		appIDs := map[string]bool{}
		for aid, app := range d.Apps {
			if app.SourceID == id {
				appIDs[aid] = true
				delete(d.Apps, aid)
			}
		}
		for vid, v := range d.Versions {
			if appIDs[v.AppID] {
				delete(d.Versions, vid)
			}
		}
		for key, c := range d.AppConfigs {
			if appIDs[c.AppID] {
				delete(d.AppConfigs, key)
			}
		}
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) refreshAsync(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := s.sources.Sync(ctx, id); err == nil {
		_, _ = s.signing.EnqueuePending()
	}
}

func (s *Server) refreshSource(w http.ResponseWriter, r *http.Request, u *store.User) {
	id := r.PathValue("id")
	if _, ok := s.canEditSource(id, u); !ok {
		writeError(w, http.StatusNotFound, errors.New("source not found or not permitted"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	res, err := s.sources.Sync(ctx, id)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	created, _ := s.signing.EnqueuePending()
	writeJSON(w, http.StatusOK, map[string]any{"sync": res, "jobs_created": created})
}

// ---- apps and per-app configuration ----

func (s *Server) listApps(w http.ResponseWriter, _ *http.Request, u *store.User) {
	type item struct {
		App      map[string]any   `json:"app"`
		Versions []map[string]any `json:"versions"`
		Config   any              `json:"config"`
	}
	out := []item{}
	s.st.View(func(d *store.Data) {
		for _, a := range d.Apps {
			versions := []map[string]any{}
			for _, v := range d.Versions {
				if v.AppID == a.ID {
					versions = append(versions, map[string]any{
						"id": v.ID, "version": v.Version, "build_version": v.BuildVersion,
						"date": v.Date, "download_url": v.DownloadURL, "size": v.Size,
						"min_os_version": v.MinOSVersion, "detected_at": v.DetectedAt,
						"localized_description": v.LocalizedDescription,
					})
				}
			}
			var cfg any
			if c := d.AppConfigs[store.ConfigKey(u.ID, a.ID)]; c != nil {
				cfg = c
			}
			out = append(out, item{
				App: map[string]any{
					"id": a.ID, "source_id": a.SourceID, "bundle_id": a.BundleID,
					"name": a.Name, "developer_name": a.DeveloperName,
					"subtitle": a.Subtitle, "icon_url": a.IconURL, "updated_at": a.UpdatedAt,
				},
				Versions: versions, Config: cfg,
			})
		}
	})
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) saveAppConfig(w http.ResponseWriter, r *http.Request, u *store.User) {
	appID := r.PathValue("id")
	var p struct {
		Enabled          bool                     `json:"enabled"`
		DisplayName      string                   `json:"display_name"`
		IconSource       string                   `json:"icon_source"`
		IconLibraryID    string                   `json:"icon_library_id"`
		IconCustomURL    string                   `json:"icon_custom_url"`
		BundleIDOverride string                   `json:"bundle_id_override"`
		CertificateID    string                   `json:"certificate_id"`
		SignOptions      store.SignOptionOverride `json:"sign_options"`
	}
	if err := decodeJSON(r, &p); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	switch p.IconSource {
	case "library", "custom", "original":
	default:
		p.IconSource = "original"
	}
	key := store.ConfigKey(u.ID, appID)
	var out *store.AppConfig
	if err := s.st.Update(func(d *store.Data) error {
		if d.Apps[appID] == nil {
			return errors.New("app not found")
		}
		if p.CertificateID != "" {
			c := d.Certificates[p.CertificateID]
			if c == nil || c.UserID != u.ID {
				return errors.New("certificate not found")
			}
		}
		if p.IconLibraryID != "" {
			ic := d.Icons[p.IconLibraryID]
			if ic == nil || !ic.Enabled {
				return errors.New("icon not found or disabled")
			}
		}
		cfg := d.AppConfigs[key]
		if cfg == nil {
			cfg = &store.AppConfig{ID: store.NewID(), UserID: u.ID, AppID: appID}
			d.AppConfigs[key] = cfg
		}
		cfg.Enabled, cfg.DisplayName = p.Enabled, p.DisplayName
		cfg.IconSource, cfg.IconLibraryID, cfg.IconCustomURL = p.IconSource, p.IconLibraryID, p.IconCustomURL
		cfg.BundleIDOverride, cfg.CertificateID = p.BundleIDOverride, p.CertificateID
		cfg.SignOptions, cfg.UpdatedAt = p.SignOptions, time.Now()
		cp := *cfg
		out = &cp
		return nil
	}); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// deleteAppConfig removes the caller's configuration for an app. Apps and
// versions are subscription metadata shared by all users, so they are kept:
// the card simply moves back to the unconfigured state and can be set up
// again. Published artifacts are intentionally preserved.
func (s *Server) deleteAppConfig(w http.ResponseWriter, r *http.Request, u *store.User) {
	appID := r.PathValue("id")
	key := store.ConfigKey(u.ID, appID)
	if err := s.st.Update(func(d *store.Data) error {
		if d.AppConfigs[key] == nil {
			return errors.New("configuration not found")
		}
		delete(d.AppConfigs, key)
		return nil
	}); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) signApp(w http.ResponseWriter, r *http.Request, u *store.User) {
	appID := r.PathValue("id")
	id, err := s.signing.Enqueue(u.ID, appID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": id})
}

// ---- icon library (admin maintains, users choose) ----

func (s *Server) listIcons(w http.ResponseWriter, _ *http.Request, _ *store.User) {
	out := []*store.Icon{}
	s.st.View(func(d *store.Data) {
		for _, v := range d.Icons {
			out = append(out, v)
		}
	})
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createIcon(w http.ResponseWriter, r *http.Request, u *store.User) {
	if u.Role != "admin" {
		writeError(w, http.StatusForbidden, errors.New("administrator only"))
		return
	}
	var p struct {
		Label, URL string
		Enabled    bool
	}
	if err := decodeJSON(r, &p); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := source.ValidateURL(p.URL); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ic := &store.Icon{ID: store.NewID(), Label: p.Label, URL: p.URL,
		CreatedBy: u.ID, Enabled: p.Enabled, CreatedAt: time.Now()}
	if err := s.st.Update(func(d *store.Data) error {
		d.Icons[ic.ID] = ic
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, ic)
}

func (s *Server) deleteIcon(w http.ResponseWriter, r *http.Request, u *store.User) {
	if u.Role != "admin" {
		writeError(w, http.StatusForbidden, errors.New("administrator only"))
		return
	}
	id := r.PathValue("id")
	if err := s.st.Update(func(d *store.Data) error {
		delete(d.Icons, id)
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- certificates (p12 + password + mobileprovision) ----

func (s *Server) listCertificates(w http.ResponseWriter, _ *http.Request, u *store.User) {
	type view struct {
		ID        string    `json:"id"`
		Name      string    `json:"name"`
		HasProv   bool      `json:"has_provision"`
		CreatedAt time.Time `json:"created_at"`
	}
	out := []view{}
	s.st.View(func(d *store.Data) {
		for _, c := range d.Certificates {
			if u.Role == "admin" || c.UserID == u.ID {
				out = append(out, view{c.ID, c.Name, c.ProvisionEnc != "", c.CreatedAt})
			}
		}
	})
	writeJSON(w, http.StatusOK, out)
}

func readUpload(r *http.Request, field string) ([]byte, error) {
	f, _, err := r.FormFile(field)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, 8<<20))
}

func (s *Server) createCertificate(w http.ResponseWriter, r *http.Request, u *store.User) {
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	p12, err := readUpload(r, "p12")
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("p12 file: %w", err))
		return
	}
	prov, err := readUpload(r, "provision")
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("provision file: %w", err))
		return
	}
	password := r.FormValue("password")
	name := r.FormValue("name")
	if name == "" {
		name = "我的证书"
	}
	if len(p12) == 0 || len(prov) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("p12 and provision files must not be empty"))
		return
	}
	p12Enc, err := secure.Encrypt(p12, s.key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	passEnc, err := secure.Encrypt([]byte(password), s.key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	provEnc, err := secure.Encrypt(prov, s.key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	cert := &store.Certificate{ID: store.NewID(), UserID: u.ID, Name: name,
		P12Enc: p12Enc, PasswordEnc: passEnc, ProvisionEnc: provEnc, CreatedAt: time.Now()}
	if err := s.st.Update(func(d *store.Data) error {
		d.Certificates[cert.ID] = cert
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": cert.ID, "name": cert.Name})
}

func (s *Server) deleteCertificate(w http.ResponseWriter, r *http.Request, u *store.User) {
	id := r.PathValue("id")
	cert, ok := s.st.Certificate(id)
	if !ok || (u.Role != "admin" && cert.UserID != u.ID) {
		writeError(w, http.StatusNotFound, errors.New("certificate not found"))
		return
	}
	if err := s.st.Update(func(d *store.Data) error {
		delete(d.Certificates, id)
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- jobs / artifacts ----

const visibleJobRecords = 5

func (s *Server) listJobs(w http.ResponseWriter, _ *http.Request, u *store.User) {
	out := []*store.SignJob{}
	s.st.View(func(d *store.Data) {
		for _, j := range d.Jobs {
			if u.Role == "admin" || j.UserID == u.ID {
				out = append(out, j)
			}
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > visibleJobRecords {
		out = out[:visibleJobRecords]
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listArtifacts(w http.ResponseWriter, _ *http.Request, u *store.User) {
	out := []*store.Artifact{}
	s.st.View(func(d *store.Data) {
		for _, a := range d.Artifacts {
			if u.Role == "admin" || a.UserID == u.ID {
				out = append(out, a)
			}
		}
	})
	writeJSON(w, http.StatusOK, out)
}

// deleteArtifact removes one published artifact and its install data. Only the
// artifact owner or an administrator may delete it; the signing manager also
// removes the IPA file, icon file and cached manifest from disk.
func (s *Server) deleteArtifact(w http.ResponseWriter, r *http.Request, u *store.User) {
	id := r.PathValue("id")
	a, ok := s.st.Artifact(id)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("artifact not found"))
		return
	}
	if u.Role != "admin" && a.UserID != u.ID {
		writeError(w, http.StatusForbidden, errors.New("not permitted"))
		return
	}
	if err := s.signing.DeleteArtifact(id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- public install surface ----

// installApps lists artifacts visible to the signed-in user. All URLs are
// derived from the current request, so the address is never stale.
func (s *Server) installApps(w http.ResponseWriter, r *http.Request, u *store.User) {
	base := requestBaseURL(r)
	type view struct {
		ID          string    `json:"id"`
		Name        string    `json:"name"`
		BundleID    string    `json:"bundle_id"`
		Version     string    `json:"version"`
		IconURL     string    `json:"icon_url"`
		UpdatedAt   time.Time `json:"updated_at"`
		Size        int64     `json:"size"`
		ManifestURL string    `json:"manifest_url"`
		InstallURL  string    `json:"install_url"`
		IPAURL      string    `json:"ipa_url"`
	}
	out := []view{}
	s.st.View(func(d *store.Data) {
		for _, a := range d.Artifacts {
			if u.Role != "admin" && a.UserID != u.ID {
				continue
			}
			name := a.DisplayName
			if name == "" {
				name = a.BundleID
			}
			icon := a.IconURL
			if idx := strings.Index(icon, "/assets/"); idx >= 0 {
				icon = base + icon[idx:]
			}
			ipaURL := base + "/ipa/" + a.ID + ".ipa"
			p := plist.Params{
				BundleID: a.BundleID, Name: name, Version: a.Version,
				IPAURL:           ipaURL,
				IconURL:          icon,
				FullSizeImageURL: icon,
			}
			manifest := base + "/manifest/" + a.ID + ".plist"
			installURL := plist.ItmsServicesURL(manifest, true)
			if s.gen.Mode() == "plistserver" {
				remote, err := plist.DirectManifestURL(s.cfg.Plist.PlistServerURL, p)
				if err != nil {
					log.Printf("install apps: remote manifest for %s: %v", a.ID, err)
				} else {
					// Everything after url= is encoded as one value so the remote
				// /genPlist query string is never parsed as part of the scheme.
				manifest, installURL = remote, plist.ItmsServicesURL(remote, true)
				}
			}
			out = append(out, view{
				ID: a.ID, Name: name, BundleID: a.BundleID, Version: a.Version,
				IconURL: icon, UpdatedAt: a.CreatedAt, Size: a.Size,
				ManifestURL: manifest,
				InstallURL:  installURL,
				IPAURL:      ipaURL,
			})
		}
	})
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) manifest(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(r.PathValue("id"), ".plist")
	a, ok := s.st.Artifact(id)
	if !ok {
		http.Error(w, "manifest not found", http.StatusNotFound)
		return
	}
	base := requestBaseURL(r)
	icon := a.IconURL
	if idx := strings.Index(icon, "/assets/"); idx >= 0 {
		icon = base + icon[idx:]
	}
	p := plist.Params{
		BundleID: a.BundleID, Name: a.DisplayName, Version: a.Version,
		IPAURL:           base + "/ipa/" + a.ID + ".ipa",
		IconURL:          icon,
		FullSizeImageURL: icon,
	}
	if p.Name == "" {
		p.Name = a.BundleID
	}
	if s.gen.Mode() == "plistserver" {
		target, err := plist.DirectManifestURL(s.cfg.Plist.PlistServerURL, p)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	body, err := s.gen.Generate(r.Context(), p)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = io.WriteString(w, body)
}

func (s *Server) ipa(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(r.PathValue("id"), ".ipa")
	a, ok := s.st.Artifact(id)
	if !ok {
		http.Error(w, "ipa not found", http.StatusNotFound)
		return
	}
	path := filepath.Join(s.cfg.DataDir, a.IPAPath)
	if _, err := os.Stat(path); err != nil {
		http.Error(w, "ipa file missing", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, path)
}

func (s *Server) asset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `\/`) {
		http.Error(w, "invalid asset name", http.StatusBadRequest)
		return
	}
	path := filepath.Join(s.cfg.Dir("assets"), name)
	if _, err := os.Stat(path); err != nil {
		http.Error(w, "asset not found", http.StatusNotFound)
		return
	}
	http.ServeFile(w, r, path)
}

// ---- settings centre ----

type settingsPayload struct {
	SiteTitle    string `json:"site_title"`
	BarkEnabled  bool   `json:"bark_enabled"`
	BarkServer   string `json:"bark_server"`
	BarkKey      string `json:"bark_key"`
	BarkSound    string `json:"bark_sound"`
	BarkGroup    string `json:"bark_group"`
	KeepVersions int    `json:"keep_versions"`
}

func (s *Server) currentSettings() store.Settings {
	var cfg store.Settings
	s.st.View(func(d *store.Data) { cfg = d.Settings })
	if strings.TrimSpace(cfg.SiteTitle) == "" {
		cfg.SiteTitle = store.DefaultSiteTitle
	}
	return cfg
}

// settingsView never leaks the Bark device key to non-administrators.
func settingsView(cfg store.Settings, editable bool) map[string]any {
	view := map[string]any{
		"site_title":    cfg.SiteTitle,
		"keep_versions": cfg.KeepVersions,
		"editable":      editable,
	}
	if editable {
		view["bark_enabled"] = cfg.BarkEnabled
		view["bark_server"] = cfg.BarkServer
		view["bark_key"] = cfg.BarkKey
		view["bark_sound"] = cfg.BarkSound
		view["bark_group"] = cfg.BarkGroup
	}
	return view
}

func (s *Server) getSettings(w http.ResponseWriter, _ *http.Request, u *store.User) {
	writeJSON(w, http.StatusOK, settingsView(s.currentSettings(), u.Role == "admin"))
}

func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request, u *store.User) {
	if u.Role != "admin" {
		writeError(w, http.StatusForbidden, errors.New("administrator only"))
		return
	}
	var p settingsPayload
	if err := decodeJSON(r, &p); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	title := strings.TrimSpace(p.SiteTitle)
	if title == "" {
		title = store.DefaultSiteTitle
	}
	if len([]rune(title)) > 60 {
		writeError(w, http.StatusBadRequest, errors.New("site title must be at most 60 characters"))
		return
	}
	server := strings.TrimRight(strings.TrimSpace(p.BarkServer), "/")
	if server != "" {
		if err := source.ValidateURL(server); err != nil {
			writeError(w, http.StatusBadRequest, errors.New("Bark 服务器地址必须是完整的 http(s) URL"))
			return
		}
	}
	keep := p.KeepVersions
	if keep < 1 {
		keep = 1
	}
	if keep > 50 {
		keep = 50
	}
	next := store.Settings{
		SiteTitle:    title,
		BarkEnabled:  p.BarkEnabled,
		BarkServer:   server,
		BarkKey:      strings.TrimSpace(p.BarkKey),
		BarkSound:    strings.TrimSpace(p.BarkSound),
		BarkGroup:    strings.TrimSpace(p.BarkGroup),
		KeepVersions: keep,
		UpdatedAt:    time.Now(),
	}
	if err := s.st.Update(func(d *store.Data) error {
		d.Settings = next
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// Apply the new limit immediately so lowering it cleans up old artifacts
	// right away instead of waiting for the next signing run. The signing
	// manager is optional in some test harnesses, so guard the call.
	if s.signing != nil {
		if err := s.signing.TrimAll(keep); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, settingsView(s.currentSettings(), true))
}

func (s *Server) testBark(w http.ResponseWriter, r *http.Request, u *store.User) {
	if u.Role != "admin" {
		writeError(w, http.StatusForbidden, errors.New("administrator only"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := s.notifier.Test(ctx); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// runNow performs one full check on demand: refresh every enabled source the
// caller owns, then enqueue signing jobs for newly detected versions.
func (s *Server) runNow(w http.ResponseWriter, r *http.Request, u *store.User) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	var ids []string
	s.st.View(func(d *store.Data) {
		for id, src := range d.Sources {
			if src.Enabled && (u.Role == "admin" || src.OwnerID == u.ID) {
				ids = append(ids, id)
			}
		}
	})
	var results []source.SyncResult
	var errs []string
	for _, id := range ids {
		res, err := s.sources.Sync(ctx, id)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		results = append(results, *res)
	}
	created := 0
	if s.signing != nil {
		created, _ = s.signing.EnqueuePending()
	}
	newVersions := 0
	updates := []source.AppUpdate{}
	for _, res := range results {
		newVersions += res.NewVersions
		updates = append(updates, res.Updates...)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sources":      len(results),
		"new_versions": newVersions,
		"updates":      updates,
		"jobs_created": created,
		"errors":       errs,
	})
}
