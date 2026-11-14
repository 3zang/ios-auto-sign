// Package signing implements the job queue, zsign invocation, artifact
// publishing and the "keep last N signed versions" retention policy.
package signing

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"ios-auto-sign/internal/config"
	"ios-auto-sign/internal/secure"
	"ios-auto-sign/internal/store"
)

type Manager struct {
	st           *store.Store
	cfg          config.Config
	key          *[32]byte
	client       *http.Client
	downloadHTTP *http.Client
	jobs         chan string
}

func New(st *store.Store, cfg config.Config, key *[32]byte, client *http.Client) *Manager {
	return &Manager{
		st: st, cfg: cfg, key: key, client: client,
		downloadHTTP: &http.Client{Timeout: 15 * time.Minute},
		jobs:         make(chan string, 256),
	}
}

// Start launches the single worker. Signing is IO and CPU heavy, so jobs are
// serialized deliberately. Jobs interrupted by a restart are re-queued.
func (m *Manager) Start(ctx context.Context) {
	var pending []string
	_ = m.st.Update(func(d *store.Data) error {
		for id, j := range d.Jobs {
			switch j.Status {
			case "queued":
				pending = append(pending, id)
			case "running":
				j.Status = "failed"
				j.Error = "interrupted by service restart"
				j.FinishedAt = time.Now()
			}
		}
		return nil
	})
	go func() {
		for _, id := range pending {
			select {
			case m.jobs <- id:
			case <-ctx.Done():
				return
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case id := <-m.jobs:
				m.run(ctx, id)
			}
		}
	}()
}

type candidate struct {
	userID, appID, versionID, certID string
}

// EnqueuePending scans enabled app configs and creates a job whenever the
// latest upstream version has not been signed yet. It is idempotent.
func (m *Manager) EnqueuePending() (int, error) {
	var cands []candidate
	m.st.View(func(d *store.Data) {
		for _, c := range d.AppConfigs {
			if !c.Enabled || c.CertificateID == "" {
				continue
			}
			cert := d.Certificates[c.CertificateID]
			if cert == nil || cert.UserID != c.UserID {
				continue
			}
			var latest *store.AppVersion
			for _, v := range d.Versions {
				if v.AppID != c.AppID {
					continue
				}
				if latest == nil || v.DetectedAt.After(latest.DetectedAt) ||
					(v.DetectedAt.Equal(latest.DetectedAt) && v.Date > latest.Date) {
					latest = v
				}
			}
			if latest == nil {
				continue
			}
			already := false
			for _, a := range d.Artifacts {
				if a.UserID == c.UserID && a.AppID == c.AppID && a.VersionID == latest.ID {
					already = true
					break
				}
			}
			failures := 0
			var lastFailure time.Time
			for _, j := range d.Jobs {
				if j.UserID != c.UserID || j.VersionID != latest.ID {
					continue
				}
				switch j.Status {
				case "queued", "running":
					already = true
				case "failed":
					failures++
					if j.FinishedAt.After(lastFailure) {
						lastFailure = j.FinishedAt
					}
				}
				if already {
					break
				}
			}
			// A broken certificate or a dead download URL must not be retried
			// on every poll. Automatic retries are cooled down and capped;
			// manual "sign now" still works at any time.
			if !already && failures > 0 &&
				(failures >= 3 || time.Since(lastFailure) < time.Hour) {
				already = true
			}
			if !already {
				cands = append(cands, candidate{c.UserID, c.AppID, latest.ID, c.CertificateID})
			}
		}
	})
	created := 0
	for _, c := range cands {
		if _, err := m.createJob(c); err == nil {
			created++
		}
	}
	return created, nil
}

// Enqueue forces a re-sign of the latest version for one app config.
func (m *Manager) Enqueue(userID, appID string) (string, error) {
	var c candidate
	var found bool
	m.st.View(func(d *store.Data) {
		cfg := d.AppConfigs[store.ConfigKey(userID, appID)]
		if cfg == nil || cfg.CertificateID == "" {
			return
		}
		var latest *store.AppVersion
		for _, v := range d.Versions {
			if v.AppID == appID && (latest == nil || v.DetectedAt.After(latest.DetectedAt)) {
				latest = v
			}
		}
		if latest == nil {
			return
		}
		c = candidate{userID, appID, latest.ID, cfg.CertificateID}
		found = true
	})
	if !found {
		return "", errors.New("app is not enabled, has no certificate, or has no version metadata")
	}
	return m.createJob(c)
}

