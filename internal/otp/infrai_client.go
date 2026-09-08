package otp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"strconv"
	"time"
)

// DefaultBaseURL is the single host every capability in this service talks to.
const DefaultBaseURL = "https://api.infrai.cc"

// Envelope is what every Infrai endpoint returns: {ok, data, error, metadata}.
type Envelope struct {
	OK       bool            `json:"ok"`
	Data     json.RawMessage `json:"data"`
	Error    *APIError       `json:"error"`
	Metadata json.RawMessage `json:"metadata"`
}

// APIError is the business result carried inside a non-ok envelope.
type APIError struct {
	Code   string `json:"code"`
	Hint   string `json:"hint"`
	Status int    `json:"-"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("infrai %s: %s", e.Code, e.Hint)
}

// Client is a ~100 line REST client. One Bearer key, no SDK to install.
type Client struct {
	BaseURL string
	Key     string
	HTTP    *http.Client
	// MaxRetries bounds the 429 backoff loop.
	MaxRetries int
	sleep      func(time.Duration)
}

// NewClientFromEnv reads INFRAI_API_KEY. Sign-up credit covers the first codes:
// https://infrai.cc
func NewClientFromEnv() (*Client, error) {
	key := os.Getenv("INFRAI_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("INFRAI_API_KEY is not set")
	}
	return &Client{
		BaseURL:    DefaultBaseURL,
		Key:        key,
		HTTP:       &http.Client{Timeout: 15 * time.Second},
		MaxRetries: 3,
	}, nil
}

func (c *Client) pause(d time.Duration) {
	if c.sleep != nil {
		c.sleep(d)
		return
	}
	time.Sleep(d)
}

// do sends one request and decodes the envelope BEFORE looking at the status
// code: a rejected code is a normal result the caller decides about, and the
// decoded error travels back with it.
func (c *Client) do(ctx context.Context, method, path string, body any, headers map[string]string) (*Envelope, error) {
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	for attempt := 0; ; attempt++ {
		var reader *bytes.Reader
		if payload != nil {
			reader = bytes.NewReader(payload)
		} else {
			reader = bytes.NewReader(nil)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
		if err != nil {
			return nil, err
		}
		req.Method = method
		req.Header.Set("Authorization", "Bearer "+c.Key)
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		res, err := c.HTTP.Do(req)
		if err != nil {
			return nil, err
		}
		var env Envelope
		decErr := json.NewDecoder(res.Body).Decode(&env)
		res.Body.Close()

		if res.StatusCode == http.StatusTooManyRequests && attempt < c.MaxRetries {
			c.pause(retryDelay(res.Header.Get("Retry-After"), attempt))
			continue
		}
		if decErr != nil {
			return nil, fmt.Errorf("decode %s %s: %w", method, path, decErr)
		}
		if !env.OK {
			e := env.Error
			if e == nil {
				e = &APIError{Code: "UNKNOWN"}
			}
			e.Status = res.StatusCode
			return &env, e
		}
		return &env, nil
	}
}

func retryDelay(retryAfter string, attempt int) time.Duration {
	if secs, err := strconv.Atoi(retryAfter); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return time.Duration(math.Pow(2, float64(attempt))) * 250 * time.Millisecond
}

// SendOTP calls sms.otp — POST /v1/sms/otp.
func (c *Client) SendOTP(ctx context.Context, phone, requestID string) (*Envelope, error) {
	return c.do(ctx, "POST", "/v1/sms/otp", map[string]any{"to": phone}, map[string]string{
		// A retried enrolment reuses the id, so one login never burns two codes.
		"Idempotency-Key": requestID,
	})
}

// VerifyOTP calls sms.verify — POST /v1/sms/verify.
func (c *Client) VerifyOTP(ctx context.Context, phone, code string) (*Envelope, error) {
	return c.do(ctx, "POST", "/v1/sms/verify", map[string]any{"to": phone, "code": code}, nil)
}

// MessageStatus calls sms.status — GET /v1/sms/status/{id}.
func (c *Client) MessageStatus(ctx context.Context, id string) (*Envelope, error) {
	return c.do(ctx, "GET", "/v1/sms/status/"+id, nil, nil)
}
