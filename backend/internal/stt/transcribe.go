// Package stt is the cloud-fallback speech-to-text proxy. When on-device
// sherpa-onnx is unavailable or low-confidence, the app POSTs the recorded
// audio here; pocketd forwards it to Groq Whisper Large v3 Turbo (cheap and
// fast, $0.04/hr) and returns the transcript.
//
// Keeping the cloud API key server-side avoids shipping it in the APK and
// lets us swap providers without an app update.
package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
)

type Transcriber struct {
	apiKey  string // Groq API key (POCKET_GROQ_API_KEY)
	model   string // default "whisper-large-v3-turbo"
	baseURL string // default "https://api.groq.com/openai/v1"
	client  *http.Client
}

func NewTranscriber(apiKey, model, baseURL string) *Transcriber {
	if model == "" {
		model = "whisper-large-v3-turbo"
	}
	if baseURL == "" {
		baseURL = "https://api.groq.com/openai/v1"
	}
	return &Transcriber{apiKey: apiKey, model: model, baseURL: baseURL, client: http.DefaultClient}
}

type Result struct {
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
	CostCents  float64 `json:"costCents,omitempty"`
}

// Transcribe forwards audio bytes (WAV/MP3/m4a) to the cloud Whisper API.
func (t *Transcriber) Transcribe(ctx context.Context, audio []byte, filename string) (*Result, error) {
	if t.apiKey == "" {
		return nil, fmt.Errorf("stt cloud key not configured (POCKET_GROQ_API_KEY)")
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(audio); err != nil {
		return nil, err
	}
	_ = w.WriteField("model", t.model)
	_ = w.WriteField("response_format", "json")
	w.Close()

	req, err := http.NewRequestWithContext(ctx, "POST", t.baseURL+"/audio/transcriptions", &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("groq stt %d: %s", resp.StatusCode, string(body))
	}
	var apiResp struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, err
	}
	return &Result{Text: apiResp.Text, Confidence: 0.95}, nil
}
