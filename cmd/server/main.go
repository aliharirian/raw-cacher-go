package main

import (
	"context"
	"github.com/yourname/raw-cacher-go/internal/metrics"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yourname/raw-cacher-go/internal/config"
	"github.com/yourname/raw-cacher-go/internal/server"
	"github.com/yourname/raw-cacher-go/internal/storage"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	ctx := context.Background()
	store, err := storage.NewStore(ctx, cfg.MinioEndpoint, cfg.MinioAccess, cfg.MinioSecret, cfg.MinioBucket)
	if err != nil {
		log.Fatalf("minio error: %v", err)
	}

	mux := http.NewServeMux()

	srv := server.NewServer(store, cfg.TTLDefault, cfg.TTL404, cfg.ServeIf)
	mux.Handle("/", srv)

	httpSrv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 0,
	}

	health := &metrics.HealthHandler{Store: store}
	mux.Handle("/healthz", health.HealthCheckHandler())

	go func() {
		log.Printf("raw-cacher-go listening on %s", cfg.ListenAddr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctxShutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctxShutdown)
	log.Println("server stopped")
}
