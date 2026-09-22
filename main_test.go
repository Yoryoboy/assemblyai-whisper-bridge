package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testConfig(upstreamURL string) Config {
	return Config{
		Addr:        "127.0.0.1:0",
		APIKey:      "test-key",
		Model:       "universal-3-5-pro",
		UpstreamURL: upstreamURL,
	}
}

// clientRequest builds a Whisper-dialect multipart request with a file field.
func clientRequest(t *testing.T, target string, file []byte, withFile bool) *http.Request {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if withFile {
		fw, err := w.CreateFormFile("file", "audio.wav")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(file); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := w.WriteField("model", "whisper-1"); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, target, &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

func TestTranscriptionSuccessMapping(t *testing.T) {
	audio := []byte("fake-wav-bytes")
	var gotAuth, gotModel, gotField string
	var gotAudio []byte
	var gotContentType string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotModel = r.Header.Get("X-AAI-Model")
		f, header, err := r.FormFile("audio")
		if err != nil {
			t.Errorf("upstream missing audio field: %v", err)
			http.Error(w, "missing audio", http.StatusBadRequest)
			return
		}
		defer f.Close()
		gotField = header.Filename
		gotAudio, _ = io.ReadAll(f)
		gotContentType = header.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		// Extra fields must be dropped by the bridge.
		io.WriteString(w, `{"text":"hello world","words":[],"confidence":0.9}`)
	}))
	defer upstream.Close()

	s := NewServer(testConfig(upstream.URL))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, clientRequest(t, "/v1/audio/transcriptions", audio, true))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if len(decoded) != 1 || decoded["text"] != "hello world" {
		t.Fatalf("response = %v, want only {text:hello world}", decoded)
	}
	if gotAuth != "test-key" {
		t.Errorf("Authorization = %q, want test-key", gotAuth)
	}
	if gotModel != "universal-3-5-pro" {
		t.Errorf("X-AAI-Model = %q, want universal-3-5-pro", gotModel)
	}
	if !bytes.Equal(gotAudio, audio) {
		t.Errorf("upstream audio bytes mismatch")
	}
	if gotField != "audio.wav" {
		t.Errorf("upstream filename = %q, want audio.wav", gotField)
	}
	if gotContentType != "audio/wav" {
		t.Errorf("upstream part content type = %q, want audio/wav", gotContentType)
	}
}

