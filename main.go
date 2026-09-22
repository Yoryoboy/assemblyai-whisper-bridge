// Command assemblyai-whisper-bridge adapts the OpenAI Whisper
// transcription dialect to AssemblyAI Sync STT for local personal use.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"strings"
	"time"
)

const (
	defaultAddr         = "127.0.0.1:8787"
	defaultModel        = "universal-3-5-pro"
	defaultUpstreamURL  = "https://sync.assemblyai.com/transcribe"
	maxMemory           = 40 << 20 // keep typical requests in memory; larger ones may use temp files
	upstreamTimeout     = 30 * time.Second
	transcriptionsRoute = "/v1/audio/transcriptions"
)

// Config is environment-based so tests can point the bridge at a fake upstream.
type Config struct {
	Addr        string
	APIKey      string
	Model       string
	UpstreamURL string
}

// relevantEnvKeys is the minimal set the bridge configures via .env.
// Unknown keys are ignored so unrelated tool variables never interfere.
var relevantEnvKeys = map[string]bool{
	"ADDR":               true,
	"ASSEMBLYAI_API_KEY": true,
	"ASSEMBLYAI_MODEL":   true,
	"ASSEMBLYAI_URL":     true,
}

// loadDotEnv reads simple KEY=value lines from path without overriding
// variables already present in the process environment. Blank lines and
// lines starting with '#' are skipped. A missing file is not an error.
// Malformed lines fail loudly so credentials are never silently misconfigured.
func loadDotEnv(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	for i, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		eq := strings.IndexByte(trimmed, '=')
		if eq < 0 {
			return fmt.Errorf("invalid line %d in %s: missing '='", i+1, path)
		}
		key := strings.TrimSpace(trimmed[:eq])
		if !validEnvKey(key) {
			return fmt.Errorf("invalid line %d in %s: bad key %q", i+1, path, key)
		}
		if !relevantEnvKeys[key] {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, unquote(strings.TrimSpace(trimmed[eq+1:]))); err != nil {
			return fmt.Errorf("set %s from %s: %w", key, path, err)
		}
	}
	return nil
}

func validEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9' {
			continue
		}
		return false
	}
	return true
}

// unquote strips one layer of matching single or double quotes.
func unquote(v string) string {
	if len(v) >= 2 {
		if f, l := v[0], v[len(v)-1]; f == l && (f == '"' || f == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}

// configFromEnv loads .env from the working directory, then reads
// configuration with localhost/model defaults.
func configFromEnv() (Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return Config{}, err
	}
	return Config{
		Addr:        envOr("ADDR", defaultAddr),
		APIKey:      os.Getenv("ASSEMBLYAI_API_KEY"),
		Model:       envOr("ASSEMBLYAI_MODEL", defaultModel),
		UpstreamURL: envOr("ASSEMBLYAI_URL", defaultUpstreamURL),
	}, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Server forwards Whisper-dialect transcription requests to AssemblyAI.
type Server struct {
	cfg    Config
	client *http.Client
}

// NewServer builds a Server with a 30s upstream timeout.
func NewServer(cfg Config) *Server {
	return &Server{cfg: cfg, client: &http.Client{Timeout: upstreamTimeout}}
}

// Handler wires the only route the bridge serves.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(transcriptionsRoute, s.handleTranscription)
	return mux
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":%q}`, msg)
}

func (s *Server) handleTranscription(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := r.ParseMultipartForm(maxMemory); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart body")
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing file field")
		return
	}
	defer f.Close()
	audio, err := io.ReadAll(f)
	if err != nil || len(audio) == 0 {
		writeError(w, http.StatusBadRequest, "empty file field")
		return
	}

	text, err := s.transcribe(audio)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"text": text}); err != nil {
		log.Printf("encode response: %v", err)
	}
}

// transcribe forwards raw audio bytes as the upstream "audio" part.
func (s *Server) transcribe(audio []byte) (string, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="audio"; filename="audio.wav"`)
	h.Set("Content-Type", "audio/wav")
	part, err := w.CreatePart(h)
	if err != nil {
		return "", fmt.Errorf("upstream request failed")
	}
	if _, err := part.Write(audio); err != nil {
		return "", fmt.Errorf("upstream request failed")
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("upstream request failed")
	}

	req, err := http.NewRequest(http.MethodPost, s.cfg.UpstreamURL, &body)
	if err != nil {
		return "", fmt.Errorf("upstream request failed")
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-AAI-Model", s.cfg.Model)
	if s.cfg.APIKey != "" {
		req.Header.Set("Authorization", s.cfg.APIKey)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("upstream request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("upstream error: %s", resp.Status)
	}
	var decoded struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return "", fmt.Errorf("invalid upstream response")
	}
	if decoded.Text == "" {
		return "", fmt.Errorf("invalid upstream response")
	}
	return decoded.Text, nil
}

func main() {
	cfg, err := configFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	s := NewServer(cfg)
	log.Printf("listening on %s", s.cfg.Addr)
	if err := http.ListenAndServe(s.cfg.Addr, s.Handler()); err != nil {
		log.Fatal(err)
	}
}
