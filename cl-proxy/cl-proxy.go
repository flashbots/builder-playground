package clproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/flashbots/mev-boost-relay/common"
	"github.com/sirupsen/logrus"
)

type Config struct {
	LogOutput io.Writer
	Port      uint64
	Primary   string
	Secondary string
}

func DefaultConfig() *Config {
	return &Config{
		LogOutput: os.Stdout,
		Port:      5656,
	}
}

type ClProxy struct {
	config *Config
	log    *logrus.Entry
	server *http.Server
}

func New(config *Config) (*ClProxy, error) {
	log := common.LogSetup(false, "info")
	log.Logger.SetOutput(config.LogOutput)

	proxy := &ClProxy{
		config: config,
		log:    log,
	}

	return proxy, nil
}

// Run starts the HTTP server
func (s *ClProxy) Run() error {
	mux := http.NewServeMux()
	s.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.config.Port),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		Handler:      mux,
	}

	mux.HandleFunc("/", s.handleRequest)

	s.log.Infof("Starting server on port %d", s.config.Port)
	s.log.Infof("Primary: %s", s.config.Primary)
	s.log.Infof("Secondary: %s", s.config.Secondary)

	if err := s.server.ListenAndServe(); err != http.ErrServerClosed {
		return fmt.Errorf("server error: %v", err)
	}
	return nil
}

// Close gracefully shuts down the server
func (s *ClProxy) Close() error {
	s.log.Info("Shutting down server...")

	// Create a context with timeout for shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Attempt graceful shutdown
	if err := s.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("server shutdown error: %v", err)
	}

	return nil
}

type jsonrpcMessage struct {
	Version string            `json:"jsonrpc,omitempty"`
	ID      json.RawMessage   `json:"id,omitempty"`
	Method  string            `json:"method,omitempty"`
	Params  []json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage   `json:"result,omitempty"`
}

func (s *ClProxy) handleRequest(w http.ResponseWriter, r *http.Request) {
	// Only accept POST requests
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Multiplex all the request to both primary and secondary but omit the
	// block building requests (this is, remove 'params' field from FCU and omit get payload).
	// There are two reasons for this:
	// - The secondary builder does not use the Engine API to build blocks but the relayer so these requests are not necessary.
	// - The CL->EL setup is not configured anyway to handle two block builders throught the Engine API.
	// Note that we still have to relay this request to the primary EL node since we need
	// to have a fallback node in the CL.
	var jsonRPCRequest jsonrpcMessage
	if err := json.Unmarshal(data, &jsonRPCRequest); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	s.log.Info(fmt.Sprintf("Received request: method=%s", jsonRPCRequest.Method))

	// proxy to primary and consider its response as the final response to send back to the CL
	resp, err := s.proxy(s.config.Primary, r, data)
	if err != nil {
		s.log.Errorf("Error multiplexing to primary: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		s.log.Errorf("Error reading response from primary: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(respData)

	if s.config.Secondary == "" {
		return
	}

	if strings.HasPrefix(jsonRPCRequest.Method, "engine_getPayload") {
		// the only request we do not send since the secondary builder does not have the payload id
		// and it will always fail
		return
	}

	if strings.HasPrefix(jsonRPCRequest.Method, "engine_forkchoiceUpdated") {
		// set to nil the second parameter of the forkchoiceUpdated call
		if len(jsonRPCRequest.Params) == 1 {
			// not expected
			s.log.Warn("ForkchoiceUpdated call with only one parameter")
		} else {
			jsonRPCRequest.Params[1] = nil

			data, err = json.Marshal(jsonRPCRequest)
			if err != nil {
				s.log.Errorf("Error marshalling forkchoiceUpdated request: %v", err)
				return
			}
		}
	}

	// proxy to secondary
	s.log.Info(fmt.Sprintf("Multiplexing request to secondary: method=%s", jsonRPCRequest.Method))
	if _, err := s.proxy(s.config.Secondary, r, data); err != nil {
		s.log.Errorf("Error multiplexing to secondary: %v", err)
	}
}

func (s *ClProxy) proxy(dst string, r *http.Request, data []byte) (*http.Response, error) {
	// Create a new request
	req, err := http.NewRequest(http.MethodPost, dst, bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}

	// Copy headers. It is important since we have to copy
	// the JWT header from the CL
	req.Header = r.Header

	// Perform the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	return resp, nil
}
