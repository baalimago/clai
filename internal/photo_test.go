package internal

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestQuery(t *testing.T) {
	ctx := context.Background()
	API_KEY := "test_api_key"
	text := []string{"test prompt"}

	mockResponse := ImageResponses{
		Created: 1,
		Data: []ImageResponse{
			{
				RevisedPrompt: "test revised prompt",
				URL:           "http://example.com/test.jpg",
				B64_JSON:      "base64encodedstring",
			},
		},
	}
	mockResponseBody, _ := json.Marshal(mockResponse)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(mockResponseBody)
	}))
	defer server.Close()

	pq := PhotoQuerier{
		Model:        "test-model",
		PhotoDir:     "test-dir",
		PhotoPrefix:  "test-prefix",
		PromptFormat: "%s",
		url:          server.URL,
		raw:          true,
		client:       server.Client(),
	}

	response, err := pq.query(ctx, API_KEY, text)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if len(response.Data) != 1 {
		t.Errorf("Expected 1 image response, got %d", len(response.Data))
	}

	expectedURL := "http://example.com/test.jpg"
	if response.Data[0].URL != expectedURL {
		t.Errorf("Expected URL to be %s, got %s", expectedURL, response.Data[0].URL)
	}
}

func TestSaveImage(t *testing.T) {
	// Setup
	ctx := context.Background()
	tempDir := t.TempDir()
	defer os.RemoveAll(tempDir)

	pq := PhotoQuerier{
		PhotoDir:    tempDir,
		PhotoPrefix: "test",
	}

	imgData := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/w8AAwAB/aurfgcAAAAASUVORK5CYII="
	imgResp := ImageResponse{
		B64_JSON: imgData,
	}

	err := pq.saveImage(ctx, imgResp)
	if err != nil {
		t.Errorf("saveImage() error = %v, wantErr false", err)
	}
	files, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("Failed to read temp directory: %v", err)
	}
	if len(files) != 1 {
		t.Errorf("Expected 1 file in temp directory, got %d", len(files))
	}
	filePath := filepath.Join(tempDir, files[0].Name())
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read saved image file: %v", err)
	}

	decodedImgData, _ := base64.StdEncoding.DecodeString(imgData)
	if string(content) != string(decodedImgData) {
		t.Errorf("Saved image content does not match original image data")
	}
}
