// Command fakegw runs the simulated SMS / voice provider with its phone
// simulator page (http://localhost:8090 in docker-compose).
//
//	KARDAN_URL     where to forward replies (default http://localhost:8080)
//	GATEWAY_TOKEN  shared secret with Kardan (optional)
//	GATEWAY_ADDR   listen address (default :8090)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/undeluro/kardan-alerts/internal/fakegw"
)

func main() {
	addr := env("GATEWAY_ADDR", ":8090") // not HTTP_ADDR: the shared image sets that for kardan
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		resp, err := (&http.Client{Timeout: 3 * time.Second}).Get("http://127.0.0.1" + addr + "/healthz")
		if err != nil || resp.StatusCode != http.StatusOK {
			fmt.Fprintln(os.Stderr, "unhealthy", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	gw := fakegw.New(env("KARDAN_URL", "http://localhost:8080"), os.Getenv("GATEWAY_TOKEN"), log)
	srv := &http.Server{Addr: addr, Handler: gw.Handler(), ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Info("fake SMS/voice gateway listening", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
