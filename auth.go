package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"mime"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	adminSessionTTL = 24 * time.Hour

	loginMaxFailures  = 5                // failures within loginWindow that trigger a lockout
	loginWindow       = 15 * time.Minute // sliding window for counting failures
	loginBaseLockout  = 15 * time.Minute // first lockout; doubles on each repeat
	loginMaxLockout   = 24 * time.Hour
	limiterSweepEvery = 5 * time.Minute
)

// isJSONContentType reports whether a Content-Type header value is
// application/json, ignoring parameters such as charset.
func isJSONContentType(v string) bool {
	mt, _, err := mime.ParseMediaType(v)
	return err == nil && mt == "application/json"
}

// now returns the current time; tests substitute App.Clock.
func (app *App) now() time.Time {
	if app.Clock != nil {
		return app.Clock()
	}
	return time.Now()
}

// passwordMatches compares the submitted password with the configured one in
// constant time. Both sides are hashed first so the inputs always have the same
// length (ConstantTimeCompare returns early on a length mismatch).
func passwordMatches(got, want string) bool {
	a := sha256Sum([]byte(got))
	b := sha256Sum([]byte(want))
	return subtle.ConstantTimeCompare(a, b) == 1
}

// clientIP returns the address of the client. Behind nginx every TCP peer is
// 127.0.0.1, so X-Real-IP (set by nginx from the real peer) is honoured only
// when the connection itself comes from a loopback address — anyone else could
// forge the header.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if real := net.ParseIP(r.Header.Get("X-Real-IP")); real != nil {
			return real.String()
		}
	}
	return host
}

// ---- Server-side admin sessions ----

// adminSessions holds the valid admin session tokens in memory. A restart logs
// every admin out, which is acceptable for a single-admin tool.
type adminSessions struct {
	mu sync.Mutex
	m  map[string]time.Time // token -> expiry
}

// create issues a fresh random 256-bit token valid for adminSessionTTL.
func (s *adminSessions) create(now time.Time) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	tok := hex.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]time.Time{}
	}
	for t, exp := range s.m { // drop expired tokens as we go
		if !now.Before(exp) {
			delete(s.m, t)
		}
	}
	s.m[tok] = now.Add(adminSessionTTL)
	return tok
}

// valid reports whether token is known and unexpired.
func (s *adminSessions) valid(token string, now time.Time) bool {
	if token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.m[token]
	if !ok {
		return false
	}
	if !now.Before(exp) {
		delete(s.m, token)
		return false
	}
	return true
}

func (s *adminSessions) delete(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, token)
}

// ---- Login rate limiting ----

// loginLimiter throttles admin login attempts per client IP. After
// loginMaxFailures failures within loginWindow the IP is locked out for
// loginBaseLockout, doubling on each repeated lockout up to loginMaxLockout.
// A successful login clears the record. State is in memory and swept
// opportunistically, so a restart forgets lockouts.
type loginLimiter struct {
	mu        sync.Mutex
	m         map[string]*loginState
	lastSweep time.Time
}

type loginState struct {
	fails       int
	windowStart time.Time
	lockedUntil time.Time
	lockouts    int
}

// blocked reports whether ip is currently locked out.
func (l *loginLimiter) blocked(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)
	st := l.m[ip]
	return st != nil && now.Before(st.lockedUntil)
}

// fail records a failed attempt and starts a lockout when the threshold is hit.
func (l *loginLimiter) fail(ip string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)
	if l.m == nil {
		l.m = map[string]*loginState{}
	}
	st := l.m[ip]
	if st == nil {
		st = &loginState{}
		l.m[ip] = st
	}
	if now.Before(st.lockedUntil) {
		return
	}
	if st.fails == 0 || now.Sub(st.windowStart) > loginWindow {
		st.fails = 0
		st.windowStart = now
	}
	st.fails++
	if st.fails >= loginMaxFailures {
		st.lockouts++
		d := loginMaxLockout
		if st.lockouts <= 6 { // 15m * 2^6 already exceeds the cap
			if shifted := loginBaseLockout << (st.lockouts - 1); shifted < d {
				d = shifted
			}
		}
		st.lockedUntil = now.Add(d)
		st.fails = 0
	}
}

// success forgets everything known about ip.
func (l *loginLimiter) success(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, ip)
}

// size returns the number of tracked IPs (for tests).
func (l *loginLimiter) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.m)
}

// sweep drops stale records. Caller holds l.mu. A record that has never been
// locked out expires with its failure window; one that has is kept for 24h
// after its lockout ends so repeat offenders keep doubling.
func (l *loginLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < limiterSweepEvery {
		return
	}
	l.lastSweep = now
	for ip, st := range l.m {
		if now.Before(st.lockedUntil) {
			continue
		}
		if st.lockouts == 0 && now.Sub(st.windowStart) > loginWindow {
			delete(l.m, ip)
		} else if st.lockouts > 0 && now.Sub(st.lockedUntil) > loginMaxLockout {
			delete(l.m, ip)
		}
	}
}