// maxJobRecords is how many signature job records are kept per account. The
// UI shows the same number, so old records are trimmed instead of growing
// forever in store.json.
const maxJobRecords = 5

// trimJobs keeps the newest records per user. Queued/running jobs are never
// removed, even if they would exceed the limit, because deleting them would
// leave orphaned work in the queue.
func trimJobs(d *store.Data, keep int) {
	byUser := map[string][]*store.SignJob{}
	for _, j := range d.Jobs {
		byUser[j.UserID] = append(byUser[j.UserID], j)
	}
	for _, list := range byUser {
		if len(list) <= keep {
			continue
		}
		sort.Slice(list, func(i, j int) bool {
			return list[i].CreatedAt.After(list[j].CreatedAt)
		})
		for _, j := range list[keep:] {
			if j.Status == "queued" || j.Status == "running" {
				continue
			}
			delete(d.Jobs, j.ID)
		}
	}
}

func (m *Manager) createJob(c candidate) (string, error) {
	id := store.NewID()
	now := time.Now()
	err := m.st.Update(func(d *store.Data) error {
		d.Jobs[id] = &store.SignJob{
			ID: id, UserID: c.userID, AppID: c.appID, VersionID: c.versionID,
			CertificateID: c.certID, Status: "queued", CreatedAt: now,
		}
		trimJobs(d, maxJobRecords)
		return nil
	})
	if err != nil {
		return "", err
	}
	select {
	case m.jobs <- id:
	default:
		go func() { m.jobs <- id }()
	}
	return id, nil
}

func (m *Manager) run(ctx context.Context, jobID string) {
	var job *store.SignJob
	if err := m.st.Update(func(d *store.Data) error {
		j := d.Jobs[jobID]
		if j == nil || j.Status != "queued" {
			return errors.New("job not runnable")
		}
		j.Status = "running"
		j.StartedAt = time.Now()
		cp := *j
		job = &cp
		return nil
	}); err != nil {
		return
	}
	logText, err := m.execute(ctx, job)
	if len(logText) > 8192 {
		logText = logText[len(logText)-8192:]
	}
	_ = m.st.Update(func(d *store.Data) error {
		if j := d.Jobs[jobID]; j != nil {
			j.Log = logText
			j.FinishedAt = time.Now()
			if err != nil {
				j.Status = "failed"
				j.Error = err.Error()
			} else {
				j.Status = "succeeded"
				j.Error = ""
			}
		}
		return nil
	})
}

