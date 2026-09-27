package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/youpy/go-wav"

	"github.com/Seraf-seraf/voice_assistent/internal/audio"
)

const wavChunkSamples = 4096

var ErrResponseTooLarge = errors.New("ответ STT превышает допустимый размер")

type HTTPError struct {
	StatusCode int
	Message    string
}

func (e *HTTPError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("STT вернул HTTP status %d", e.StatusCode)
	}
	return fmt.Sprintf("STT вернул HTTP status %d: %s", e.StatusCode, e.Message)
}

type HTTPOptions struct {
	Endpoint         string
	Timeout          time.Duration
	MaxResponseBytes int64
	APIKey           string
	HTTPClient       *http.Client
}

type HTTPClient struct {
	endpoint         string
	timeout          time.Duration
	maxResponseBytes int64
	apiKey           string
	httpClient       *http.Client
}

func NewHTTPClient(options HTTPOptions) (*HTTPClient, error) {
	if err := validateEndpoint(options.Endpoint); err != nil {
		return nil, err
	}
	if options.Timeout <= 0 {
		return nil, errors.New("время ожидания STT должно быть положительным")
	}
	if options.MaxResponseBytes <= 0 {
		return nil, errors.New("максимальный размер ответа STT должен быть положительным")
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	return &HTTPClient{
		endpoint: options.Endpoint, timeout: options.Timeout,
		maxResponseBytes: options.MaxResponseBytes, apiKey: options.APIKey, httpClient: client,
	}, nil
}

func (c *HTTPClient) Transcribe(ctx context.Context, utterance audio.Utterance) (Transcript, error) {
	if err := validateUtterance(utterance); err != nil {
		return Transcript{}, err
	}
	startedAt := time.Now()
	requestBody, contentType, err := buildRequestBody(utterance)
	if err != nil {
		return Transcript{}, err
	}

	requestContext, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, c.endpoint, requestBody)
	if err != nil {
		return Transcript{}, fmt.Errorf("создать запрос STT: %w", err)
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return Transcript{}, fmt.Errorf("выполнить запрос STT: %w", err)
	}
	defer response.Body.Close()
	body, err := readLimited(response.Body, c.maxResponseBytes)
	if err != nil {
		return Transcript{}, err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return Transcript{}, parseHTTPError(response.StatusCode, body)
	}
	if err := requireJSON(response.Header.Get("Content-Type")); err != nil {
		return Transcript{}, err
	}

	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Transcript{}, fmt.Errorf("прочитать ответ STT: %w", err)
	}
	return Transcript{Text: payload.Text, Duration: time.Since(startedAt)}, nil
}

func buildRequestBody(utterance audio.Utterance) (*bytes.Buffer, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", "utterance.wav")
	if err != nil {
		return nil, "", fmt.Errorf("создать поле WAV в многокомпонентном запросе: %w", err)
	}
	if err := encodeWAV(file, utterance); err != nil {
		return nil, "", err
	}
	if err := writer.WriteField("response_format", "json"); err != nil {
		return nil, "", fmt.Errorf("записать формат ответа STT: %w", err)
	}
	if err := writer.WriteField("temperature", "0.0"); err != nil {
		return nil, "", fmt.Errorf("записать параметр Temperature STT: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("закрыть многокомпонентный запрос STT: %w", err)
	}
	return &body, writer.FormDataContentType(), nil
}

func encodeWAV(output io.Writer, utterance audio.Utterance) error {
	writer := wav.NewWriter(
		output, uint32(len(utterance.Samples)), uint16(utterance.Format.Channels),
		uint32(utterance.Format.SampleRate), 16,
	)
	chunk := make([]wav.Sample, wavChunkSamples)
	for offset := 0; offset < len(utterance.Samples); {
		count := min(len(chunk), len(utterance.Samples)-offset)
		for index := 0; index < count; index++ {
			chunk[index].Values[0] = int(utterance.Samples[offset+index])
		}
		if err := writer.WriteSamples(chunk[:count]); err != nil {
			return fmt.Errorf("закодировать реплику в WAV: %w", err)
		}
		offset += count
	}
	return nil
}

func validateUtterance(utterance audio.Utterance) error {
	if err := utterance.Format.Validate(); err != nil {
		return fmt.Errorf("формат аудио: %w", err)
	}
	if utterance.Format.Channels != 1 {
		return errors.New("STT ожидает одноканальную реплику")
	}
	if len(utterance.Samples) == 0 {
		return errors.New("реплика STT не содержит аудиоотсчётов")
	}
	return nil
}

func validateEndpoint(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("разобрать адрес STT: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("адрес STT поддерживает только протоколы http и https")
	}
	if parsed.Host == "" {
		return errors.New("адрес STT должен содержать имя узла")
	}
	if parsed.User != nil {
		return errors.New("реквизиты доступа в адресе STT запрещены")
	}
	return nil
}

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, fmt.Errorf("прочитать ответ STT: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, ErrResponseTooLarge
	}
	return body, nil
}

func requireJSON(contentType string) error {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return fmt.Errorf("разобрать тип содержимого ответа STT: %w", err)
	}
	if mediaType != "application/json" {
		return fmt.Errorf("STT вернул неподдерживаемый тип содержимого %q", mediaType)
	}
	return nil
}

func parseHTTPError(statusCode int, body []byte) error {
	var payload struct {
		Error string `json:"error"`
	}
	message := ""
	if json.Unmarshal(body, &payload) == nil {
		message = strings.TrimSpace(payload.Error)
	}
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	if len(message) > 200 {
		message = message[:200]
	}
	return &HTTPError{StatusCode: statusCode, Message: message}
}
