package main

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sahfiann/file-swap/internal/httpapi"
)

func main() {
	log.Printf("[SERVER] starting")
	addr := os.Getenv("PORT")
	if addr == "" {
		addr = "8080"
	}
	if !strings.Contains(addr, ":") {
		addr = ":" + addr
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           httpapi.NewMux(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		// SSE job streams stay open for the lifetime of long video encodes.
		WriteTimeout:   0,
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	log.Printf("[SERVER] listening addr=%s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("[SERVER] shutdown reason=%v", err)
	}
}
