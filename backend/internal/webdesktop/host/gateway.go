// Package host serves a packaged dist and authenticates its native webview.
package host

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const policy = "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; object-src 'none'; base-uri 'none'; frame-src 'none'; frame-ancestors 'none'; form-action 'none'"

type gateway struct {
	host, origin, bearer, proof, session string
	expires                              time.Time
	mu                                   sync.Mutex
	active                               bool
	dist                                 *os.Root
	backend                              http.Handler
}

func secret() string {
	var b [32]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func newGateway(address, bearer string, root *os.Root, backend http.Handler) *gateway {
	return &gateway{host: address, origin: "http://" + address, bearer: bearer, proof: secret(), session: secret(),
		expires: time.Now().Add(time.Minute), dist: root, backend: backend}
}

func matches(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", policy)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Cache-Control", "no-store")
	if r.Host != g.host {
		http.Error(w, "invalid host", http.StatusForbidden)
		return
	}
	switch r.URL.Path {
	case "/__bootstrap":
		if r.Method != http.MethodPost || r.URL.RawQuery != "" || len(r.Header.Values("Authorization")) != 1 ||
			r.Header.Get("Origin") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Sec-Fetch-Site") != "" || r.Header.Get("Sec-Fetch-Mode") != "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		g.mu.Lock()
		ok := !g.active && time.Now().Before(g.expires) && matches(r.Header.Get("Authorization"), "Bearer "+g.proof)
		if ok {
			g.active = true
		}
		g.mu.Unlock()
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(struct {
			Version int    `json:"version"`
			Session string `json:"session"`
		}{1, g.session})
	case "/rpc":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", 405)
			return
		}
		cookies := r.CookiesNamed("bridra_wk")
		g.mu.Lock()
		active := g.active
		g.mu.Unlock()
		if !active || len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != g.origin ||
			len(cookies) != 1 || !matches(cookies[0].Value, g.session) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		request := r.Clone(r.Context())
		request.Header.Del("Cookie")
		request.Header.Del("Origin")
		request.Header.Set("Authorization", "Bearer "+g.bearer)
		g.backend.ServeHTTP(w, request)
	default:
		g.serveAsset(w, r)
	}
}

func (g *gateway) serveAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", 405)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" {
		name = "index.html"
	}
	if !fs.ValidPath(name) || strings.ContainsAny(name, `\:`) {
		http.NotFound(w, r)
		return
	}
	file, err := g.dist.Open(filepath.FromSlash(name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, name, info.ModTime(), file)
}
