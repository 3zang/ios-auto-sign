package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
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

// pollLoop honours each source's own interval_minutes (the old implementation
// used one global ticker for everything). Every check drives notifications and
// job creation from the sync results, exactly like the manual endpoints.
func pollLoop(ctx context.Context, cfg config.Config, st *store.Store, src *source.Service, sign *signing.Manager, bark *notify.Sender) {
	run := func(all bool) {
		now := time.Now()
		var ids []string
		st.View(func(d *store.Data) {
			for id, s := range d.Sources {
				if !s.Enabled {
					continue
				}
				if all {
					ids = append(ids, id)
					continue
				}
				interval := s.IntervalMinutes
				if interval < 1 {
					interval = cfg.PollIntervalMinutes
				}
				if interval < 1 {
					interval = 30
				}
				if s.LastCheckedAt.IsZero() || now.Sub(s.LastCheckedAt) >= time.Duration(interval)*time.Minute {
					ids = append(ids, id)
				}
			}
		})
		sort.Strings(ids)
		for _, id := range ids {
			c, cancel := context.WithTimeout(ctx, 5*time.Minute)
			res, err := src.Sync(c, id)
			if err != nil {
				log.Printf("poll: source %s: %v", id, err)
				cancel()
				continue
			}
			if res.Changed {
				log.Printf("poll: source %s changed, %d new version(s)", id, res.NewVersions)
			}
			if n := bark.NotifyUpdates(c, res.Updates); n > 0 {
				log.Printf("poll: source %s sent %d bark notification(s)", id, n)
			}
			cancel()
		}
		// Idempotent: also picks up configs added since the last check.
		n, err := sign.EnqueuePending()
		if err != nil {
			log.Printf("poll: enqueue: %v", err)
		} else if n > 0 {
			log.Printf("poll: created %d signing job(s)", n)
		}
	}
	run(true) // reconcile everything once at startup
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run(false)
		}
	}
}
