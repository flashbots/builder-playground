package playground

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

type ReadyzServer struct {
	instances []*instance
	port      int
	server    *http.Server
	mu        sync.RWMutex
}

type ReadyzResponse struct {
	Ready bool   `json:"ready"`
	Error string `json:"error,omitempty"`
}

func NewReadyzServer(instances []*instance, port int) *ReadyzServer {
	return &ReadyzServer{
		instances: instances,
		port:      port,
	}
}

func (s *ReadyzServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/livez", s.handleLivez)
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

func (s *ReadyzServer) handleLivez(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK")) //nolint:errcheck
}

func (s *ReadyzServer) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ready, err := s.isReady()

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

	if err := json.NewEncoder(w).Encode(response); err != nil {
		fmt.Printf("Failed to encode readyz response: %v\n", err)
	}
}

func (s *ReadyzServer) isReady() (bool, error) {
	ctx := context.Background()
	for _, inst := range s.instances {
		if _, ok := inst.component.(ServiceReady); ok {
			elURL := fmt.Sprintf("http://localhost:%d", inst.service.MustGetPort("http").HostPort)
			ready, err := isChainProducingBlocks(ctx, elURL)
			if err != nil {
				return false, err
			}
			if !ready {
				return false, nil
			}
		}
	}
	return true, nil
}

func (s *ReadyzServer) Port() int {
	return s.port
}