func writeDotEnv(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// withUnsetEnv unsets keys for the test and restores their prior state after.
// Restoration uses t.Cleanup: a defer here would run when this helper
// returns, not when the test ends.
func withUnsetEnv(t *testing.T, keys ...string) {
	t.Helper()
	type saved struct {
		v  string
		ok bool
	}
	snap := make(map[string]saved, len(keys))
	for _, key := range keys {
		v, ok := os.LookupEnv(key)
		snap[key] = saved{v: v, ok: ok}
		_ = os.Unsetenv(key)
	}
	t.Cleanup(func() {
		for key, s := range snap {
			if s.ok {
				_ = os.Setenv(key, s.v)
			} else {
				_ = os.Unsetenv(key)
			}
		}
	})
}

var dotEnvKeys = []string{"ADDR", "ASSEMBLYAI_API_KEY", "ASSEMBLYAI_MODEL", "ASSEMBLYAI_URL"}

func TestLoadDotEnvAppliesCommentsBlanksAndEmpty(t *testing.T) {
	withUnsetEnv(t, dotEnvKeys...)
	path := writeDotEnv(t, "# comment\n\nASSEMBLYAI_API_KEY=test-dotenv-key\nASSEMBLYAI_MODEL=dotenv-model\nADDR=\n")
	if err := loadDotEnv(path); err != nil {
		t.Fatalf("loadDotEnv: %v", err)
	}
	if got := os.Getenv("ASSEMBLYAI_API_KEY"); got != "test-dotenv-key" {
		t.Errorf("ASSEMBLYAI_API_KEY = %q, want test-dotenv-key", got)
	}
	if got := os.Getenv("ASSEMBLYAI_MODEL"); got != "dotenv-model" {
		t.Errorf("ASSEMBLYAI_MODEL = %q, want dotenv-model", got)
	}
	if v, ok := os.LookupEnv("ADDR"); !ok || v != "" {
		t.Errorf("ADDR = %q, %v; want empty value present", v, ok)
	}
}

func TestLoadDotEnvPreservesProcessEnv(t *testing.T) {
	withUnsetEnv(t, dotEnvKeys...)
	t.Setenv("ASSEMBLYAI_API_KEY", "process-key")
	path := writeDotEnv(t, "ASSEMBLYAI_API_KEY=dotenv-key\nASSEMBLYAI_MODEL=dotenv-model\n")
	if err := loadDotEnv(path); err != nil {
		t.Fatalf("loadDotEnv: %v", err)
	}
	if got := os.Getenv("ASSEMBLYAI_API_KEY"); got != "process-key" {
		t.Errorf("ASSEMBLYAI_API_KEY = %q, want process-key", got)
	}
	if got := os.Getenv("ASSEMBLYAI_MODEL"); got != "dotenv-model" {
		t.Errorf("ASSEMBLYAI_MODEL = %q, want dotenv-model", got)
	}
}

func TestLoadDotEnvExplicitEmptyProcessEnvWins(t *testing.T) {
	withUnsetEnv(t, dotEnvKeys...)
	t.Setenv("ASSEMBLYAI_MODEL", "")
	path := writeDotEnv(t, "ASSEMBLYAI_MODEL=dotenv-model\n")
	if err := loadDotEnv(path); err != nil {
		t.Fatalf("loadDotEnv: %v", err)
	}
	if got, ok := os.LookupEnv("ASSEMBLYAI_MODEL"); !ok || got != "" {
		t.Errorf("ASSEMBLYAI_MODEL = %q, %v; want explicit empty process value preserved", got, ok)
	}
}

func TestLoadDotEnvQuotedAndUnknownIgnored(t *testing.T) {
	withUnsetEnv(t, append(dotEnvKeys, "SOME_TOOL_VAR")...)
	path := writeDotEnv(t, "ASSEMBLYAI_MODEL=\"quoted-model\"\nSOME_TOOL_VAR=ignored\n")
	if err := loadDotEnv(path); err != nil {
		t.Fatalf("loadDotEnv: %v", err)
	}
	if got := os.Getenv("ASSEMBLYAI_MODEL"); got != "quoted-model" {
		t.Errorf("ASSEMBLYAI_MODEL = %q, want quoted-model", got)
	}
	if v, ok := os.LookupEnv("SOME_TOOL_VAR"); ok || v != "" {
		t.Errorf("SOME_TOOL_VAR = %q, %v; want unknown valid key ignored", v, ok)
	}
}

func TestLoadDotEnvMissingFile(t *testing.T) {
	if err := loadDotEnv(filepath.Join(t.TempDir(), ".env")); err != nil {
		t.Fatalf("missing .env should not error, got %v", err)
	}
}

func TestLoadDotEnvMalformed(t *testing.T) {
	withUnsetEnv(t, dotEnvKeys...)
	if err := loadDotEnv(writeDotEnv(t, "ASSEMBLYAI_API_KEY\n")); err == nil {
		t.Fatal("line without '=' should error")
	}
	if err := loadDotEnv(writeDotEnv(t, "9BAD=x\n")); err == nil {
		t.Fatal("bad key name should error")
	}
}

func TestConfigFromEnvReadsDotEnvWithPrecedence(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("ASSEMBLYAI_API_KEY=dotenv-key\nASSEMBLYAI_MODEL=dotenv-model\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	withUnsetEnv(t, dotEnvKeys...)
	t.Setenv("ASSEMBLYAI_MODEL", "process-model")
	cfg, err := configFromEnv()
	if err != nil {
		t.Fatalf("configFromEnv: %v", err)
	}
	if cfg.APIKey != "dotenv-key" {
		t.Errorf("APIKey = %q, want dotenv-key", cfg.APIKey)
	}
	if cfg.Model != "process-model" {
		t.Errorf("Model = %q, want process-model", cfg.Model)
	}
}

func TestTranscriptionMissingFile(t *testing.T) {
	s := NewServer(testConfig("http://example.invalid"))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, clientRequest(t, "/v1/audio/transcriptions", nil, false))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestTranscriptionWrongMethod(t *testing.T) {
	s := NewServer(testConfig("http://example.invalid"))
	req := httptest.NewRequest(http.MethodGet, "/v1/audio/transcriptions", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestTranscriptionUpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "quota exceeded", http.StatusUnauthorized)
	}))
	defer upstream.Close()

	s := NewServer(testConfig(upstream.URL))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, clientRequest(t, "/v1/audio/transcriptions", []byte("audio"), true))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "upstream error") {
		t.Fatalf("body = %q, want upstream error mention", rec.Body.String())
	}
}

func TestTranscriptionUpstreamBadJSON(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `not json`)
	}))
	defer upstream.Close()

	s := NewServer(testConfig(upstream.URL))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, clientRequest(t, "/v1/audio/transcriptions", []byte("audio"), true))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}
