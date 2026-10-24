// Command ailimits-relay serves the AI Limits relay HTTP API.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/SecondBrainProd/ai-limits/relay/api"
	"github.com/SecondBrainProd/ai-limits/relay/store"
)

// healthURL derives the local health-check URL from a listen address.
func healthURL(addr string) (string, error) {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	if port == "" {
		return "", fmt.Errorf("no port in %q", addr)
	}
	return "http://" + net.JoinHostPort("127.0.0.1", port) + "/healthz", nil
}

func healthcheck(addr string) int {
	url, err := healthURL(addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck: status", resp.StatusCode)
		return 1
	}
	return 0
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	dbPath := flag.String("db", "/data/ailimits.db", "SQLite database path")
	check := flag.Bool("healthcheck", false, "GET /healthz on the local -addr port and exit 0 if it returns 200")
	flag.Parse()

	if *check {
		os.Exit(healthcheck(*addr))
	}

	st, err := store.Open(*dbPath, time.Now)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cleanup := func() {
		if n, err := st.Cleanup(ctx); err != nil {
			log.Printf("cleanup: %v", err)
		} else if n > 0 {
			log.Printf("cleanup: removed %d rows", n)
		}
	}
	var bg sync.WaitGroup
	bg.Add(1)
	go func() {
		defer bg.Done()
		cleanup() // once at startup, then hourly
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				cleanup()
			}
		}
	}()

	srv := &http.Server{
		Addr:              *addr,
		Handler:           api.New(st, api.NewLimiter(time.Now)),
		MaxHeaderBytes:    8 << 10,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(sctx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}()
	log.Printf("listening on %s", *addr)
	serveFailed := false
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("serve: %v", err)
		serveFailed = true
		stop()
	}
	// Wait for in-flight requests and the cleanup loop before the deferred
	// store.Close runs.
	<-shutdownDone
	bg.Wait()
	if serveFailed {
		st.Close()
		os.Exit(1)
	}
}
