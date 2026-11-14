package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ios-auto-sign/internal/auth"
	"ios-auto-sign/internal/config"
	"ios-auto-sign/internal/notify"
	"ios-auto-sign/internal/plist"
	"ios-auto-sign/internal/secure"
	"ios-auto-sign/internal/signing"
	"ios-auto-sign/internal/source"
	"ios-auto-sign/internal/store"
	"ios-auto-sign/internal/web"
)

func main() {
	configPath := flag.String("config", "", "path to config.json")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		log.Fatalf("data dirs: %v", err)
	}
	key, err := secure.LoadMasterKey(cfg.MasterKeyFile)
	if err != nil {
		log.Fatalf("master key: %v", err)
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	client := &http.Client{Timeout: time.Duration(cfg.HTTPTimeoutSeconds) * time.Second}
	gen, err := plist.New(cfg.Plist.Mode, cfg.Plist.PlistServerURL, client)
	if err != nil {
		log.Fatalf("plist generator: %v", err)
	}

	authMgr := auth.New(st, cfg.PublicBaseURL)
	sourceSvc := source.New(st, client)
	signMgr := signing.New(st, cfg, key, client)
	server := web.New(cfg, st, authMgr, sourceSvc, signMgr, gen, key, client)

	// Notifications are driven by sync results from pollLoop and the HTTP
	// handlers, not by an implicit source hook, so background detection and
	// manual "立即执行" take exactly the same path.
	bark := notify.New(st, client)

	ensureDefaultSource(st, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signMgr.Start(ctx)
	go pollLoop(ctx, cfg, st, sourceSvc, signMgr, bark)

	httpServer := &http.Server{
		Addr:              cfg.Listen,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Printf("listening on %s (public base %s)", cfg.Listen, cfg.PublicBaseURL)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	}()

	log.Printf("plist mode=%s  zsign=%s  keep_versions=%d  data=%s",
		gen.Mode(), cfg.ZsignPath, cfg.KeepVersions, cfg.DataDir)
	if _, err := os.Stat(cfg.ZsignPath); err != nil {
		log.Printf("WARNING: zsign not found at %s - signing jobs will fail until it is installed", cfg.ZsignPath)
	}
	if len(os.Getenv("IOS_SIGN_MASTER_KEY")) == 0 {
		log.Printf("master key stored at %s - back it up; losing it makes stored certificates undecryptable", cfg.MasterKeyFile)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Printf("shutting down")
	cancel()
	shutdownCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	_ = httpServer.Shutdown(shutdownCtx)
}

func ensureDefaultSource(st *store.Store, cfg config.Config) {
	_ = st.Update(func(d *store.Data) error {
		if len(d.Sources) > 0 {
			return nil
		}
		id := store.NewID()
		d.Sources[id] = &store.Source{
			ID: id, Name: "braless 软件源", URL: cfg.DefaultSourceURL,
			Enabled: true, IntervalMinutes: cfg.PollIntervalMinutes, CreatedAt: time.Now(),
		}
		return nil
	})
}

// pollLoop drives per-user polling. Each account has its own refresh interval
// per source (store.User.SourceIntervals, falling back to the source default)
// and its own notification cursor (store.User.SourceNotifiedAt). A source is
// fetched from upstream at most once per tick — but only when at least one user
// is actually due for it — while every due user is then notified of the
// versions discovered since their own last notification. This keeps one user's
// schedule from leaking into another's.
func pollLoop(ctx context.Context, cfg config.Config, st *store.Store, src *source.Service, sign *signing.Manager, bark *notify.Sender) {
	const defaultInterval = 30

	effectiveInterval := func(userInterval, sourceInterval int) int {
		if userInterval > 0 {
			return userInterval
		}
		if sourceInterval > 0 {
			return sourceInterval
		}
		if cfg.PollIntervalMinutes > 0 {
			return cfg.PollIntervalMinutes
		}
		return defaultInterval
	}

	run := func() {
		now := time.Now()

		// Snapshot the work we must do. Only plain values are kept across the
		// Sync calls below, because the underlying store pointers are replaced
		// by every Update and must not be held.
		type duePair struct {
			uid   string
			srcID string
			since time.Time // user's last notification cursor for this source
		}
		var due []duePair
		needSync := map[string]bool{}

		st.View(func(d *store.Data) {
			for _, u := range d.Users {
				if u == nil {
					continue
				}
				for _, s := range d.Sources {
					if !s.Enabled {
						continue
					}
					if u.Role != "admin" && s.OwnerID != u.ID {
						continue
					}
					iv := effectiveInterval(u.SourceIntervals[s.ID], s.IntervalMinutes)
					since := u.SourceNotifiedAt[s.ID]
					if since.IsZero() || now.Sub(since) >= time.Duration(iv)*time.Minute {
						sinceCopy := since
						due = append(due, duePair{uid: u.ID, srcID: s.ID, since: sinceCopy})
						needSync[s.ID] = true
					}
				}
			}
		})

		// Fetch each needed source at most once this tick.
		for sid := range needSync {
			c, cancel := context.WithTimeout(ctx, 5*time.Minute)
			res, err := src.Sync(c, sid)
			if err != nil {
				log.Printf("poll: source %s: %v", sid, err)
				cancel()
				continue
			}
			if res.Changed {
				log.Printf("poll: source %s changed, %d new version(s)", sid, res.NewVersions)
			}
			cancel()
		}

		// Notify each due user of versions they have not been told about yet.
		// Group SourceNotifiedAt writes per user to limit store churn.
		userTouch := map[string][]string{}
		for _, p := range due {
			userTouch[p.uid] = append(userTouch[p.uid], p.srcID)
			if p.since.IsZero() {
				// First contact: establish the baseline so we never flood the
				// device with the entire existing catalogue.
				continue
			}
			ups := bark.UserUpdatesSince(p.srcID, p.since)
			if len(ups) == 0 {
				continue
			}
			if n := bark.NotifyUpdates(ctx, ups, p.uid); n > 0 {
				log.Printf("poll: sent %d bark notification(s) to user %s for source %s", n, p.uid, p.srcID)
			}
		}

		// Advance every touched user's notification cursor to now.
		if len(userTouch) > 0 {
			_ = st.Update(func(d *store.Data) error {
				for uid, srcs := range userTouch {
					u := d.Users[uid]
					if u == nil {
						continue
					}
					if u.SourceNotifiedAt == nil {
						u.SourceNotifiedAt = map[string]time.Time{}
					}
					for _, sid := range srcs {
						u.SourceNotifiedAt[sid] = now
					}
				}
				return nil
			})
		}

		// Idempotent: also picks up configs added since the last check.
		n, err := sign.EnqueuePending()
		if err != nil {
			log.Printf("poll: enqueue: %v", err)
		} else if n > 0 {
			log.Printf("poll: created %d signing job(s)", n)
		}
	}
	run() // reconcile once at startup (every user is due the first time)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
