package playground

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

type ReadyzServer struct {
	checker  NetworkReadyChecker
	manifest *Manifest
	port     int
	server   *http.Server
	mu       sync.RWMutex
}

type ReadyzResponse struct {
	Ready bool   `json:"ready"`
	Error string `json:"error,omitempty"`
}

func NewReadyzServer(checker NetworkReadyChecker, manifest *Manifest, port int) *ReadyzServer {
	return &ReadyzServer{
		checker:  checker,
		manifest: manifest,
		port:     port,
	}
}

func (s *ReadyzServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", s.handleReadyz)

	s.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
		Handler: mux,
	}

	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("Readyz server error: %v\n", err)
		}
	}()

	return nil
}

func (s *ReadyzServer) Stop() error {
	if s.server != nil {
		return s.server.Shutdown(context.Background())
	}
	return nil
}

func (s *ReadyzServer) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	ready, err := s.checker.IsNetworkReady(ctx, s.manifest)

	response := ReadyzResponse{
		Ready: ready,
	}

	if err != nil {
		response.Error = err.Error()
	}

	w.Header().Set("Content-Type", "application/json")

	if ready {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	json.NewEncoder(w).Encode(response)
}

func (s *ReadyzServer) Port() int {
	return s.port
}
