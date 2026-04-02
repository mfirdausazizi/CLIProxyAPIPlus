package openai

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

func TestWriteResponsesSSEChunkPairsEventAndData(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()

	// Simulate how the translator returns event: and data: as separate chunks.
	writeResponsesSSEChunk(recorder, []byte("event: response.created"))
	writeResponsesSSEChunk(recorder, []byte("data: {\"type\":\"response.created\"}"))

	body := recorder.Body.String()

	// event: and data: must be in the same SSE block (single \n between them,
	// double \n only after the data: line to mark end of event).
	expected := "\nevent: response.created\ndata: {\"type\":\"response.created\"}\n\n"
	if body != expected {
		t.Fatalf("SSE framing broken: event: and data: must be paired in the same block.\nGot:  %q\nWant: %q", body, expected)
	}
}

func TestWriteResponsesSSEChunkEventWithTrailingNewline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()

	// If the upstream sends event: with a trailing \n, we must not produce \n\n
	// which would prematurely terminate the SSE event block.
	writeResponsesSSEChunk(recorder, []byte("event: response.created\n"))
	writeResponsesSSEChunk(recorder, []byte("data: {\"type\":\"response.created\"}"))

	body := recorder.Body.String()
	expected := "\nevent: response.created\ndata: {\"type\":\"response.created\"}\n\n"
	if body != expected {
		t.Fatalf("trailing newline on event: chunk broke SSE framing.\nGot:  %q\nWant: %q", body, expected)
	}
}

func TestWriteResponsesSSEChunkDataOnlySuffix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()

	writeResponsesSSEChunk(recorder, []byte("data: {\"hello\":true}"))

	body := recorder.Body.String()
	expected := "data: {\"hello\":true}\n\n"
	if body != expected {
		t.Fatalf("unexpected output.\nGot:  %q\nWant: %q", body, expected)
	}
}

func TestForwardResponsesStreamSeparatesDataOnlySSEChunks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	h := NewOpenAIResponsesAPIHandler(base)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		t.Fatalf("expected gin writer to implement http.Flusher")
	}

	data := make(chan []byte, 2)
	errs := make(chan *interfaces.ErrorMessage)
	data <- []byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"arguments\":\"{}\"}}")
	data <- []byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-1\",\"output\":[]}}")
	close(data)
	close(errs)

	h.forwardResponsesStream(c, flusher, func(error) {}, data, errs)
	body := recorder.Body.String()
	parts := strings.Split(strings.TrimSpace(body), "\n\n")
	if len(parts) != 2 {
		t.Fatalf("expected 2 SSE events, got %d. Body: %q", len(parts), body)
	}

	expectedPart1 := "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"arguments\":\"{}\"}}"
	if parts[0] != expectedPart1 {
		t.Errorf("unexpected first event.\nGot: %q\nWant: %q", parts[0], expectedPart1)
	}

	expectedPart2 := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-1\",\"output\":[]}}"
	if parts[1] != expectedPart2 {
		t.Errorf("unexpected second event.\nGot: %q\nWant: %q", parts[1], expectedPart2)
	}
}
