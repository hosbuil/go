package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

var key = []byte("01234567890123456789012345678901")

func token(payload string, alg string) string {
	enc := base64.RawURLEncoding.EncodeToString
	raw := enc([]byte(`{"alg":"`+alg+`"}`)) + "." + enc([]byte(payload))
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(raw))
	return raw + "." + enc(mac.Sum(nil))
}
func validToken() string {
	return token(fmt.Sprintf(`{"sub":"alice","iss":"issuer","aud":"gateway","exp":%d}`, time.Now().Add(time.Hour).Unix()), "HS256")
}
func TestJWT(t *testing.T) {
	now := time.Now()
	if _, e := verify(validToken(), key, "issuer", "gateway", now); e != nil {
		t.Fatal(e)
	}
	for _, s := range []string{validToken() + "x", token(`{"sub":"a","iss":"issuer","aud":"gateway","exp":1}`, "HS256"), token(`{"sub":"a","iss":"issuer","aud":"gateway","exp":9999999999}`, "none"), token(`{"sub":"a","iss":"wrong","aud":"gateway","exp":9999999999}`, "HS256"), token(`{"sub":"a","iss":"issuer","aud":"wrong","exp":9999999999}`, "HS256"), token(`{"sub":"a","iss":"issuer","aud":"gateway","exp":9999999999,"nbf":9999999999}`, "HS256")} {
		if _, e := verify(s, key, "issuer", "gateway", now); e == nil {
			t.Fatal("accepted invalid token")
		}
	}
}
func TestLimiter(t *testing.T) {
	l := &limiter{clients: map[string]bucket{}, rate: 1, burst: 2, limit: 1}
	now := time.Now()
	if !l.allow("a", now) || !l.allow("a", now) || l.allow("a", now) || l.allow("b", now) || !l.allow("a", now.Add(time.Second)) {
		t.Fatal("bucket behavior")
	}
}
func TestGateway(t *testing.T) {
	backend := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "" || r.Header.Get("X-Authenticated-Subject") != "alice" || strings.Contains(r.Header.Get("X-Forwarded-For"), "spoof") {
				t.Error("unsafe headers")
			}
			fmt.Fprint(w, name+":"+r.URL.RequestURI())
		}))
	}
	a, b := backend("a"), backend("b")
	defer a.Close()
	defer b.Close()
	cfg := Config{Issuer: "issuer", Audience: "gateway", Rate: 1000, Burst: 1000, Routes: []Route{{Prefix: "/api", Backends: []string{a.URL, b.URL}}, {Prefix: "/api/special", Backends: []string{b.URL}}}}
	h, e := newGateway(cfg, key)
	if e != nil {
		t.Fatal(e)
	}
	request := func(path, auth string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", auth)
		r.Header.Set("X-Forwarded-For", "spoof")
		r.Header.Set("X-Authenticated-Subject", "spoof")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if request("/api", "").Code != 401 {
		t.Fatal("missing auth")
	}
	for _, want := range []string{"a:/api?q=1", "b:/api?q=1", "a:/api?q=1"} {
		w := request("/api?q=1", "Bearer "+validToken())
		if w.Code != 200 || w.Body.String() != want {
			t.Fatalf("got %d %s want %s", w.Code, w.Body.String(), want)
		}
	}
	if w := request("/api/special", "Bearer "+validToken()); !strings.HasPrefix(w.Body.String(), "b:") {
		t.Fatal("longest prefix")
	}
	if request("/apix", "Bearer "+validToken()).Code != 404 {
		t.Fatal("route boundary")
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if request("/api", "Bearer "+validToken()).Code != 200 {
				t.Error("parallel request")
			}
		}()
	}
	wg.Wait()
	cfg.Burst = 1
	h, e = newGateway(cfg, key)
	if e != nil {
		t.Fatal(e)
	}
	request("/api", "")
	if request("/api", "").Code != 429 {
		t.Fatal("rate limit")
	}
}
