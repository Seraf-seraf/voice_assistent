package stt

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/service/audio"
)

func TestHTTPClientTranscribe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", request.Method)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q", got)
		}
		file := readMultipartRequest(t, request)
		if !strings.HasPrefix(string(file), "RIFF") || !strings.Contains(string(file[:44]), "WAVE") {
			t.Errorf("uploaded file is not WAV: %q", file[:min(12, len(file))])
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"text":"Привет"}`)
	}))
	defer server.Close()
	client := newTestClient(t, HTTPOptions{
		Endpoint: server.URL, Timeout: time.Second, MaxResponseBytes: 1024, APIKey: "secret",
	})

	transcript, err := client.Transcribe(context.Background(), testUtterance())
	if err != nil {
		t.Fatalf("Transcribe() error: %v", err)
	}
	if transcript.Text != "Привет" || transcript.Duration <= 0 {
		t.Fatalf("Transcript = %+v", transcript)
	}
}

func TestHTTPClientReturnsServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(response, `{"error":"модель не загружена"}`)
	}))
	defer server.Close()
	client := newTestClient(t, HTTPOptions{Endpoint: server.URL, Timeout: time.Second, MaxResponseBytes: 1024})

	_, err := client.Transcribe(context.Background(), testUtterance())
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("Transcribe() error = %v, want HTTPError 503", err)
	}
}

func TestHTTPClientLimitsResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, strings.Repeat("x", 33))
	}))
	defer server.Close()
	client := newTestClient(t, HTTPOptions{Endpoint: server.URL, Timeout: time.Second, MaxResponseBytes: 32})

	_, err := client.Transcribe(context.Background(), testUtterance())
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("Transcribe() error = %v, want ErrResponseTooLarge", err)
	}
}

func TestHTTPClientHonorsTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"text":"late"}`)
	}))
	defer server.Close()
	client := newTestClient(t, HTTPOptions{Endpoint: server.URL, Timeout: 10 * time.Millisecond, MaxResponseBytes: 1024})

	_, err := client.Transcribe(context.Background(), testUtterance())
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("Transcribe() error = %v, want timeout", err)
	}
}

func TestHTTPClientValidatesUtterance(t *testing.T) {
	client := newTestClient(t, HTTPOptions{
		Endpoint: "http://127.0.0.1/inference", Timeout: time.Second, MaxResponseBytes: 1024,
	})
	_, err := client.Transcribe(context.Background(), audio.Utterance{})
	if err == nil || !strings.Contains(err.Error(), "формат аудио") {
		t.Fatalf("Transcribe() error = %v, want format error", err)
	}
}

func readMultipartRequest(t *testing.T, request *http.Request) []byte {
	t.Helper()
	if err := request.ParseMultipartForm(2 << 20); err != nil {
		t.Fatalf("ParseMultipartForm() error: %v", err)
	}
	if request.FormValue("response_format") != "json" || request.FormValue("temperature") != "0.0" {
		t.Fatalf("unexpected form values: %+v", request.Form)
	}
	file, _, err := request.FormFile("file")
	if err != nil {
		t.Fatalf("FormFile() error: %v", err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("ReadAll() error: %v", err)
	}
	return data
}

func newTestClient(t *testing.T, options HTTPOptions) *HTTPClient {
	t.Helper()
	client, err := NewHTTPClient(options)
	if err != nil {
		t.Fatalf("NewHTTPClient() error: %v", err)
	}
	return client
}

func testUtterance() audio.Utterance {
	format := audio.Format{SampleRate: 16000, Channels: 1, FrameDuration: 20 * time.Millisecond}
	return audio.Utterance{ID: 1, Samples: make([]int16, format.SamplesPerFrame()), Format: format}
}
