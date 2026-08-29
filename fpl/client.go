package fpl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"
)

// BaseURL is the public FPL API root. No auth, no API key.
const BaseURL = "https://fantasy.premierleague.com/api/"

// The API blocks unusual clients, so present a normal browser UA.
const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

type Client struct {
	HTTP     *http.Client
	Attempts int
}

func NewClient() *Client {
	return &Client{
		HTTP:     &http.Client{Timeout: 30 * time.Second},
		Attempts: 4,
	}
}

// StatusError is a non-200 response from the API.
type StatusError struct {
	URL    string
	Status int
	Body   string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("GET %s: %d %s", e.URL, e.Status, e.Body)
}

// NotFound reports whether err is a 404. The API 404s for entries that do not
// exist and for picks of a gameweek that has not kicked off yet, both of which
// are expected conditions rather than failures.
func NotFound(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Status == http.StatusNotFound
}

// Get fetches path (relative to BaseURL, trailing slash included) into out,
// retrying on 429 and 5xx with exponential backoff.
func (c *Client) Get(ctx context.Context, path string, out any) error {
	url := BaseURL + path
	var lastErr error

	for attempt := 0; attempt < c.Attempts; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * 500 * time.Millisecond
			backoff += time.Duration(rand.Intn(250)) * time.Millisecond
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", "application/json")

		resp, err := c.HTTP.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		switch {
		case resp.StatusCode == http.StatusOK:
			if err := json.Unmarshal(body, out); err != nil {
				return fmt.Errorf("decoding %s: %w", url, err)
			}
			return nil
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			lastErr = &StatusError{url, resp.StatusCode, snippet(body)}
		default:
			// 404 and friends are terminal; retrying will not help.
			return &StatusError{url, resp.StatusCode, snippet(body)}
		}
	}
	return fmt.Errorf("gave up after %d attempts: %w", c.Attempts, lastErr)
}

func snippet(b []byte) string {
	const max = 120
	if len(b) > max {
		return string(b[:max]) + "..."
	}
	return string(b)
}
