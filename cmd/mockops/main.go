// Command mockops runs the simulated operations estate that runbook actions
// call in the demo (http://localhost:8091 in docker-compose).
//
//	MOCKOPS_ADDR  listen address (default :8091)
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

	"github.com/undeluro/kardan-alerts/internal/mockops"
)

func main() {
	addr := os.Getenv("MOCKOPS_ADDR")
	if addr == "" {
		addr = ":8091"
	}
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		resp, err := (&http.Client{Timeout: 3 * time.Second}).Get("http://127.0.0.1" + addr + "/healthz")
		if err != nil || resp.StatusCode != http.StatusOK {
			fmt.Fprintln(os.Stderr, "unhealthy", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	srv := &http.Server{Addr: addr, Handler: mockops.New(log).Handler(), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Info("mockops listening", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("mockops", "err", err)
		os.Exit(1)
	}
}
