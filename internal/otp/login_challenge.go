package otp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Outcome is the decision the release console acts on.
type Outcome string

const (
	OutcomeAccepted Outcome = "accepted"
	OutcomeRejected Outcome = "rejected"
	OutcomeLocked   Outcome = "locked"
)

// MaxAttempts is how many wrong codes a phone may burn before the challenge is
// closed and the operator has to request a fresh one.
const MaxAttempts = 3

// Challenge is one in-flight login for one engineer.
type Challenge struct {
	Phone     string    `json:"phone"`
	MessageID string    `json:"message_id"`
	Attempts  int       `json:"attempts"`
	IssuedAt  time.Time `json:"issued_at"`
	Closed    bool      `json:"closed"`
}

// Gate holds live challenges and applies the attempt policy. Zero value is not
// usable; call NewGate.
type Gate struct {
	client *Client
	now    func() time.Time

	mu   sync.Mutex
	live map[string]*Challenge
}

func NewGate(c *Client) *Gate {
	return &Gate{client: c, now: time.Now, live: map[string]*Challenge{}}
}

// Result is what an HTTP handler (or the release CLI) turns into a response.
type Result struct {
	Outcome    Outcome `json:"outcome"`
	Phone      string  `json:"phone"`
	MessageID  string  `json:"message_id,omitempty"`
	Attempts   int     `json:"attempts"`
	Reason     string  `json:"reason,omitempty"`
	HTTPStatus int     `json:"-"`
}

type otpData struct {
	MessageID string `json:"message_id"`
}

// Start sends a code to phone. requestID ties a retried enrolment to the same
// challenge, so a flaky operator connection does not text two codes.
func (g *Gate) Start(ctx context.Context, phone, requestID string) (*Challenge, error) {
	env, err := g.client.SendOTP(ctx, phone, requestID)
	if err != nil {
		return nil, err
	}
	var d otpData
	if len(env.Data) > 0 {
		_ = json.Unmarshal(env.Data, &d)
	}
	ch := &Challenge{Phone: phone, MessageID: d.MessageID, IssuedAt: g.now()}
	g.mu.Lock()
	g.live[phone] = ch
	g.mu.Unlock()
	return ch, nil
}

// Check applies the code to the open challenge for phone.
func (g *Gate) Check(ctx context.Context, phone, code string) (Result, error) {
	g.mu.Lock()
	ch, ok := g.live[phone]
	g.mu.Unlock()
	if !ok || ch.Closed {
		return Result{
			Outcome:    OutcomeRejected,
			Phone:      phone,
			Reason:     "no open challenge for this number",
			HTTPStatus: http.StatusUnauthorized,
		}, nil
	}

	_, err := g.client.VerifyOTP(ctx, phone, strings.TrimSpace(code))

	g.mu.Lock()
	defer g.mu.Unlock()
	ch.Attempts++

	if err == nil {
		ch.Closed = true
		delete(g.live, phone)
		return Result{
			Outcome:    OutcomeAccepted,
			Phone:      phone,
			MessageID:  ch.MessageID,
			Attempts:   ch.Attempts,
			HTTPStatus: http.StatusOK,
		}, nil
	}

	// A code the service declined is a decision, not a transport failure: it
	// arrives as a decoded envelope and becomes a 401/403 to our own caller.
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return Result{}, err
	}

	res := Result{
		Outcome:    OutcomeRejected,
		Phone:      phone,
		MessageID:  ch.MessageID,
		Attempts:   ch.Attempts,
		Reason:     apiErr.Hint,
		HTTPStatus: http.StatusUnauthorized,
	}
	if ch.Attempts >= MaxAttempts {
		ch.Closed = true
		delete(g.live, phone)
		res.Outcome = OutcomeLocked
		res.Reason = "too many attempts; request a new code"
		res.HTTPStatus = http.StatusForbidden
	}
	return res, nil
}

// Diagnose reports delivery state for a code we sent, for the "did my SMS ever
// leave" question an on-call engineer asks during a release.
func (g *Gate) Diagnose(ctx context.Context, messageID string) (json.RawMessage, error) {
	env, err := g.client.MessageStatus(ctx, messageID)
	if err != nil {
		return nil, err
	}
	return env.Data, nil
}
