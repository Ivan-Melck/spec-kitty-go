package doctrine_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// docAPI serves the org doctrine HTTP API contract.
func docAPI() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/artifact-types":
			w.Write([]byte(`{"types": ["directives", "tactics"]}`))
		case "/artifacts/directives":
			w.Write([]byte(`{"artifacts": [{"filename": "a.directive.yaml", "content": "id: DIR_A\n"}, {"filename": "../evil.yaml", "content": "x"}]}`))
		case "/artifacts/tactics":
			w.Write([]byte(`{"artifacts": [{"filename": "t.tactic.yaml", "content": "id: t\n"}]}`))
		case "/version":
			w.Write([]byte(`{"version": "2.3.4"}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func writeGitSource(t *testing.T, s *testkit.Sandbox) {
	t.Helper()
	s.WriteFile("src/tactics/t.tactic.yaml", "id: tac-x\nschema_version: \"1.0\"\nname: X\nsteps:\n  - title: s\n")
	s.WriteFile("src/directives/d.directive.yaml", "x: 1\n")
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-q", "-m", "one"}, {"tag", "v1.0"}} {
		if r := s.Exec("src", "git", args...); r.Code != 0 {
			t.Fatalf("git %v: %s", args, r.Stderr)
		}
	}
}

func fetchConfig(s *testkit.Sandbox, api string) string {
	return "charter_packs:\n  org:\n    packs:\n" +
		"    - name: gitpack\n      local_path: packs/gitpack\n      source_type: git\n      url: " + filepath.Join(s.Dir, "src") + "\n" +
		"    - name: apipack\n      local_path: packs/apipack\n      source_type: api\n      url: " + api + "\n" +
		"    - name: nosrc\n      local_path: packs/nosrc\n" +
		"    - name: badapi\n      local_path: packs/badapi\n      source_type: api\n      url: http://127.0.0.1:1\n"
}

func TestParityCharterFetch(t *testing.T) {
	t.Parallel()
	srv := docAPI()
	defer srv.Close()
	p := newOraclePair(t)
	comparePackTool(t, p, "charter", "fetch")
	p.Both(func(s *testkit.Sandbox) {
		writeGitSource(t, s)
		appendConfig(s, fetchConfig(s, srv.URL))
	})
	for _, args := range [][]string{
		{"charter", "fetch", "--dry-run"},
		{"charter", "fetch", "--pack", "nope"},
		{"charter", "fetch", "--pack", "gitpack"},
		{"charter", "fetch", "--pack", "gitpack"},
	} {
		comparePackTool(t, p, args...)
	}
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("packs/gitpack/tactics/t.tactic.yaml", s.ReadFile("packs/gitpack/tactics/t.tactic.yaml")+"local: 1\n")
	})
	comparePackTool(t, p, "charter", "fetch", "--pack", "gitpack")
	p.Both(func(s *testkit.Sandbox) { s.Exec("packs/gitpack", "git", "checkout", "-q", "--", ".") })
	comparePackTool(t, p, "charter", "fetch", "--pack", "apipack")
	for _, rel := range []string{"packs/apipack/directives/a.directive.yaml", "packs/apipack/tactics/t.tactic.yaml", "packs/apipack/evil.yaml"} {
		compareFileBoth(t, p, rel)
	}
	g := reManifestVol.ReplaceAllString(p.Go.ReadFile("packs/apipack/pack-manifest.yaml"), "$1: <V>")
	o := reManifestVol.ReplaceAllString(p.Oracle.ReadFile("packs/apipack/pack-manifest.yaml"), "$1: <V>")
	if p.Go.Normalize(g) != p.Oracle.Normalize(o) {
		t.Errorf("api pack manifest differs\n%s", testkit.Diff(o, g))
	}
	comparePackTool(t, p, "charter", "fetch", "--pack", "nosrc")
	comparePackTool(t, p, "charter", "fetch", "--pack", "badapi")
	comparePackTool(t, p, "charter", "fetch")
}

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestCharterFetchHTTPSGoOnly(t *testing.T) {
	bundle := tarGz(t, map[string]string{"pack-v1/directives/x.directive.yaml": "id: X\n", "pack-v1/tactics/y.tactic.yaml": "id: y\n"})
	hits := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(304)
			return
		}
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("ETag", `"v1"`)
		w.Write(bundle)
	}))
	defer srv.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newProject(t)
	appendConfig(s, "charter_packs:\n  org:\n    packs:\n    - name: bundle\n      local_path: packs/bundle\n      source_type: https\n      url: "+srv.URL+"/pack.tar.gz\n      ref: v1.0\n")
	s.Env = append(s.Env, "SSL_CERT_FILE="+ca)
	r := s.Run("charter", "fetch")
	if r.Code != 0 || !strings.Contains(r.Stdout, "Pack 'bundle': 2 artifacts") || !strings.Contains(r.Stdout, "Version: v1.0") {
		t.Fatalf("https fetch: %d %s %s", r.Code, r.Stdout, r.Stderr)
	}
	if !s.Exists("packs/bundle/directives/x.directive.yaml") || !strings.Contains(s.ReadFile("packs/bundle/pack-manifest.yaml"), "etag: '\"v1\"'") {
		t.Fatalf("snapshot: %s", s.ReadFile("packs/bundle/pack-manifest.yaml"))
	}
	r = s.Run("charter", "fetch")
	if r.Code != 0 || !strings.Contains(r.Stdout, "(unchanged)") {
		t.Fatalf("conditional refetch: %d %s", r.Code, r.Stdout)
	}
}

func TestParityCharterFetchHTTPS(t *testing.T) {
	t.Parallel()
	bundle := tarGz(t, map[string]string{"pack-v1/directives/x.directive.yaml": "id: X\n", "pack-v1/tactics/y.tactic.yaml": "id: y\n"})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/denied.tar.gz":
			w.WriteHeader(403)
		case r.URL.Path == "/missing.tar.gz":
			http.NotFound(w, r)
		case r.URL.Path == "/plain":
			w.Write([]byte("not an archive"))
		case r.Header.Get("If-None-Match") == `"v1"`:
			w.WriteHeader(304)
		default:
			w.Header().Set("Content-Type", "application/gzip")
			w.Header().Set("ETag", `"v1"`)
			w.Write(bundle)
		}
	}))
	defer srv.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o644); err != nil {
		t.Fatal(err)
	}
	p := newOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		s.Env = append(s.Env, "SSL_CERT_FILE="+ca, "REQUESTS_CA_BUNDLE="+ca)
		cfg := "charter_packs:\n  org:\n    packs:\n"
		for _, n := range []string{"pack", "denied", "missing", "plain"} {
			suffix := ".tar.gz"
			if n == "plain" {
				suffix = ""
			}
			cfg += "    - name: " + n + "\n      local_path: packs/" + n + "\n      source_type: https\n      url: " + srv.URL + "/" + n + suffix + "\n"
		}
		cfg += "    - name: insecure\n      local_path: packs/insecure\n      source_type: https\n      url: http://127.0.0.1:1/x.tar.gz\n"
		appendConfig(s, cfg)
	})
	for _, n := range []string{"pack", "pack", "denied", "missing", "plain", "insecure"} {
		comparePackTool(t, p, "charter", "fetch", "--pack", n)
	}
	g := reManifestVol.ReplaceAllString(p.Go.ReadFile("packs/pack/pack-manifest.yaml"), "$1: <V>")
	o := reManifestVol.ReplaceAllString(p.Oracle.ReadFile("packs/pack/pack-manifest.yaml"), "$1: <V>")
	if p.Go.Normalize(g) != p.Oracle.Normalize(o) {
		t.Errorf("https pack manifest differs\n%s", testkit.Diff(o, g))
	}
}
