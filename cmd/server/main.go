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

	// Every newly discovered version produces one Bark message naming the app
	// and carrying that app's icon. Delivery failures are logged and never
	// fail the sync itself.
	bark := notify.New(st, client)
	sourceSvc.SetUpdateHook(func(ctx context.Context, up source.AppUpdate) {
		if err := bark.NotifyUpdate(ctx, notify.Update{
			AppName: up.AppName, BundleID: up.BundleID, AppID: up.AppID,
			Version: up.Version, SourceName: up.SourceName, IconURL: up.IconURL,
		}); err != nil {
			log.Printf("bark notification: %v", err)
		}
	})

	ensureDefaultSource(st, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signMgr.Start(ctx)
	go pollLoop(ctx, cfg, sourceSvc, signMgr)

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

func pollLoop(ctx context.Context, cfg config.Config, src *source.Service, sign *signing.Manager) {
	run := func() {
		c, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		results, errs := src.SyncAll(c)
		for _, e := range errs {
			log.Printf("poll: %v", e)
		}
		for _, r := range results {
			if r.Changed {
				log.Printf("poll: source %s changed, %d new version(s)", r.SourceID, r.NewVersions)
			}
		}
		n, err := sign.EnqueuePending()
		if err != nil {
			log.Printf("poll: enqueue: %v", err)
		} else if n > 0 {
			log.Printf("poll: created %d signing job(s)", n)
		}
	}
	run()
	ticker := time.NewTicker(time.Duration(cfg.PollIntervalMinutes) * time.Minute)
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