func (m *Manager) execute(ctx context.Context, job *store.SignJob) (string, error) {
	var app store.App
	var ver store.AppVersion
	var cfg store.AppConfig
	var cert store.Certificate
	var iconURL string
	var ok bool
	m.st.View(func(d *store.Data) {
		if a := d.Apps[job.AppID]; a != nil {
			app = *a
			ok = true
		}
		if v := d.Versions[job.VersionID]; v != nil {
			ver = *v
		}
		if c := d.AppConfigs[store.ConfigKey(job.UserID, job.AppID)]; c != nil {
			cfg = *c
		}
		if c := d.Certificates[job.CertificateID]; c != nil {
			cert = *c
		}
		switch cfg.IconSource {
		case "library":
			if ic := d.Icons[cfg.IconLibraryID]; ic != nil && ic.Enabled {
				iconURL = ic.URL
			}
		case "custom":
			iconURL = cfg.IconCustomURL
		default:
			iconURL = app.IconURL
		}
	})
	if !ok || ver.DownloadURL == "" || cert.P12Enc == "" {
		return "", errors.New("missing app, version or certificate data")
	}

	tmpRoot := m.cfg.Dir("tmp")
	if err := os.MkdirAll(tmpRoot, 0o700); err != nil {
		return "", err
	}
	work, err := os.MkdirTemp(tmpRoot, "job-"+job.ID+"-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)

	unsigned := filepath.Join(work, "unsigned.ipa")
	if err := download(ctx, m.downloadHTTP, ver.DownloadURL, unsigned); err != nil {
		return "", fmt.Errorf("download unsigned IPA: %w", err)
	}

	p12, err := secure.Decrypt(cert.P12Enc, m.key)
	if err != nil {
		return "", fmt.Errorf("decrypt p12: %w", err)
	}
	passwordBytes, err := secure.Decrypt(cert.PasswordEnc, m.key)
	if err != nil {
		return "", fmt.Errorf("decrypt p12 password: %w", err)
	}
	prov, err := secure.Decrypt(cert.ProvisionEnc, m.key)
	if err != nil {
		return "", fmt.Errorf("decrypt provisioning profile: %w", err)
	}
	password := string(passwordBytes)
	p12Path := filepath.Join(work, "cert.p12")
	provPath := filepath.Join(work, "dev.mobileprovision")
	if err := os.WriteFile(p12Path, p12, 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(provPath, prov, 0o600); err != nil {
		return "", err
	}

	iconInWork := ""
	if iconURL != "" {
		dst := filepath.Join(work, "icon.png")
		if err := download(ctx, m.downloadHTTP, iconURL, dst); err == nil {
			iconInWork = dst
		}
	}

	name := cfg.DisplayName
	if name == "" {
		name = app.Name
	}
	bundleID := cfg.BundleIDOverride
	if bundleID == "" {
		bundleID = app.BundleID
	}
	zipLevel := m.cfg.SignOptions.ZipLevel
	if cfg.SignOptions.ZipLevel != nil {
		zipLevel = *cfg.SignOptions.ZipLevel
	}
	if zipLevel < 0 || zipLevel > 9 {
		zipLevel = 9
	}
	force := m.cfg.SignOptions.Force || cfg.SignOptions.Force
	adhoc := m.cfg.SignOptions.Adhoc || cfg.SignOptions.Adhoc

	outIPA := filepath.Join(work, "signed.ipa")
	args := make([]string, 0, 16)
	if adhoc {
		// Ad-hoc signing does not consume p12 / provisioning profile.
		args = append(args, "-a")
	} else {
		args = append(args, "-k", p12Path, "-p", password, "-m", provPath)
	}
	if force {
		args = append(args, "-f")
	}
	if name != "" {
		args = append(args, "-n", name)
	}
	if cfg.BundleIDOverride != "" {
		args = append(args, "-b", cfg.BundleIDOverride)
	}
	if iconInWork != "" {
		args = append(args, "-I", iconInWork)
	}
	args = append(args, "-z", strconv.Itoa(zipLevel), "-o", outIPA)
	args = append(args, m.cfg.SignOptions.ExtraArgs...)
	args = append(args, cfg.SignOptions.ExtraArgs...)
	args = append(args, unsigned)

	signCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(signCtx, m.cfg.ZsignPath, args...)
	output, cmdErr := cmd.CombinedOutput()
	logText := redact(string(output), password)
	if cmdErr != nil {
		return logText + "\n" + redact(cmdErr.Error(), password),
			fmt.Errorf("zsign failed: %s", redact(cmdErr.Error(), password))
	}
	if err := validateIPA(outIPA); err != nil {
		return logText, fmt.Errorf("signed IPA validation: %w", err)
	}

	artifactID := store.NewID()
	ipaRel := filepath.Join("artifacts", artifactID+".ipa")
	ipaAbs := filepath.Join(m.cfg.DataDir, ipaRel)
	if err := moveFile(outIPA, ipaAbs); err != nil {
		return logText, err
	}
	stat, err := os.Stat(ipaAbs)
	if err != nil {
		return logText, err
	}

	iconRel := ""
	localIconURL := ""
	if iconInWork != "" {
		iconRel = filepath.Join("assets", artifactID+".png")
		if err := copyFile(iconInWork, filepath.Join(m.cfg.DataDir, iconRel)); err == nil {
			localIconURL = m.cfg.PublicBaseURL + "/assets/" + artifactID + ".png"
		}
	}
	if localIconURL == "" && strings.HasPrefix(iconURL, "https://") {
		localIconURL = iconURL
	}

	// The install manifest is generated on demand by the HTTP layer, where the
	// current request's real address is known; nothing is stored here.

	if err := m.st.Update(func(d *store.Data) error {
		d.Artifacts[artifactID] = &store.Artifact{
			ID: artifactID, JobID: job.ID, UserID: job.UserID, AppID: job.AppID,
			VersionID: job.VersionID, DisplayName: name, BundleID: bundleID,
			Version: ver.Version, IconURL: localIconURL,
			IPAPath: ipaRel, IconPath: iconRel, Size: stat.Size(), CreatedAt: time.Now(),
		}
		return nil
	}); err != nil {
		return logText, err
	}
	m.enforceRetention(job.UserID, job.AppID)
	return logText + "\nartifact: " + artifactID, nil
}

// keepVersions returns the effective retention count: the administrator-set
// value wins, otherwise the config file default is used. It is always >= 1.
func (m *Manager) keepVersions() int {
	var n int
	m.st.View(func(d *store.Data) { n = d.Settings.KeepVersions })
	if n < 1 {
		n = m.cfg.KeepVersions
	}
	if n < 1 {
		n = 1
	}
	return n
}

// removeArtifactFiles deletes the on-disk IPA, icon and any cached manifest
// for an artifact. Missing files are ignored.
func removeArtifactFiles(cfg config.Config, a store.Artifact) {
	for _, rel := range []string{a.IPAPath, a.IconPath} {
		if rel != "" {
			_ = os.Remove(filepath.Join(cfg.DataDir, rel))
		}
	}
	_ = os.Remove(filepath.Join(cfg.Dir("manifests"), a.ID+".plist"))
}

// enforceRetention keeps only the newest keepVersions artifacts per user+app
// and removes their IPA, icon and manifest files from disk.
func (m *Manager) enforceRetention(userID, appID string) {
	var arts []store.Artifact
	m.st.View(func(d *store.Data) {
		for _, a := range d.Artifacts {
			if a.UserID == userID && a.AppID == appID {
				arts = append(arts, *a)
			}
		}
	})
	keep := m.keepVersions()
	sort.Slice(arts, func(i, j int) bool { return arts[i].CreatedAt.After(arts[j].CreatedAt) })
	if len(arts) <= keep {
		return
	}
	stale := arts[keep:]
	for _, a := range stale {
		removeArtifactFiles(m.cfg, a)
	}
	_ = m.st.Update(func(d *store.Data) error {
		for _, a := range stale {
			delete(d.Artifacts, a.ID)
		}
		return nil
	})
}

// DeleteArtifact removes a single published artifact and its install data
// (IPA file, icon file and cached manifest). The install list is derived from
// artifacts, so removing it also drops the corresponding install entry.
func (m *Manager) DeleteArtifact(id string) error {
	var a *store.Artifact
	m.st.View(func(d *store.Data) {
		if x := d.Artifacts[id]; x != nil {
			cp := *x
			a = &cp
		}
	})
	if a == nil {
		return errors.New("artifact not found")
	}
	removeArtifactFiles(m.cfg, *a)
	return m.st.Update(func(d *store.Data) error {
		delete(d.Artifacts, id)
		return nil
	})
}

// TrimAll enforces the retention limit across every user+app pair at once. It
// is used when the administrator lowers the keep-versions setting so existing
// artifacts are cleaned up immediately rather than only on the next signing.
func (m *Manager) TrimAll(keep int) error {
	if keep < 1 {
		keep = 1
	}
	byKey := map[string][]*store.Artifact{}
	m.st.View(func(d *store.Data) {
		for _, a := range d.Artifacts {
			key := a.UserID + "\x00" + a.AppID
			byKey[key] = append(byKey[key], a)
		}
	})
	var remove []string
	for _, list := range byKey {
		if len(list) <= keep {
			continue
		}
		sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.After(list[j].CreatedAt) })
		for _, a := range list[keep:] {
			remove = append(remove, a.ID)
			removeArtifactFiles(m.cfg, *a)
		}
	}
	if len(remove) == 0 {
		return nil
	}
	return m.st.Update(func(d *store.Data) error {
		for _, id := range remove {
			delete(d.Artifacts, id)
		}
		return nil
	})
}

func redact(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "***")
}

func download(ctx context.Context, client *http.Client, rawURL, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, io.LimitReader(resp.Body, 1<<30))
	return err
}

func validateIPA(path string) error {
	z, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer z.Close()
	for _, f := range z.File {
		if strings.HasPrefix(f.Name, "Payload/") {
			return nil
		}
	}
	return errors.New("no Payload/ entry in IPA")
}

func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := copyFile(src, dst); err != nil {
		return err
	}
	return os.Remove(src)
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
