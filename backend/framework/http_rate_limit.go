package framework

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
)

var ErrHTTPRateLimitKey = errors.New("framework: HTTP rate limit key is unavailable")

type HTTPRateLimitKeyFunc func(*http.Request, Principal) (string, error)

func DefaultHTTPRateLimitKey(
	request *http.Request,
	principal Principal,
) (string, error) {
	if principal.valid() {
		return rateLimitIdentityKey("principal", principal.Subject()), nil
	}
	if request == nil {
		return "", ErrHTTPRateLimitKey
	}
	remoteAddress := strings.TrimSpace(request.RemoteAddr)
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		if net.ParseIP(remoteAddress) == nil {
			return "", ErrHTTPRateLimitKey
		}
		host = remoteAddress
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return "", ErrHTTPRateLimitKey
	}
	return rateLimitIdentityKey("ip", host), nil
}

func rateLimitIdentityKey(kind, value string) string {
	return fmt.Sprintf("%s:%x", kind, sha256.Sum256([]byte(value)))
}
