package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Config struct {
	Listen   string  `json:"listen"`
	Issuer   string  `json:"issuer"`
	Audience string  `json:"audience"`
	Rate     float64 `json:"rate"`
	Burst    int     `json:"burst"`
	Routes   []Route `json:"routes"`
}
type Route struct {
	Prefix   string   `json:"prefix"`
	Backends []string `json:"backends"`
}
type Claims struct {
	Sub string          `json:"sub"`
	Iss string          `json:"iss"`
	Aud json.RawMessage `json:"aud"`
	Exp int64           `json:"exp"`
	Nbf int64           `json:"nbf"`
}

func verify(token string, key []byte, issuer, audience string, now time.Time) (Claims, error) {
	var c Claims
	fail := errors.New("invalid token")
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(token) > 8192 {
		return c, fail
	}
	decode := base64.RawURLEncoding.Strict().DecodeString
	header, e := decode(parts[0])
	if e != nil {
		return c, fail
	}
	var h struct {
		Alg  string   `json:"alg"`
		Crit []string `json:"crit"`
		B64  *bool    `json:"b64"`
	}
	if json.Unmarshal(header, &h) != nil || h.Alg != "HS256" || len(h.Crit) > 0 || h.B64 != nil {
		return c, fail
	}
	sig, e := decode(parts[2])
	if e != nil {
		return c, fail
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return c, fail
	}
	payload, e := decode(parts[1])
	if e != nil || json.Unmarshal(payload, &c) != nil {
		return c, fail
	}
	var aud string
	var audiences []string
	if json.Unmarshal(c.Aud, &aud) == nil {
		audiences = []string{aud}
	} else if json.Unmarshal(c.Aud, &audiences) != nil {
		return c, fail
	}
	match := false
	for _, v := range audiences {
		if v == audience {
			match = true
		}
	}
	if c.Sub == "" || len(c.Sub) > 256 || strings.ContainsAny(c.Sub, "\r\n") || c.Iss != issuer || !match || c.Exp <= now.Unix() || c.Nbf > now.Unix() {
		return c, fail
	}
	return c, nil
}

type bucket struct {
	tokens float64
	at     time.Time
}
type limiter struct {
	mu      sync.Mutex
	clients map[string]bucket
	rate    float64
	burst   int
	limit   int
}

func (l *limiter) allow(id string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.clients[id]
	if !ok {
		if len(l.clients) >= l.limit {
			for k, v := range l.clients {
				if now.Sub(v.at).Seconds() >= float64(l.burst)/l.rate {
					delete(l.clients, k)
				}
			}
		}
		if len(l.clients) >= l.limit {
			return false
		}
		b = bucket{float64(l.burst), now}
	}
	b.tokens = min(float64(l.burst), b.tokens+max(0, now.Sub(b.at).Seconds())*l.rate)
	b.at = now
	allowed := b.tokens >= 1
	if allowed {
		b.tokens--
	}
	l.clients[id] = b
	return allowed
}

type pool struct {
	prefix  string
	proxies []*httputil.ReverseProxy
	next    atomic.Uint64
}

func newGateway(cfg Config, key []byte) (http.Handler, error) {
	if len(key) < 32 || cfg.Issuer == "" || cfg.Audience == "" || cfg.Rate <= 0 || cfg.Rate > 1e6 || cfg.Burst < 1 || cfg.Burst > 1000000 || len(cfg.Routes) == 0 {
		return nil, errors.New("invalid config or JWT_SECRET (minimum 32 bytes)")
	}
	pools := []*pool{}
	seen := map[string]bool{}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 256
	transport.MaxIdleConnsPerHost = 64
	transport.ResponseHeaderTimeout = 15 * time.Second
	for _, route := range cfg.Routes {
		if !strings.HasPrefix(route.Prefix, "/") || strings.ContainsAny(route.Prefix, "?#") || seen[route.Prefix] || len(route.Backends) == 0 {
			return nil, errors.New("invalid or duplicate route")
		}
		seen[route.Prefix] = true
		q := &pool{prefix: strings.TrimSuffix(route.Prefix, "/")}
		for _, raw := range route.Backends {
			target, err := url.Parse(raw)
			if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") || target.User != nil || target.RawQuery != "" || target.Fragment != "" {
				return nil, fmt.Errorf("invalid backend: %s", raw)
			}
			proxy := &httputil.ReverseProxy{Transport: transport, Rewrite: func(r *httputil.ProxyRequest) { r.SetURL(target); r.SetXForwarded(); r.Out.Header.Del("Authorization") }, ErrorHandler: func(w http.ResponseWriter, r *http.Request, e error) { http.Error(w, "backend unavailable", 502) }}
			q.proxies = append(q.proxies, proxy)
		}
		pools = append(pools, q)
	}
	sort.Slice(pools, func(i, j int) bool { return len(pools[i].prefix) > len(pools[j].prefix) })
	limits := &limiter{clients: map[string]bucket{}, rate: cfg.Rate, burst: cfg.Burst, limit: 100000}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			if r.Method != "GET" {
				w.WriteHeader(405)
				return
			}
			w.Write([]byte("ok\n"))
			return
		}
		// Rate limit direct peer IP before expensive authentication. Forwarded headers are untrusted.
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
		if !limits.allow(ip, time.Now()) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "rate limit exceeded", 429)
			return
		}
		auth := strings.Fields(r.Header.Get("Authorization"))
		if len(auth) != 2 || !strings.EqualFold(auth[0], "Bearer") {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", 401)
			return
		}
		claims, err := verify(auth[1], key, cfg.Issuer, cfg.Audience, time.Now())
		if err != nil {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", 401)
			return
		}
		for _, q := range pools {
			if q.prefix == "" || r.URL.Path == q.prefix || strings.HasPrefix(r.URL.Path, q.prefix+"/") {
				r.Header.Del("X-Authenticated-Subject")
				r.Header.Set("X-Authenticated-Subject", claims.Sub)
				q.proxies[(q.next.Add(1)-1)%uint64(len(q.proxies))].ServeHTTP(w, r)
				return
			}
		}
		http.NotFound(w, r)
	}), nil
}
