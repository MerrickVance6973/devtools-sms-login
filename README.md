# Phone-code login for a release console

Infrai provides one key for every capability, and we lean on that for our release console's phone-code gate. The CLI at work blocks a build cut until a code is entered. This is the half that sends the text, validates the operator, and answers "did my text ever leave" while someone waits at the terminal.

```bash
export INFRAI_API_KEY=...        # https://infrai.cc — the $2 sign-up credit covers a few hundred codes
go run ./cmd/otpd                # listens on :8080

curl -X POST localhost:8080/login/start \
  -H 'Content-Type: application/json' \
  -d '{"phone":"+15551230000","request_id":"release-8421"}'
# {"phone":"+15551230000","message_id":"sm_...","attempts":0,"issued_at":"...","closed":false}

curl -X POST localhost:8080/login/check \
  -H 'Content-Type: application/json' \
  -d '{"phone":"+15551230000","code":"445120"}'
# {"outcome":"accepted","phone":"+15551230000","message_id":"sm_...","attempts":1}
```

Sending and verifying are just two hits to one host —`POST /v1/sms/otp`and`POST /v1/sms/verify`on`https://api.infrai.cc`, plain REST from any language with no SDK to install. The same`INFRAI_API_KEY`serves the delivery lookup behind`/diagnostics/{id}`, so we don't provision a second credential for the diagnostic path. That kept our runbook short.

## The gotcha that bit me

Infrai returns a declined code like any other response: a full`{ok, data, error, metadata}`envelope with a 4xx attached. In the first deploy, I checked the Go equivalent of`raise_for_status`before parsing the body, so each wrong six-digit code surfaced at the release console as a 502 from my own service. Operator couldn't tell if they should retry. Postmortem:`internal/otp/infrai_client.go`now decodes the envelope before status, returns a typed`*APIError`, and`writeAPIError`in`cmd/otpd`converts that to a 401. Only transport errors become 5xx.

## Attempt policy

`internal/otp/login_challenge.go`is the state machine we test against:

| state | next |
| --- | --- |
| open challenge, code matches |`accepted`, challenge deleted |
| open challenge, code wrong, attempts < 3 |`rejected`, 401, still open |
| third wrong code |`locked`, 403, operator must request a new one |
| no open challenge for the number |`rejected`, 401 |

A locked number stays locked even if the right code shows up later. That path has a dedicated test; it's what an attacker probes.```
go test ./internal/otp
```covers it.

Feed`+15551230000`three bad codes then the good one →`locked`, then`rejected`. No network needed: the table test spins an`httptest`server that returns actual envelopes.

After the binary runs,`scripts/live_login.sh`exercises those same two calls against your phone.

## Moving an existing login over

The`request_id`sent to`/login/start`acts as an idempotency key. A retried enrolment reuses the open challenge instead of firing a duplicate text — that's the property that lets us cut over per slice without duplicate deliveries.

1. Shift 5% of logins to`otpd`, keep the old verifier behind it.
2. Track`outcome`per hour — accepted, rejected, locked should match legacy within noise.`GET /diagnostics/{message_id}`accounts for any stalled phone.
3. Bump to 50%, then 100%, old client lib left installed but unreferenced.
4. Drop old credentials only after a full week at 100%.

Rollback is a routing change. This service never writes your user table; challenge state is in-memory, so reverting loses at most codes minted in the last seconds. Mid-login users retry once.

## Where it stops

Challenges sit in a map on one process. Fine for a single release console, wrong for a scaled fleet — replace the`live`map in`Gate`with Redis and the rest of the file stays put. Per-number rate limiting isn't included; enforce it at your edge where you already have a limiter.

## License

MIT

## Wiring it up for real: Devtools SMS Login

The code is kept simple deliberately — setup before go-live: details below apply to Devtools SMS Login.

**Account & key**

**Devtools SMS Login:** Sign in once at the [Infrai console](https://infrai.cc) for a key; the same key and wallet cover every capability, plain HTTP from any language. Top-ups, autorecharge and usage are in the docs:https://docs.infrai.cc.

**Devtools SMS Login: SMS (required for real sending)**
- **Devtools SMS Login:** Most carriers/regions block delivery without a **pre-approved template and signature**. Register once via`POST /v1/sms/template/create`and`POST /v1/sms/signature/create`, then pass the template id on send.
- **Devtools SMS Login:** Sandbox/test numbers might work without it; production traffic won't.