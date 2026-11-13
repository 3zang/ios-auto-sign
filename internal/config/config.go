// Package config loads the service configuration from a JSON file with
// environment-variable overrides. All paths are normalized to absolute paths
// so the same config behaves identically on Linux and Windows.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const DefaultSourceURL = "https://raw.githubusercontent.com/braless/soft_collections/main/all-apps.json"

type PlistConfig struct {
	// Mode is "builtin" or "plistserver".
	Mode           string `json:"mode"`
	PlistServerURL string `json:"plistserver_url"`
	// IPABaseURL overrides the base used to build fetchurl / IPA links.
	// Empty means PublicBaseURL.
	IPABaseURL string `json:"ipa_base_url"`
}

type SignOptions struct {
	ZipLevel  int      `json:"zip_level"`
	Force     bool     `json:"force"`
	Adhoc     bool     `json:"adhoc"`
	ExtraArgs []string `json:"extra_args"`
}

type Config struct {
	Listen              string      `json:"listen"`
	PublicBaseURL       string      `json:"public_base_url"`
	DataDir             string      `json:"data_dir"`
	MasterKeyFile       string      `json:"master_key_file"`
	ZsignPath           string      `json:"zsign_path"`
	PollIntervalMinutes int         `json:"poll_interval_minutes"`
	KeepVersions        int         `json:"keep_versions"`
	HTTPTimeoutSeconds  int         `json:"http_timeout_seconds"`
	DefaultSourceURL    string      `json:"default_source_url"`
	Plist               PlistConfig `json:"plist"`
	SignOptions         SignOptions `json:"sign_options"`
}

func defaultZsignPath() string {
	name := "zsign"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join("bin", name)
}

func Default() Config {
	return Config{
		Listen:              "127.0.0.1:8080",
		PublicBaseURL:       "http://127.0.0.1:8080",
		DataDir:             "data",
		MasterKeyFile:       "master.key",
		ZsignPath:           defaultZsignPath(),
		PollIntervalMinutes: 30,
		KeepVersions:        2,
		HTTPTimeoutSeconds:  60,
		DefaultSourceURL:    DefaultSourceURL,
		Plist: PlistConfig{
			Mode:           "builtin",
			PlistServerURL: "http://127.0.0.1:3788",
		},
		SignOptions: SignOptions{ZipLevel: 9},
	}
}

// Load reads config.json (or the path supplied by -config / IOS_SIGN_CONFIG).
// A missing file is not an error: defaults plus environment overrides are used.
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		path = os.Getenv("IOS_SIGN_CONFIG")
	}
	if path == "" {
		path = "config.json"
	}
	b, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(b, &cfg); err != nil {
			return cfg, fmt.Errorf("parse config %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return cfg, fmt.Errorf("read config %s: %w", path, err)
	}

	envStr("IOS_SIGN_LISTEN", &cfg.Listen)
	envStr("IOS_SIGN_PUBLIC_BASE_URL", &cfg.PublicBaseURL)
	envStr("IOS_SIGN_DATA_DIR", &cfg.DataDir)
	envStr("IOS_SIGN_MASTER_KEY_FILE", &cfg.MasterKeyFile)
	envStr("IOS_SIGN_ZSIGN", &cfg.ZsignPath)
	envStr("IOS_SIGN_DEFAULT_SOURCE", &cfg.DefaultSourceURL)
	envStr("IOS_SIGN_PLIST_MODE", &cfg.Plist.Mode)
	envStr("IOS_SIGN_PLISTSERVER_URL", &cfg.Plist.PlistServerURL)
	envStr("IOS_SIGN_PLIST_IPA_BASE_URL", &cfg.Plist.IPABaseURL)
	envInt("IOS_SIGN_POLL_MINUTES", &cfg.PollIntervalMinutes)
	envInt("IOS_SIGN_KEEP_VERSIONS", &cfg.KeepVersions)
	envInt("IOS_SIGN_HTTP_TIMEOUT", &cfg.HTTPTimeoutSeconds)

	if cfg.PollIntervalMinutes < 1 {
		cfg.PollIntervalMinutes = 1
	}
	if cfg.KeepVersions < 1 {
		cfg.KeepVersions = 1
	}
	if cfg.HTTPTimeoutSeconds < 5 {
		cfg.HTTPTimeoutSeconds = 5
	}
	if cfg.SignOptions.ZipLevel < 0 || cfg.SignOptions.ZipLevel > 9 {
		cfg.SignOptions.ZipLevel = 9
	}
	if cfg.DefaultSourceURL == "" {
		cfg.DefaultSourceURL = DefaultSourceURL
	}
	cfg.Plist.Mode = strings.ToLower(strings.TrimSpace(cfg.Plist.Mode))
	if cfg.Plist.Mode != "builtin" && cfg.Plist.Mode != "plistserver" {
		return cfg, fmt.Errorf("plist.mode must be builtin or plistserver, got %q", cfg.Plist.Mode)
	}
	if cfg.Plist.PlistServerURL == "" {
		cfg.Plist.PlistServerURL = "http://127.0.0.1:3788"
	}
	cfg.Plist.PlistServerURL = strings.TrimRight(cfg.Plist.PlistServerURL, "/")
	cfg.PublicBaseURL = strings.TrimRight(cfg.PublicBaseURL, "/")
	cfg.Plist.IPABaseURL = strings.TrimRight(strings.TrimSpace(cfg.Plist.IPABaseURL), "/")
	if cfg.Plist.IPABaseURL == "" {
		cfg.Plist.IPABaseURL = cfg.PublicBaseURL
	}

	abs, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return cfg, err
	}
	cfg.DataDir = abs
	if !filepath.IsAbs(cfg.MasterKeyFile) {
		cfg.MasterKeyFile = filepath.Join(cfg.DataDir, cfg.MasterKeyFile)
	}
	if cfg.ZsignPath == "" {
		cfg.ZsignPath = defaultZsignPath()
	}
	return cfg, nil
}

func envStr(key string, dst *string) {
	if v := os.Getenv(key); v != "" {
		*dst = v
	}
}

func envInt(key string, dst *int) {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			*dst = n
		}
	}
}

// Dir returns a subdirectory inside the data directory.
func (c Config) Dir(name string) string {
	if name == "" {
		return c.DataDir
	}
	return filepath.Join(c.DataDir, name)
}

func (c Config) EnsureDirs() error {
	for _, d := range []string{"", "artifacts", "manifests", "assets", "tmp"} {
		if err := os.MkdirAll(c.Dir(d), 0o700); err != nil {
			return fmt.Errorf("create data dir %s: %w", c.Dir(d), err)
		}
	}
	return nil
}
