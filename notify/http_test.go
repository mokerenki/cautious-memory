package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// This file covers HTTP-layer error classification for doJSON.
//
// Why test separately: each channel adapter handles only its own platform business error codes (DingTalk errcode,
// Feishu code, Telegram ok field), while the **HTTP layer** classification is done uniformly by doJSON.
// They are two independent lines of defense. Without this, a 503-returning intermediate gateway would be treated as
// permanent failure and retries abandoned; while a 403 would be treated as retryable, wasting three backoffs.

func replyServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDoJSONClassifiesHTTPStatus(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		permanent bool
	}{
		{"200 success is not an error", 200, false},
		{"429 rate limit retryable", 429, false},
		{"408 request timeout retryable", 408, false},
		{"500 server error retryable", 500, false},
		{"502 gateway error retryable", 502, false},
		{"503 service unavailable retryable", 503, false},
		{"400 param error permanent failure", 400, true},
		{"401 auth failure permanent failure", 401, true},
		{"403 forbidden permanent failure", 403, true},
		{"404 not found permanent failure", 404, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := replyServer(t, tc.status, `{"detail":"upstream says no"}`)
			_, err := doJSON(context.Background(), "GET", srv.URL, nil, nil)
			if tc.status < 300 {
				if err != nil {
					t.Fatalf("2xx should not error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("non-2xx should error")
			}
			if got := IsPermanent(err); got != tc.permanent {
				t.Fatalf("HTTP %d permanent determination wrong: expected %v got %v (%v)",
					tc.status, tc.permanent, got, err)
			}
			// The status code must appear in the error, otherwise the user can't tell whether they misconfigured or the other end is down.
			// Assert the number rather than Go's English StatusText: this package's messages are English (consistent with the rest of the project);
			// numbers are language-independent and stably assertable.
			if !strings.Contains(err.Error(), strconv.Itoa(tc.status)) {
				t.Errorf("error message should carry HTTP status code %d, got %v", tc.status, err)
			}
		})
	}
}

// TestDoJSONIncludesResponseSnippet covers the snippet: the error explanation returned by the other end must be brought back,
// otherwise the user only knows "it failed", not why the other end rejected.
func TestDoJSONIncludesResponseSnippet(t *testing.T) {
	srv := replyServer(t, 400, `{"error":"invalid webhook token"}`)
	_, err := doJSON(context.Background(), "GET", srv.URL, nil, nil)
	if err == nil {
		t.Fatal("should error")
	}
	if !strings.Contains(err.Error(), "invalid webhook token") {
		t.Errorf("error message should carry the other end's explanation, got %v", err)
	}
}

// TestDoJSONSnippetIsSingleLineAndBounded constrains the snippet's shape:
// the other end's response goes verbatim into the last_error column and frontend table; multi-line / oversized breaks layout and payload.
func TestDoJSONSnippetIsSingleLineAndBounded(t *testing.T) {
	// Response with newlines, tabs, and 5000 chars of oversized content.
	long := strings.Repeat("x", 5000)
	srv := replyServer(t, 500, "line1\nline2\r\n\tline3 "+long)
	_, err := doJSON(context.Background(), "GET", srv.URL, nil, nil)
	if err == nil {
		t.Fatal("should error")
	}
	msg := err.Error()
	if strings.ContainsAny(msg, "\r\n\t") {
		t.Errorf("error message should be compressed to a single line, got %q", msg)
	}
	// Snippet cap 200 chars + fixed prefix; total must be far below the original response.
	if len(msg) > 400 {
		t.Errorf("error message too long (%d bytes), should be snippet-truncated: %q", len(msg), msg)
	}
}

// TestDoJSONRejectsOversizedResponse confirms reading has a cap: when the other end abnormally returns oversized content,
// the entire response must not be read into memory (each entry in delivery history stores one last_error).
func TestDoJSONRejectsOversizedResponse(t *testing.T) {
	huge := strings.Repeat("A", 1<<20) // 1 MiB
	srv := replyServer(t, 400, huge)
	_, err := doJSON(context.Background(), "GET", srv.URL, nil, nil)
	if err == nil {
		t.Fatal("should error")
	}
	if len(err.Error()) > 400 {
		t.Errorf("oversized response should be length-capped and truncated read, error message length %d", len(err.Error()))
	}
}
