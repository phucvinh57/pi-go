package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// streamClient has no overall timeout: a response may stream for minutes.
// Cancellation comes from the request context.
var streamClient = &http.Client{}

// maxSSELine bounds a single SSE line. Reasoning payloads can be large.
const maxSSELine = 16 << 20

// postStream POSTs body as JSON and returns the open response. A non-2xx
// status becomes an *httpError carrying a snippet of the body.
func postStream(ctx context.Context, url string, headers map[string]string, body any) (*http.Response, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("invalid request URL %q: %w", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, &httpError{Status: resp.StatusCode, Body: strings.TrimSpace(string(snippet))}
	}
	return resp, nil
}

// readSSE calls fn for every server-sent event in r, with the event name (""
// if none) and the data lines joined by newlines. It stops at the first error
// from fn, or at EOF.
func readSSE(r io.Reader, fn func(event, data string) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxSSELine)

	var event string
	var data []string
	flush := func() error {
		defer func() { event, data = "", nil }()
		if len(data) == 0 {
			return nil
		}
		return fn(event, strings.Join(data, "\n"))
	}

	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if err := flush(); err != nil {
				return err
			}
		case strings.HasPrefix(line, ":"):
			// comment / keep-alive
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return flush()
}
