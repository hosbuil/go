# Go API gateway and load balancer

A dependency-free Go 1.22+ HTTP reverse proxy with configurable service routes, atomic round-robin selection, HS256 JWT authentication, and per-peer-IP token-bucket rate limiting.

## Run

```sh
export JWT_SECRET="$(openssl rand -hex 32)"
go run . -config config.json
```

Start your backend services on ports 9001/9002 for `/api` and 9003 for `/billing`. The gateway listens on `127.0.0.1:8080`. `GET /healthz` is public and reports process liveness. Other paths require authentication. Routes use longest matching path prefixes with segment boundaries: `/api` matches `/api/items` but not `/apix`. Original path and query are preserved; a configured backend base path is prepended. Each route has its own round-robin counter.

## Local test token

In the same shell, generate a token using Python's standard library, then make a request:

```sh
TOKEN=$(python3 - <<'PY'
import base64, hashlib, hmac, json, os, time
enc = lambda b: base64.urlsafe_b64encode(b).rstrip(b'=')
header = enc(json.dumps({'alg': 'HS256', 'typ': 'JWT'}).encode())
claims = enc(json.dumps({'sub': 'demo', 'iss': 'local-dev', 'aud': 'go-gateway', 'exp': int(time.time()) + 3600}).encode())
message = header + b'.' + claims
print((message + b'.' + enc(hmac.new(os.environ['JWT_SECRET'].encode(), message, hashlib.sha256).digest())).decode())
PY
)
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8080/api/items
```

JWT validation requires a signature, `sub`, matching `iss` and `aud`, and an unexpired integer `exp`; optional integer `nbf` is enforced. Audience can be a string or array. Only HS256 is accepted, unsupported critical extensions are rejected, and the shared secret must contain at least 32 bytes. Supply strong random secret material via the environment. Token issuance and user login belong to an external identity service.

The gateway removes the bearer token before forwarding and overwrites `X-Authenticated-Subject` with the verified subject. Forwarding headers are rebuilt by Go's `ReverseProxy.Rewrite`/`SetXForwarded`, so inbound client claims about forwarding origin are not trusted. Backend services should only trust identity headers from the gateway over a protected network.

## Limits and failure behavior

- `rate` is refill tokens/second per direct peer IP; `burst` is initial and maximum tokens. Requests are limited before authentication, including unknown paths. Rejections return 429 and `Retry-After: 1`.
- Client state is capped at 100,000 entries. At capacity, fully refilled idle entries are reclaimed; otherwise new clients are rejected. Existing clients retain their buckets.
- Behind another proxy, all clients from that peer share a bucket. Trusted-proxy-aware client extraction is not implemented. IPv6 address rotation can evade per-IP quotas.
- Authentication failures return 401; unmatched routes return 404; transport failures return 502. Backend HTTP error responses are passed through.
- Backend selection is round-robin, without health checks, circuit breakers, or automatic failover/retries. This avoids duplicating non-idempotent requests. `/healthz` does not check backend readiness.
- Limits are local to one gateway process. Multiple replicas require shared rate-limit state for global quotas.
- HTTP server timeouts and a 15-second backend response-header timeout bound ordinary requests. Long streams and WebSocket tunneling are not part of the verified feature set.
- Termination signals trigger up to 10 seconds of graceful shutdown. TLS termination is expected upstream; use a private network for backend traffic or configure HTTPS backend URLs.

## Verify

```sh
go test -race ./...
go vet ./...
go build -o gateway .
```

Tests cover signature/claim rejection, token bucket refill and capacity, authentication, round-robin order, concurrent proxy requests, prefix boundaries, longest-prefix selection, query preservation, and spoofed forwarding/identity headers.

Reference: [Go reverse proxy implementation](https://go.dev/src/net/http/httputil/reverseproxy.go).
