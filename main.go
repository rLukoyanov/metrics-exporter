package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"metrics-generator/internal/pushgateway"
	"metrics-generator/internal/remotewrite"
)

//go:embed all:web/dist
var webFS embed.FS

const (
	modePushgateway = "pushgateway"
	modeRemoteWrite = "remotewrite"
)

type server struct {
	pusher   *pushgateway.Pusher
	remote   *remotewrite.Pusher
	sendMode string
}

type pushResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

func main() {
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	sendMode := os.Getenv("SEND_MODE")
	if sendMode == "" {
		sendMode = modePushgateway
	}

	pgCfg := pushgateway.ConfigFromEnv()
	pg := pushgateway.New(pgCfg)

	rwCfg := remotewrite.ConfigFromEnv()
	rw := remotewrite.New(rwCfg)

	log.Printf("default send mode: %s", sendMode)
	log.Printf("pushgateway url: %s", pgCfg.URL)
	log.Printf("remote write url: %s (enabled: %v)", rwCfg.URL, rw.Enabled())

	srv := &server{pusher: pg, remote: rw, sendMode: sendMode}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/push", srv.handlePush)
	mux.HandleFunc("/api/config", srv.handleConfig)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/", srv.staticHandler())

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("listening on %s", addr)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server error: %v", err)
	}
}

func (s *server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"sendMode":       s.sendMode,
		"pushgatewayUrl": s.pusher.URL(),
		"remoteWriteUrl": s.remote.URL(),
		"remoteEnabled":  s.remote.Enabled(),
		"tokenPresent":   s.pusher.TokenPresent(),
	})
}

func (s *server) handlePush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, pushResponse{Message: "method not allowed"})
		return
	}

	var req pushgateway.Request
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, pushResponse{Message: "invalid request body: " + err.Error()})
		return
	}

	mode := req.Mode
	if mode == "" {
		mode = s.sendMode
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	var err error
	switch mode {
	case modePushgateway:
		err = s.pusher.Push(ctx, req)
	case modeRemoteWrite:
		if !s.remote.Enabled() {
			writeJSON(w, http.StatusBadRequest, pushResponse{Message: "remote write is not configured (REMOTE_WRITE_URL is empty)"})
			return
		}
		err = s.pushRemoteWrite(ctx, req)
	default:
		writeJSON(w, http.StatusBadRequest, pushResponse{Message: "unknown mode: " + mode})
		return
	}

	if err != nil {
		log.Printf("push failed (%s): %v", mode, err)
		writeJSON(w, http.StatusBadGateway, pushResponse{Message: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, pushResponse{OK: true, Message: "metric pushed successfully"})
}

func (s *server) pushRemoteWrite(ctx context.Context, req pushgateway.Request) error {
	families, err := pushgateway.BuildFamilies(req)
	if err != nil {
		return err
	}
	return s.remote.Push(ctx, families)
}

func (s *server) staticHandler() http.Handler {
	sub, err := fs.Sub(webFS, "web/dist")
	if err != nil {
		log.Fatalf("embed web assets: %v", err)
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(sub, path); err != nil {
			// SPA fallback
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
