package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/tonyc-dev/devtools-sms-login/internal/otp"
)

// otpd is the phone-code half of a release console login: it issues a code,
// decides whether an operator may run a release, and answers "where did that
// text go" while the release is waiting.
func main() {
	client, err := otp.NewClientFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	gate := otp.NewGate(client)

	mux := http.NewServeMux()

	// POST /login/start {"phone":"+1555...","request_id":"deploy-8421"}
	mux.HandleFunc("POST /login/start", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Phone     string `json:"phone"`
			RequestID string `json:"request_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Phone == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "phone is required"})
			return
		}
		ch, err := gate.Start(r.Context(), body.Phone, body.RequestID)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, ch)
	})

	// POST /login/check {"phone":"+1555...","code":"445120"}
	mux.HandleFunc("POST /login/check", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Phone string `json:"phone"`
			Code  string `json:"code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "phone and code are required"})
			return
		}
		res, err := gate.Check(r.Context(), body.Phone, body.Code)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		writeJSON(w, res.HTTPStatus, res)
	})

	// GET /diagnostics/{id} — delivery state of a code we sent.
	mux.HandleFunc("GET /diagnostics/{id}", func(w http.ResponseWriter, r *http.Request) {
		data, err := gate.Diagnose(r.Context(), r.PathValue("id"))
		if err != nil {
			writeAPIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, json.RawMessage(data))
	})

	addr := ":" + strings.TrimPrefix(envOr("PORT", "8080"), ":")
	log.Printf("otpd listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// writeAPIError keeps a declined request a 4xx for our own caller.
func writeAPIError(w http.ResponseWriter, err error) {
	var apiErr *otp.APIError
	if errors.As(err, &apiErr) {
		status := apiErr.Status
		if status < 400 || status > 499 {
			status = http.StatusBadGateway
		}
		writeJSON(w, status, map[string]string{"error": apiErr.Code, "detail": apiErr.Hint})
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]string{"error": "upstream_unavailable"})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
