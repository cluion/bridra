package host

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWindowSession(t *testing.T) {
	const address = "127.0.0.1:12345"
	const origin = "http://" + address
	forwarded := 0
	g := newGateway(address, "private", nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded++
		if r.Header.Get("Authorization") != "Bearer private" || r.Header.Get("Cookie") != "" || r.Header.Get("Origin") != "" {
			t.Error("unexpected credentials forwarded")
		}
		w.WriteHeader(200)
	}))
	call := func(path string, headers http.Header) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", origin+path, nil)
		req.Header = headers
		w := httptest.NewRecorder()
		g.ServeHTTP(w, req)
		return w
	}
	proof := http.Header{"Authorization": []string{"Bearer " + g.proof}}
	if w := call("/rpc", nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	browserProof := proof.Clone()
	browserProof.Set("Origin", origin)
	if w := call("/__bootstrap", browserProof); w.Code != 401 {
		t.Fatal(w.Code)
	}
	badProof := proof.Clone()
	badProof.Set("Authorization", "Bearer guessed")
	if w := call("/__bootstrap", badProof); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := call("/__bootstrap", proof)
	var reply struct {
		Version int
		Session string
	}
	err := json.Unmarshal(w.Body.Bytes(), &reply)
	if w.Code != 200 || err != nil || reply.Version != 1 || len(reply.Session) != 64 {
		t.Fatal("handshake failed")
	}
	if w := call("/__bootstrap", proof); w.Code != 401 {
		t.Fatal("accepted replay")
	}
	cookie := "bridra_wk=" + reply.Session
	for _, headers := range []http.Header{
		{"Origin": []string{origin}},
		{"Cookie": []string{cookie}},
		{"Cookie": []string{cookie}, "Origin": []string{"http://attacker.test"}},
		{"Cookie": []string{cookie}, "Origin": []string{"null"}},
		{"Cookie": []string{cookie + "; " + cookie}, "Origin": []string{origin}},
	} {
		if w := call("/rpc", headers); w.Code != 401 {
			t.Errorf("unauthorized RPC = %d", w.Code)
		}
	}
	if w := call("/rpc", http.Header{"Cookie": []string{cookie}, "Origin": []string{origin}}); w.Code != 200 || forwarded != 1 {
		t.Fatal("expected one authenticated RPC")
	}
	expired := newGateway(address, "private", nil, nil)
	expired.expires = time.Now().Add(-time.Second)
	g = expired
	if w := call("/__bootstrap", http.Header{"Authorization": []string{"Bearer " + g.proof}}); w.Code != 401 {
		t.Fatal("accepted expired proof")
	}
}

func TestOnlyServePackagedFilesOnLoopbackHost(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<main>中文</main>"), 0644); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	g := newGateway("127.0.0.1:12345", "unused", root, nil)
	for _, tc := range []struct {
		url  string
		code int
	}{{g.origin + "/", 200}, {"http://attacker.test/", 403}, {g.origin + "/../manifest.json", 404}, {g.origin + "/unknown.js", 404}} {
		w := httptest.NewRecorder()
		g.ServeHTTP(w, httptest.NewRequest("GET", tc.url, nil))
		if w.Code != tc.code {
			t.Errorf("%s = %d", tc.url, w.Code)
		}
		if w.Code == 200 && (!strings.Contains(w.Body.String(), "中文") || w.Header().Get("Content-Security-Policy") != policy) {
			t.Error("wrong content or policy")
		}
	}
}
