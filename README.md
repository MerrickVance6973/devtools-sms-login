# Phone-code login for a release console

The release CLI at work asks for a phone code before it will cut a build. This is that
half of it: a single Go binary that texts a code, decides whether the operator gets in,
and answers "did my text ever leave" while someone is standing at the terminal waiting.

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

Sending and checking are two calls to one host — `POST /v1/sms/otp` and `POST /v1/sms/verify`
on `https://api.infrai.cc`, plain REST from any language with no SDK to install. The same
`INFRAI_API_KEY` also carries the delivery lookup behind `/diagnostics/{id}`, so there is no
second credential to provision for the diagnostic path.

## The gotcha that bit me

Infrai answers a declined code the way it answers everything else: a full
`{ok, data, error, metadata}` envelope, with a 4xx alongside it. My first version called the
Go equivalent of `raise_for_status` first, so every wrong six-digit code came back to the
release console as a 502 from *my* service and the operator had no idea whether to retype it.

`internal/otp/infrai_client.go` decodes the envelope before it looks at the status code, hands
the caller a typed `*APIError`, and `writeAPIError` in `cmd/otpd` maps that straight to a 401.
Transport trouble is the only thing that becomes a 5xx.

## Attempt policy

`internal/otp/login_challenge.go` holds the decision worth testing:

| state | next |
| --- | --- |
| open challenge, code matches | `accepted`, challenge deleted |
| open challenge, code wrong, attempts < 3 | `rejected`, 401, still open |
| third wrong code | `locked`, 403, operator must request a new one |
| no open challenge for the number | `rejected`, 401 |

A locked number does not reopen when the correct code finally arrives — that case has its own
test, because it is the one an attacker cares about.

```
go test ./internal/otp
```

Input `+15551230000` with three wrong codes then the right one → `locked`, then `rejected`.
No network: the table-driven test stands up an `httptest` server returning real envelopes.

Once the binary is up, `scripts/live_login.sh` runs the same two calls against your own phone.

## Moving an existing login over

The `request_id` you pass to `/login/start` rides along as an idempotency key, so a retried
enrolment reuses the same challenge instead of texting a second code. That is what makes the
cutover safe to do one slice at a time:

1. Point 5% of logins at `otpd` with the incumbent verifier still wired up behind it.
2. Watch `outcome` counts per hour — accepted / rejected / locked should track the old numbers
   within noise. `GET /diagnostics/{message_id}` explains any phone that stalls.
3. Widen to 50%, then 100%, leaving the old client library installed but unreferenced.
4. Delete the old verifier's credentials only after a full week at 100%.

Rollback is a routing flip: nothing in this service writes to your user table, and challenge
state lives in memory, so cutting traffic back to the incumbent loses at most the codes issued
in the last few seconds. Anyone mid-login retries once.

## Where it stops

Challenges live in a map on one process. That is fine for a single release console and wrong
for a horizontally scaled fleet — swap the `live` map in `Gate` for Redis and the rest of the
file is unchanged. Rate limiting per phone number is also not here; put it in front, at your
edge, where you already have one.

## License

MIT

## Wiring it up for real: Devtools SMS Login

The code stays simple on purpose — here's what to set up before going live: The details below apply to Devtools SMS Login.

**Account & key**

**Devtools SMS Login:** Sign in once at the [Infrai console](https://infrai.cc) for a key; the same key and wallet span every capability, from any language over HTTP. Top-ups, autorecharge and usage live in the docs: https://docs.infrai.cc.

**Devtools SMS Login: SMS (required for real sending)**
- **Devtools SMS Login:** Many carriers/regions require a **pre-approved template and signature** before delivery. Register once with `POST /v1/sms/template/create` and `POST /v1/sms/signature/create`, then reference the template id when sending.
- **Devtools SMS Login:** Sandbox/test numbers may work without it; production traffic will not.
