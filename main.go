package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	path := flag.String("config", "config.json", "configuration file")
	flag.Parse()
	f, e := os.Open(*path)
	if e != nil {
		log.Fatal(e)
	}
	defer f.Close()
	var cfg Config
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(&cfg); e != nil {
		log.Fatal(e)
	}
	handler, e := newGateway(cfg, []byte(os.Getenv("JWT_SECRET")))
	if e != nil {
		log.Fatal(e)
	}
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:8080"
	}
	server := &http.Server{Addr: cfg.Listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	log.Printf("gateway listening on %s", cfg.Listen)
	if e = server.ListenAndServe(); e != http.ErrServerClosed {
		log.Fatal(e)
	}
}
