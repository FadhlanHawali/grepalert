// Command grepalert-agent tails log files and sends webhook alerts when lines
// match rules. Rules are hot-reloaded from disk or pushed via its HTTP API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fadhlanhawali/grepalert/internal/agent"
	"github.com/fadhlanhawali/grepalert/internal/rules"
)

func main() {
	configPath := flag.String("config", "agent.yaml", "path to agent config")
	check := flag.String("check", "", "validate a rules file and exit")
	version := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *version {
		fmt.Println("grepalert-agent", agent.Version)
		return
	}
	if *check != "" {
		raw, err := os.ReadFile(*check)
		if err == nil {
			var c *rules.Compiled
			if c, err = rules.Parse(raw); err == nil {
				fmt.Printf("OK: %d active rules, %d webhooks, hash %s\n", len(c.Rules), len(c.Webhooks), c.Hash)
				return
			}
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg, err := agent.LoadConfig(*configPath)
	if err != nil {
		log.Error("load config", "err", err)
		os.Exit(1)
	}
	if cfg.Token == "" {
		log.Warn("no token configured: the agent API is unauthenticated")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a := agent.New(cfg, log)
	srv := a.Server()
	go func() {
		cfg := a.Config()
		log.Info("agent API listening", "addr", cfg.Listen, "name", cfg.Name, "version", agent.Version)
		var err error
		if cfg.TLSCert != "" {
			err = srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("API server", "err", err)
			stop()
		}
	}()

	if err := a.Run(ctx); err != nil {
		log.Error("agent", "err", err)
		os.Exit(1)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)
	log.Info("stopped")
}
