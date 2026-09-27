package server

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// maxAuthFailures failed API-key attempts within authFailureWindow block the client for authBlockDuration.
	maxAuthFailures   = 5
	authFailureWindow = 15 * time.Minute
	authBlockDuration = 15 * time.Minute
)

// authLimiter tracks failed API-key attempts per client IP to slow down brute forcing.
type authLimiter struct {
	mu        sync.Mutex
	clients   map[string]*authFailures
	lastSweep time.Time
}

type authFailures struct {
	count        int
	windowStart  time.Time
	blockedUntil time.Time
}

// blocked reports whether ip is currently blocked, and for how long.
func (l *authLimiter) blocked(ip string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if c := l.clients[ip]; c != nil && now.Before(c.blockedUntil) {
		return true, c.blockedUntil.Sub(now)
	}
	return false, 0
}

// fail records a failed attempt and reports whether it got ip blocked.
func (l *authLimiter) fail(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)
	c := l.clients[ip]
	if c == nil || now.Sub(c.windowStart) > authFailureWindow {
		c = &authFailures{windowStart: now}
		l.clients[ip] = c
	}
	c.count++
	if c.count >= maxAuthFailures {
		c.blockedUntil = now.Add(authBlockDuration)
		c.count = 0
		c.windowStart = now
		return true
	}
	return false
}

// succeed forgets ip's failures: whoever has the key isn't brute forcing it.
func (l *authLimiter) succeed(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.clients, ip)
}

// sweep drops entries whose window and block have both expired, at most once a minute.
func (l *authLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < time.Minute {
		return
	}
	l.lastSweep = now
	for ip, c := range l.clients {
		if now.Sub(c.windowStart) > authFailureWindow && !now.Before(c.blockedUntil) {
			delete(l.clients, ip)
		}
	}
}

// clientIP is the request's source address, or with behindProxy the last X-Forwarded-For entry
// (the one the trusted reverse proxy appended). Only enable behindProxy when a proxy sets that header.
func clientIP(r *http.Request, behindProxy bool) string {
	if behindProxy {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			parts := strings.Split(fwd, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func retryAfterSeconds(d time.Duration) string {
	return strconv.Itoa(int((d + time.Second - 1) / time.Second))
}
