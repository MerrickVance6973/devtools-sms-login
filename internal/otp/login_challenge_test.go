package otp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// stubAPI answers /v1/sms/otp and /v1/sms/verify with real Infrai envelopes.
// verdicts is consumed one entry per verify call.
func stubAPI(t *testing.T, verdicts []bool) *httptest.Server {
	t.Helper()
	i := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/sms/otp":
			json.NewEncoder(w).Encode(map[string]any{
				"ok": true, "data": map[string]any{"message_id": "sm_1"},
			})
		case "/v1/sms/verify":
			good := i < len(verdicts) && verdicts[i]
			i++
			if good {
				json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]any{}})
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{
				"ok": false,
				"error": map[string]any{
					"code": "INVALID_ARGUMENT", "hint": "code does not match",
				},
			})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
}

func newGate(t *testing.T, verdicts []bool) *Gate {
	srv := stubAPI(t, verdicts)
	t.Cleanup(srv.Close)
	g := NewGate(&Client{BaseURL: srv.URL, Key: "test", HTTP: srv.Client()})
	g.now = func() time.Time { return time.Unix(1700000000, 0) }
	return g
}

func TestCheckDecidesOutcome(t *testing.T) {
	cases := []struct {
		name     string
		start    bool
		verdicts []bool
		codes    []string
		want     Outcome
		wantHTTP int
	}{
		{"right code on first try", true, []bool{true}, []string{"445120"}, OutcomeAccepted, http.StatusOK},
		{"whitespace is trimmed", true, []bool{true}, []string{" 445120 "}, OutcomeAccepted, http.StatusOK},
		{"one wrong code stays open", true, []bool{false}, []string{"000000"}, OutcomeRejected, http.StatusUnauthorized},
		{"second try still works", true, []bool{false, true}, []string{"000000", "445120"}, OutcomeAccepted, http.StatusOK},
		{"third wrong code locks", true, []bool{false, false, false}, []string{"1", "2", "3"}, OutcomeLocked, http.StatusForbidden},
		{"no challenge issued", false, nil, []string{"445120"}, OutcomeRejected, http.StatusUnauthorized},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newGate(t, tc.verdicts)
			ctx := context.Background()
			const phone = "+15551230000"
			if tc.start {
				if _, err := g.Start(ctx, phone, "req-1"); err != nil {
					t.Fatalf("start: %v", err)
				}
			}
			var got Result
			for _, code := range tc.codes {
				var err error
				got, err = g.Check(ctx, phone, code)
				if err != nil {
					t.Fatalf("check: %v", err)
				}
			}
			if got.Outcome != tc.want || got.HTTPStatus != tc.wantHTTP {
				t.Fatalf("got %s/%d, want %s/%d", got.Outcome, got.HTTPStatus, tc.want, tc.wantHTTP)
			}
		})
	}
}

func TestLockedChallengeDoesNotReopen(t *testing.T) {
	g := newGate(t, []bool{false, false, false, true})
	ctx := context.Background()
	const phone = "+15551230000"
	if _, err := g.Start(ctx, phone, "req-1"); err != nil {
		t.Fatalf("start: %v", err)
	}
	for i := 0; i < MaxAttempts; i++ {
		if _, err := g.Check(ctx, phone, "000000"); err != nil {
			t.Fatalf("check: %v", err)
		}
	}
	res, err := g.Check(ctx, phone, "445120")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if res.Outcome != OutcomeRejected {
		t.Fatalf("a locked challenge accepted a code: %s", res.Outcome)
	}
}
