// Package store is the persistence layer. It keeps the whole dataset in
// memory behind a RWMutex and writes atomically to a single JSON file.
//
// This is intentionally dependency-free so the binary builds without CGO on
// Windows and Linux. The Update/View boundary is the seam for replacing this
// with SQLite later without touching the HTTP or signing layers.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ios-auto-sign/internal/secure"
)

type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"password_hash"`
	Role         string    `json:"role"` // admin | user
	CreatedAt    time.Time `json:"created_at"`
}

type Session struct {
	Token     string    `json:"token"`
	UserID    string    `json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Source struct {
	ID              string    `json:"id"`
	OwnerID         string    `json:"owner_id"`
	Name            string    `json:"name"`
	URL             string    `json:"url"`
	Enabled         bool      `json:"enabled"`
	IntervalMinutes int       `json:"interval_minutes"`
	ETag            string    `json:"etag"`
	LastHash        string    `json:"last_hash"`
	LastCheckedAt   time.Time `json:"last_checked_at"`
	LastChangedAt   time.Time `json:"last_changed_at"`
	LastError       string    `json:"last_error"`
	CreatedAt       time.Time `json:"created_at"`
}

type App struct {
	ID            string    `json:"id"`
	SourceID      string    `json:"source_id"`
	BundleID      string    `json:"bundle_id"`
	Name          string    `json:"name"`
	DeveloperName string    `json:"developer_name"`
	Subtitle      string    `json:"subtitle"`
	Description   string    `json:"description"`
	IconURL       string    `json:"icon_url"`
	TintColor     string    `json:"tint_color"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type AppVersion struct {
	ID                   string    `json:"id"`
	AppID                string    `json:"app_id"`
	Version              string    `json:"version"`
	BuildVersion         string    `json:"build_version"`
	Date                 string    `json:"date"`
	DownloadURL          string    `json:"download_url"`
	LocalizedDescription string    `json:"localized_description"`
	Size                 int64     `json:"size"`
	MinOSVersion         string    `json:"min_os_version"`
	DetectedAt           time.Time `json:"detected_at"`
}

// SignOptionOverride stores per-app overrides. Pointer fields mean "inherit
// the global default" when nil.
type SignOptionOverride struct {
	ZipLevel  *int     `json:"zip_level,omitempty"`
	Force     bool     `json:"force"`
	Adhoc     bool     `json:"adhoc"`
	ExtraArgs []string `json:"extra_args,omitempty"`
}

type AppConfig struct {
	ID               string             `json:"id"`
	UserID           string             `json:"user_id"`
	AppID            string             `json:"app_id"`
	Enabled          bool               `json:"enabled"`
	DisplayName      string             `json:"display_name"`
	IconSource       string             `json:"icon_source"` // original | library | custom
	IconLibraryID    string             `json:"icon_library_id"`
	IconCustomURL    string             `json:"icon_custom_url"`
	BundleIDOverride string             `json:"bundle_id_override"`
	CertificateID    string             `json:"certificate_id"`
	SignOptions      SignOptionOverride `json:"sign_options"`
	UpdatedAt        time.Time          `json:"updated_at"`
}

type Icon struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	URL       string    `json:"url"`
	CreatedBy string    `json:"created_by"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

// Certificate stores the p12, its password and the provisioning profile as
// AES-GCM ciphertext (base64). Never expose these fields over the API.
type Certificate struct {
	ID           string    `json:"id"`
	UserID       string    `json:"user_id"`
	Name         string    `json:"name"`
	P12Enc       string    `json:"p12_enc"`
	PasswordEnc  string    `json:"password_enc"`
	ProvisionEnc string    `json:"provision_enc"`
	CreatedAt    time.Time `json:"created_at"`
}

type SignJob struct {
	ID            string    `json:"id"`
	UserID        string    `json:"user_id"`
	AppID         string    `json:"app_id"`
	VersionID     string    `json:"version_id"`
	CertificateID string    `json:"certificate_id"`
	Status        string    `json:"status"` // queued | running | succeeded | failed
	Error         string    `json:"error"`
	Log           string    `json:"log"`
	CreatedAt     time.Time `json:"created_at"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
}

type Artifact struct {
	ID          string    `json:"id"`
	JobID       string    `json:"job_id"`
	UserID      string    `json:"user_id"`
	AppID       string    `json:"app_id"`
	VersionID   string    `json:"version_id"`
	DisplayName string    `json:"display_name"`
	BundleID    string    `json:"bundle_id"`
	Version     string    `json:"version"`
	IconURL     string    `json:"icon_url"`
	IPAPath     string    `json:"ipa_path"`  // relative to data dir
	IconPath    string    `json:"icon_path"` // relative to data dir
	Size        int64     `json:"size"`
	CreatedAt   time.Time `json:"created_at"`
}

// DefaultSiteTitle is used until an administrator sets a custom title in the
// settings centre. It is also the fallback for old store.json files that
// predate the settings section.
const DefaultSiteTitle = "iOS 自动签名"

// Settings holds the global, administrator-managed preferences. Unlike config
// values these are editable at runtime from the web UI and persisted in the
// same atomic store file.
type Settings struct {
	SiteTitle   string    `json:"site_title"`
	BarkEnabled bool      `json:"bark_enabled"`
	BarkServer  string    `json:"bark_server"`
	BarkKey     string    `json:"bark_key"`
	BarkSound   string    `json:"bark_sound"`
	BarkGroup   string    `json:"bark_group"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Data struct {
	Users        map[string]*User        `json:"users"`
	Sessions     map[string]*Session     `json:"sessions"`
	Sources      map[string]*Source      `json:"sources"`
	Apps         map[string]*App         `json:"apps"`
	Versions     map[string]*AppVersion  `json:"versions"`
	AppConfigs   map[string]*AppConfig   `json:"app_configs"`
	Icons        map[string]*Icon        `json:"icons"`
	Certificates map[string]*Certificate `json:"certificates"`
	Jobs         map[string]*SignJob     `json:"jobs"`
	Artifacts    map[string]*Artifact    `json:"artifacts"`
	Settings     Settings                `json:"settings"`
}

func (d *Data) init() {
	if d.Users == nil {
		d.Users = map[string]*User{}
	}
	if d.Sessions == nil {
		d.Sessions = map[string]*Session{}
	}
	if d.Sources == nil {
		d.Sources = map[string]*Source{}
	}
	if d.Apps == nil {
		d.Apps = map[string]*App{}
	}
	if d.Versions == nil {
		d.Versions = map[string]*AppVersion{}
	}
	if d.AppConfigs == nil {
		d.AppConfigs = map[string]*AppConfig{}
	}
	if d.Icons == nil {
		d.Icons = map[string]*Icon{}
	}
	if d.Certificates == nil {
		d.Certificates = map[string]*Certificate{}
	}
	if d.Jobs == nil {
		d.Jobs = map[string]*SignJob{}
	}
	if d.Artifacts == nil {
		d.Artifacts = map[string]*Artifact{}
	}
	if strings.TrimSpace(d.Settings.SiteTitle) == "" {
		d.Settings.SiteTitle = DefaultSiteTitle
	}
}

type Store struct {
	mu   sync.RWMutex
	path string
	data Data
}

func Open(dataDir string) (*Store, error) {
	s := &Store{path: filepath.Join(dataDir, "store.json")}
	b, err := os.ReadFile(s.path)
	if err == nil {
		if err := json.Unmarshal(b, &s.data); err != nil {
			return nil, fmt.Errorf("parse %s: %w", s.path, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	s.data.init()
	return s, nil
}

// Update runs fn under the write lock. A snapshot is restored if fn or the
// persistence step fails, so failed operations never expose partial changes.
func (s *Store) Update(fn func(*Data) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, err := s.snapshotLocked()
	if err != nil {
		return err
	}
	if err := fn(&s.data); err != nil {
		s.data = snapshot
		return err
	}
	if err := s.saveLocked(); err != nil {
		s.data = snapshot
		return err
	}
	return nil
}

func (s *Store) snapshotLocked() (Data, error) {
	b, err := json.Marshal(&s.data)
	if err != nil {
		return Data{}, err
	}
	var d Data
	if err := json.Unmarshal(b, &d); err != nil {
		return Data{}, err
	}
	d.init()
	return d, nil
}

// View exposes a read-only snapshot. Callers must not retain pointers.
func (s *Store) View(fn func(*Data)) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	fn(&s.data)
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(&s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		// Some Windows filesystems refuse to overwrite an existing target.
		if removeErr := os.Remove(s.path); removeErr == nil {
			if err2 := os.Rename(tmp, s.path); err2 == nil {
				return nil
			}
		}
		return err
	}
	return nil
}

func NewID() string {
	id, err := secure.RandomHex(12)
	if err != nil {
		// crypto/rand failure is not recoverable; a timestamp keeps uniqueness
		// within this process so the caller can still surface a proper error.
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return id
}

func ConfigKey(userID, appID string) string { return userID + ":" + appID }

func (s *Store) UserByUsername(username string) (*User, bool) {
	var out *User
	s.View(func(d *Data) {
		for _, u := range d.Users {
			if u.Username == username {
				cp := *u
				out = &cp
				return
			}
		}
	})
	return out, out != nil
}

func (s *Store) UserCount() int {
	n := 0
	s.View(func(d *Data) { n = len(d.Users) })
	return n
}

func (s *Store) Artifact(id string) (*Artifact, bool) {
	var out *Artifact
	s.View(func(d *Data) {
		if a := d.Artifacts[id]; a != nil {
			cp := *a
			out = &cp
		}
	})
	return out, out != nil
}

func (s *Store) Certificate(id string) (*Certificate, bool) {
	var out *Certificate
	s.View(func(d *Data) {
		if c := d.Certificates[id]; c != nil {
			cp := *c
			out = &cp
		}
	})
	return out, out != nil
}

func (s *Store) Source(id string) (*Source, bool) {
	var out *Source
	s.View(func(d *Data) {
		if v := d.Sources[id]; v != nil {
			cp := *v
			out = &cp
		}
	})
	return out, out != nil
}
