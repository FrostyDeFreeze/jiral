package main

import (
	"net/http"
	"os"
)

func main() {
	cfg, err := LoadConfig()
	if err != nil {
		logProcessError(os.Stderr, "config_load_failed", err)
		os.Exit(1)
	}
	srv, err := NewServer(cfg)
	if err != nil {
		logProcessError(os.Stderr, "server_init_failed", err)
		os.Exit(1)
	}
	srv.logInfo("listening", field("addr", cfg.Addr))
	if err := http.ListenAndServe(cfg.Addr, srv); err != nil {
		srv.logError("server_failed", field("err", err))
		os.Exit(1)
	}
}
