package charter

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
)

// This file ports `charter fetch`: specify_cli.charter_packs.snapshot
// (fetch_pack, write_snapshot) and the git / https(artifactory) / api
// sources.

// FetchResult is the outcome of fetching one pack.
type FetchResult struct {
	OK               bool
	ArtifactsWritten int
	PackVersion      *string
	Errors           []string
	Unchanged        bool
	ETag             *string
}

func fetchErr(msg string) *FetchResult { return &FetchResult{Errors: []string{msg}} }

// ---------------------------------------------------------------- git

type gitSource struct{ url, ref string }

func runGit(args ...string) (string, string, int) {
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	code := 0
	if err != nil {
		code = 1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
	}
	return so.String(), se.String(), code
}

var reOAuthToken = regexp.MustCompile(`oauth2:[^\s'"]+@([^@\s/'"]+(?::\d+)?(?:/|$))`)

func redactGitTokens(s string) string {
	return reOAuthToken.ReplaceAllString(s, "oauth2:<redacted>@$1")
}

func (g *gitSource) injectToken(u string) string {
	tok := os.Getenv("GIT_TOKEN")
	if tok == "" || !strings.HasPrefix(u, "https://") {
		return u
	}
	return strings.Replace(u, "https://", "https://oauth2:"+url.QueryEscape(tok)+"@", 1)
}

var reInjected = regexp.MustCompile(`^https://oauth2:[^@/]+@`)

func (g *gitSource) fetch(target string) *FetchResult {
	if _, err := os.Stat(filepath.Join(target, ".git")); err == nil {
		so, _, code := runGit("-C", target, "remote", "get-url", "origin")
		if code != 0 {
			return fetchErr(fmt.Sprintf("Refusing to update pack at %s: could not read the installed clone's origin", target))
		}
		if reInjected.ReplaceAllString(strings.TrimSpace(so), "https://") != reInjected.ReplaceAllString(g.url, "https://") {
			return fetchErr(fmt.Sprintf("Refusing to update pack at %s: configured URL differs from the installed clone's origin. Move the existing clone aside or change its origin to the configured URL, then re-fetch.", target))
		}
		return g.update(target)
	}
	return g.firstInstall(target, g.injectToken(g.url))
}

func dirNonEmpty(p string) bool {
	st, err := os.Stat(p)
	if err != nil {
		return false
	}
	if !st.IsDir() {
		return true
	}
	es, _ := os.ReadDir(p)
	return len(es) > 0
}

func (g *gitSource) firstInstall(target, u string) *FetchResult {
	if dirNonEmpty(target) {
		return fetchErr(fmt.Sprintf("Refusing to clone into non-empty directory %s: it holds existing content that a clone would overwrite. Move or remove it first.", target))
	}
	_ = os.MkdirAll(filepath.Dir(target), 0o755)
	tmp := filepath.Join(filepath.Dir(target), ".tmp-"+randHex(16))
	_, se, code := runGit("clone", u, tmp)
	if code != 0 {
		os.RemoveAll(tmp)
		msg := redactGitTokens(strings.TrimSpace(se))
		if msg == "" {
			msg = "git clone failed"
		}
		return fetchErr(msg)
	}
	if g.ref != "" {
		_, se, code := runGit("-C", tmp, "checkout", g.ref)
		if code != 0 {
			os.RemoveAll(tmp)
			msg := redactGitTokens(strings.TrimSpace(se))
			if msg == "" {
				msg = "git checkout failed"
			}
			return fetchErr(msg)
		}
	}
	var old string
	if _, err := os.Stat(target); err == nil {
		old = filepath.Join(filepath.Dir(target), ".old-"+filepath.Base(target)+"-"+randHex(16))
		if err := os.Rename(target, old); err != nil {
			os.RemoveAll(tmp)
			return fetchErr(fmt.Sprintf("Failed to install cloned pack at %s: %v", target, err))
		}
	}
	if err := os.Rename(tmp, target); err != nil {
		os.RemoveAll(tmp)
		msg := fmt.Sprintf("Failed to install cloned pack at %s: %v", target, err)
		if old != "" {
			if rerr := os.Rename(old, target); rerr != nil {
				msg += fmt.Sprintf("; automatic restore failed — the previous content is preserved at %s: %v", old, rerr)
			}
		}
		return fetchErr(msg)
	}
	if old != "" {
		os.RemoveAll(old)
	}
	return g.success(target)
}

func (g *gitSource) update(target string) *FetchResult {
	if _, se, code := runGit("-C", target, "fetch", "--tags", "origin"); code != 0 {
		msg := redactGitTokens(strings.TrimSpace(se))
		if msg == "" {
			msg = "git fetch failed"
		}
		return fetchErr(msg)
	}
	reset := "origin/HEAD"
	if g.ref != "" {
		if _, _, code := runGit("-C", target, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+g.ref); code == 0 {
			reset = "origin/" + g.ref
		} else {
			reset = g.ref
		}
	}
	if refusal := g.localChangesRefusal(target, reset); refusal != "" {
		return fetchErr(fmt.Sprintf("Refusing to update pack at %s: %s. The local content is preserved in place (no reset performed) — resolve or relocate it, then re-fetch.", target, refusal))
	}
	if _, se, code := runGit("-C", target, "reset", "--hard", reset); code != 0 {
		msg := redactGitTokens(strings.TrimSpace(se))
		if msg == "" {
			msg = "git reset failed"
		}
		return fetchErr(msg)
	}
	return g.success(target)
}

func (g *gitSource) localChangesRefusal(target, reset string) string {
	tree, se, code := runGit("-C", target, "ls-tree", "-r", "-z", "--name-only", reset)
	if code != 0 {
		return fmt.Sprintf("could not inspect the target tree %s (Could not inspect target tree %s: %s)", dct.PyRepr(reset), reset, strings.TrimSpace(se))
	}
	targetPaths := map[string]bool{}
	for _, p := range strings.Split(tree, "\x00") {
		if p != "" {
			targetPaths[p] = true
		}
	}
	st, se, code := runGit("-C", target, "status", "--porcelain=v1", "--ignored", "-uall")
	if code != 0 {
		return fmt.Sprintf("could not inspect the working tree (%s)", strings.TrimSpace(se))
	}
	var dirty []string
	for _, l := range strings.Split(strings.TrimRight(st, "\n"), "\n") {
		if len(l) < 4 {
			continue
		}
		code, path := l[:2], l[3:]
		if code == "??" || code == "!!" {
			obstructs := false
			for tp := range targetPaths {
				if tp == path || strings.HasPrefix(tp, path+"/") || strings.HasPrefix(path, tp+"/") {
					obstructs = true
					break
				}
			}
			if !obstructs {
				continue
			}
		}
		dirty = append(dirty, l)
	}
	if len(dirty) > 0 {
		var b strings.Builder
		b.WriteString("the working tree holds uncommitted local changes")
		for _, d := range dirty {
			b.WriteString("\n    " + d)
		}
		return b.String()
	}
	so, se, code := runGit("-C", target, "rev-list", "--count", reset+"..HEAD")
	if code != 0 {
		return fmt.Sprintf("could not determine whether local commits are ahead of %s (%s)", dct.PyRepr(reset), strings.TrimSpace(se))
	}
	n := strings.TrimSpace(so)
	if c, err := strconv.Atoi(n); err == nil && c > 0 {
		return fmt.Sprintf("%s local commit(s) are ahead of %s that a reset would orphan", n, dct.PyRepr(reset))
	}
	return ""
}

func countYAMLExcludingGit(dir string) int {
	n := 0
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".yaml") {
			n++
		}
		return nil
	})
	return n
}

func (g *gitSource) success(target string) *FetchResult {
	r := &FetchResult{OK: true, ArtifactsWritten: countYAMLExcludingGit(target)}
	if so, _, code := runGit("-C", target, "describe", "--tags", "--always"); code == 0 {
		if v := strings.TrimSpace(so); v != "" {
			r.PackVersion = &v
		}
	}
	return r
}

// ---------------------------------------------------------------- https

const (
	maxArchiveBytes   = 100 * 1024 * 1024
	maxExtractedBytes = 512 * 1024 * 1024
	maxArchiveMembers = 10000
)

type httpsSource struct {
	url, ref, sourceType string
	ifNoneMatch          string
}

var rePctBad = regexp.MustCompile(`%([^0-9A-Fa-f]|[0-9A-Fa-f][^0-9A-Fa-f]|.?$)`)

func canonicalHTTPSURL(u string) string {
	if strings.Contains(u, `\`) || rePctBad.MatchString(u) {
		return ""
	}
	for _, c := range u {
		if c < 32 || c == 127 {
			return ""
		}
	}
	p, err := url.Parse(u)
	if err != nil || p.Scheme != "https" || p.Hostname() == "" {
		return ""
	}
	if p.Path == "" {
		p.Path = "/"
	}
	return p.String()
}

func safeURLForError(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return "<invalid HTTPS URL>"
	}
	host := p.Hostname()
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port := p.Port(); port != "" {
		host += ":" + port
	}
	return p.Scheme + "://" + host + p.EscapedPath()
}

func orgAuthHeaders() http.Header {
	h := http.Header{}
	if v := os.Getenv("SPEC_KITTY_ORG_AUTH_HEADER"); v != "" {
		h.Set("Authorization", v)
	} else if t := os.Getenv("SPEC_KITTY_ORG_TOKEN"); t != "" {
		h.Set("Authorization", "Bearer "+t)
	}
	return h
}

var httpClient = &http.Client{Timeout: 30 * time.Second}

func (s *httpsSource) headers() http.Header {
	h := orgAuthHeaders()
	if s.ifNoneMatch != "" {
		h.Set("If-None-Match", s.ifNoneMatch)
	}
	return h
}

func (s *httpsSource) get(u string) (*http.Response, error) {
	do := func() (*http.Response, error) {
		req, err := http.NewRequest("GET", u, nil)
		if err != nil {
			return nil, err
		}
		req.Header = s.headers()
		return httpClient.Do(req)
	}
	resp, err := do()
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 500 && resp.StatusCode < 600 {
		resp.Body.Close()
		time.Sleep(2 * time.Second)
		return do()
	}
	if resp.StatusCode == 429 {
		d := 2.0
		if v, err := strconv.ParseFloat(resp.Header.Get("Retry-After"), 64); err == nil && v >= 0 {
			d = v
		}
		resp.Body.Close()
		time.Sleep(time.Duration(d * float64(time.Second)))
		return do()
	}
	return resp, nil
}

func netErrName(err error) string {
	if ue, ok := err.(*url.Error); ok {
		if ue.Timeout() {
			return "ReadTimeout"
		}
		if _, ok := ue.Err.(*net.OpError); ok {
			return "ConnectionError"
		}
		return "ConnectionError"
	}
	return "RequestException"
}

type artifactoryItem struct{ aqlURL, repo, path, name string }

func parseArtifactoryItem(u string) *artifactoryItem {
	p, err := url.Parse(u)
	if err != nil || !strings.Contains(p.EscapedPath(), "/artifactory/") {
		return nil
	}
	raw := p.EscapedPath()
	i := strings.Index(raw, "/artifactory/")
	prefix, item := raw[:i], raw[i+len("/artifactory/"):]
	segs := strings.Split(item, "/")
	var dec []string
	for _, sgm := range segs {
		if sgm == "" {
			return nil
		}
		d, err := url.PathUnescape(sgm)
		if err != nil || d == "." || d == ".." || strings.ContainsAny(d, `/\`) {
			return nil
		}
		dec = append(dec, d)
	}
	if len(dec) < 2 {
		return nil
	}
	path := strings.Join(dec[1:len(dec)-1], "/")
	if path == "" {
		path = "."
	}
	host := p.Hostname()
	if port := p.Port(); port != "" {
		host += ":" + port
	}
	return &artifactoryItem{p.Scheme + "://" + host + prefix + "/artifactory/api/search/aql", dec[0], path, dec[len(dec)-1]}
}

func (s *httpsSource) fetch(target string) *FetchResult {
	_ = os.MkdirAll(target, 0o755)
	cu := canonicalHTTPSURL(s.url)
	if cu == "" {
		return fetchErr("HTTPS doctrine source URL is invalid.")
	}
	isArt := s.sourceType == "artifactory" || strings.Contains(func() string { p, _ := url.Parse(cu); return p.Path }(), "/artifactory/")
	item := parseArtifactoryItem(cu)
	if isArt && item == nil {
		return fetchErr("Artifactory source requires a valid Artifactory item URL containing /artifactory/<repo>/<item>.")
	}
	resp, err := s.get(cu)
	if err != nil {
		return fetchErr(fmt.Sprintf("Network error fetching %s: %s", safeURLForError(cu), netErrName(err)))
	}
	defer resp.Body.Close()
	if resp.StatusCode == 304 {
		if s.ifNoneMatch == "" {
			return fetchErr("Remote returned HTTP 304 without an If-None-Match validator.")
		}
		r := &FetchResult{OK: true, Unchanged: true}
		if s.ref != "" {
			r.PackVersion = &s.ref
		}
		e := s.ifNoneMatch
		r.ETag = &e
		return r
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return fetchErr("Authentication failed. Set SPEC_KITTY_ORG_TOKEN to a valid bearer token for the doctrine bundle endpoint.")
	}
	if resp.StatusCode >= 400 {
		return fetchErr(fmt.Sprintf("HTTP %d fetching %s.", resp.StatusCode, safeURLForError(cu)))
	}
	kind := ""
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	switch {
	case strings.Contains(ct, "gzip") || strings.Contains(ct, "x-tar"):
		kind = "tar.gz"
	case strings.Contains(ct, "zip"):
		kind = "zip"
	default:
		lu := strings.ToLower(resp.Request.URL.String())
		if strings.HasSuffix(lu, ".tar.gz") || strings.HasSuffix(lu, ".tgz") {
			kind = "tar.gz"
		} else if strings.HasSuffix(lu, ".zip") {
			kind = "zip"
		}
	}
	if kind == "" {
		ctv := resp.Header.Get("Content-Type")
		if ctv == "" {
			ctv = "<missing>"
		}
		return fetchErr(fmt.Sprintf("Could not determine archive format from Content-Type (%s) or URL suffix.", ctv))
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		if n, err := strconv.ParseInt(cl, 10, 64); err == nil && n > maxArchiveBytes {
			return fetchErr(fmt.Sprintf("Failed to buffer archive: Archive exceeds raw byte limit: %d > %d", n, maxArchiveBytes))
		}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxArchiveBytes+1))
	if err != nil {
		return fetchErr("Failed to buffer archive: " + netErrName(err))
	}
	if len(body) > maxArchiveBytes {
		return fetchErr(fmt.Sprintf("Failed to buffer archive: Archive exceeds raw byte limit: %d > %d", len(body), maxArchiveBytes))
	}
	sum := sha256.Sum256(body)
	var version *string
	if item != nil {
		v, sha, msg := s.artifactoryMetadata(item, cu)
		if msg != "" {
			return fetchErr(msg)
		}
		if !hmac.Equal([]byte(sha), []byte(hex.EncodeToString(sum[:]))) {
			return fetchErr("JFrog metadata checksum does not match the downloaded archive; the mutable artifact changed during fetch.")
		}
		version = &v
	}
	n, err := extractArchive(body, target, kind)
	if err != nil {
		return fetchErr("Archive extraction failed: " + err.Error())
	}
	r := &FetchResult{OK: true, ArtifactsWritten: n}
	if version != nil {
		r.PackVersion = version
	} else if s.ref != "" {
		r.PackVersion = &s.ref
	}
	if e := resp.Header.Get("ETag"); e != "" {
		r.ETag = &e
	}
	return r
}

func (s *httpsSource) artifactoryMetadata(item *artifactoryItem, cu string) (string, string, string) {
	crit, _ := json.Marshal(map[string]string{"repo": item.repo, "path": item.path, "name": item.name, "type": "file"})
	// json.dumps(separators=(",", ":")) keeps insertion order repo,path,name,type.
	crit = []byte(fmt.Sprintf(`{"repo":%s,"path":%s,"name":%s,"type":"file"}`, jsonStr(item.repo), jsonStr(item.path), jsonStr(item.name)))
	q := fmt.Sprintf(`items.find(%s).include("repo","path","name","sha256","virtual_repos","@version")`, crit)
	do := func() (*http.Response, error) {
		req, err := http.NewRequest("POST", item.aqlURL, strings.NewReader(q))
		if err != nil {
			return nil, err
		}
		h := orgAuthHeaders()
		h.Set("Content-Type", "text/plain")
		req.Header = h
		return httpClient.Do(req)
	}
	resp, err := do()
	if err != nil {
		return "", "", "Failed to read JFrog AQL metadata: " + netErrName(err)
	}
	if resp.StatusCode == 429 || (resp.StatusCode >= 500 && resp.StatusCode < 600) {
		d := time.Second
		if resp.StatusCode >= 500 {
			d = 2 * time.Second
		}
		resp.Body.Close()
		time.Sleep(d)
		resp, err = do()
		if err != nil {
			return "", "", "Failed to read JFrog AQL metadata: " + netErrName(err)
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", "", fmt.Sprintf("Failed to read JFrog AQL metadata: HTTP %d", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", "", "Failed to read JFrog AQL metadata: invalid JSON response"
	}
	bad := "JFrog AQL did not return the exact artifact with one non-empty version property and a valid SHA-256 checksum: " + safeURLForError(cu)
	results, ok := payload["results"].([]any)
	if !ok || len(results) != 1 {
		return "", "", bad
	}
	res, ok := results[0].(map[string]any)
	if !ok {
		return "", "", bad
	}
	repoOK := res["repo"] == item.repo
	if vr, ok := res["virtual_repos"].([]any); ok {
		for _, x := range vr {
			if x == item.repo {
				repoOK = true
			}
		}
	}
	if !repoOK || res["path"] != item.path || res["name"] != item.name {
		return "", "", bad
	}
	cs, ok := res["sha256"].(string)
	if !ok {
		return "", "", bad
	}
	cs = strings.ToLower(strings.TrimSpace(cs))
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(cs) {
		return "", "", bad
	}
	props, ok := res["properties"].([]any)
	if !ok {
		return "", "", bad
	}
	var versions []string
	for _, p := range props {
		pm, ok := p.(map[string]any)
		if !ok || pm["key"] != "version" {
			continue
		}
		if v, ok := pm["value"].(string); ok && strings.TrimSpace(v) != "" {
			versions = append(versions, strings.TrimSpace(v))
		}
	}
	if len(versions) != 1 {
		return "", "", bad
	}
	return versions[0], cs, ""
}

func jsonStr(s string) string { return jsonx.QuoteString(s, true) }

func within(base, p string) bool {
	r, err := filepath.Rel(base, p)
	return err == nil && r != ".." && !strings.HasPrefix(r, "../")
}

func extractArchive(body []byte, target, kind string) (int, error) {
	base := pyResolve(target)
	if kind == "tar.gz" {
		gz, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return 0, err
		}
		tr := tar.NewReader(gz)
		type ent struct {
			hdr  *tar.Header
			data []byte
		}
		var ents []ent
		var total int64
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return 0, err
			}
			ents = append(ents, ent{h, nil})
			if len(ents) > maxArchiveMembers {
				return 0, fmt.Errorf("Archive exceeds member count limit: %d > %d", len(ents), maxArchiveMembers)
			}
			switch h.Typeflag {
			case tar.TypeSymlink, tar.TypeLink:
				return 0, fmt.Errorf("Refusing symlink/hardlink entry: %s", h.Name)
			case tar.TypeReg, tar.TypeRegA, tar.TypeDir:
			default:
				return 0, fmt.Errorf("Refusing non-file/non-dir entry: %s (type=%s)", h.Name, pyBytesRepr(h.Typeflag))
			}
			if h.Typeflag != tar.TypeDir {
				total += h.Size
				if total > maxExtractedBytes {
					return 0, fmt.Errorf("Archive exceeds extracted byte limit: %d > %d", total, maxExtractedBytes)
				}
				b, err := io.ReadAll(tr)
				if err != nil {
					return 0, err
				}
				ents[len(ents)-1].data = b
			}
			if !within(base, pyResolve(filepath.Join(target, h.Name))) {
				return 0, fmt.Errorf("Refusing path traversal entry: %s", h.Name)
			}
		}
		for _, e := range ents {
			p := filepath.Join(target, e.hdr.Name)
			if e.hdr.Typeflag == tar.TypeDir {
				_ = os.MkdirAll(p, 0o755)
				continue
			}
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			if err := os.WriteFile(p, e.data, 0o644); err != nil {
				return 0, err
			}
		}
	} else {
		zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			return 0, fmt.Errorf("File is not a zip file")
		}
		if len(zr.File) > maxArchiveMembers {
			return 0, fmt.Errorf("Archive exceeds member count limit: %d > %d", len(zr.File), maxArchiveMembers)
		}
		var total uint64
		for _, f := range zr.File {
			if !f.FileInfo().IsDir() {
				total += f.UncompressedSize64
				if total > maxExtractedBytes {
					return 0, fmt.Errorf("Archive exceeds extracted byte limit: %d > %d", total, maxExtractedBytes)
				}
			}
			if !within(base, pyResolve(filepath.Join(target, f.Name))) {
				return 0, fmt.Errorf("Refusing path traversal entry: %s", f.Name)
			}
		}
		for _, f := range zr.File {
			p := filepath.Join(target, f.Name)
			if f.FileInfo().IsDir() {
				_ = os.MkdirAll(p, 0o755)
				continue
			}
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			rc, err := f.Open()
			if err != nil {
				return 0, err
			}
			b, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return 0, err
			}
			if err := os.WriteFile(p, b, 0o644); err != nil {
				return 0, err
			}
		}
	}
	// flatten a single top-level directory
	es, _ := os.ReadDir(target)
	if len(es) == 1 && es[0].IsDir() {
		inner := filepath.Join(target, es[0].Name())
		kids, _ := os.ReadDir(inner)
		for _, k := range kids {
			_ = os.Rename(filepath.Join(inner, k.Name()), filepath.Join(target, k.Name()))
		}
		_ = os.Remove(inner)
	}
	return len(rglob(target, ".yaml")), nil
}

func pyBytesRepr(b byte) string { return fmt.Sprintf("b'%c'", b) }

// ---------------------------------------------------------------- api

type apiSource struct{ url, ref string }

var safeFilename = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

type apiResp struct {
	code int
	body []byte
	hdr  http.Header
}

func (a *apiSource) request(path string) (*apiResp, string) {
	endpoint := strings.TrimRight(a.url, "/") + path
	do := func() (*apiResp, error) {
		req, err := http.NewRequest("GET", endpoint, nil)
		if err != nil {
			return nil, err
		}
		h := http.Header{}
		if v := os.Getenv("SPEC_KITTY_ORG_AUTH_HEADER"); v != "" {
			h.Set("Authorization", v)
		} else if t := os.Getenv("SPEC_KITTY_ORG_TOKEN"); t != "" {
			h.Set("Authorization", "Bearer "+t)
		}
		h.Set("Accept", "application/json")
		req.Header = h
		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return &apiResp{resp.StatusCode, b, resp.Header}, nil
	}
	r, err := do()
	if err != nil {
		return nil, fmt.Sprintf("Network error calling %s: %s", endpoint, requestsErrorText(endpoint, err))
	}
	if r.code == 429 {
		d := 2.0
		if v, err := strconv.ParseFloat(r.hdr.Get("Retry-After"), 64); err == nil && v >= 0 {
			d = v
		}
		time.Sleep(time.Duration(d * float64(time.Second)))
		r, err = do()
		if err != nil {
			return nil, fmt.Sprintf("Network error on 429-retry for %s: %s", endpoint, requestsErrorText(endpoint, err))
		}
	}
	return r, ""
}

const apiAuthFailed = "Authentication failed against the doctrine API. Set SPEC_KITTY_ORG_TOKEN (or SPEC_KITTY_ORG_AUTH_HEADER for custom auth schemes) and retry."

func (a *apiSource) fetch(target string) *FetchResult {
	_ = os.MkdirAll(target, 0o755)
	types, msg := a.request("/artifact-types")
	if msg != "" {
		return fetchErr(msg)
	}
	var artTypes []string
	if types.code == 404 {
		artTypes = LayeredRepositoryKindPlurals
	} else {
		if types.code == 401 || types.code == 403 {
			return fetchErr(apiAuthFailed)
		}
		if types.code >= 400 {
			return fetchErr(fmt.Sprintf("GET /artifact-types failed: %d", types.code))
		}
		var payload map[string]any
		_ = json.Unmarshal(types.body, &payload)
		if l, ok := payload["types"].([]any); ok && len(l) > 0 {
			for _, x := range l {
				artTypes = append(artTypes, fmt.Sprint(x))
			}
		} else {
			artTypes = LayeredRepositoryKindPlurals
		}
	}
	var errs []string
	total := 0
	writeItems := func(sub string, items []any) int {
		dir := filepath.Join(target, sub)
		_ = os.MkdirAll(dir, 0o755)
		n := 0
		for _, it := range items {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			fn, _ := m["filename"].(string)
			content, has := m["content"]
			if fn == "" || !has || content == nil || !safeFilename.MatchString(fn) {
				continue
			}
			cs, ok := content.(string)
			if !ok {
				cs = fmt.Sprint(content)
			}
			if os.WriteFile(filepath.Join(dir, fn), []byte(cs), 0o644) == nil {
				n++
			}
		}
		return n
	}
	for _, t := range artTypes {
		r, msg := a.request("/artifacts/" + t)
		if msg != "" {
			errs = append(errs, msg)
			continue
		}
		if r.code == 404 {
			continue
		}
		if r.code == 401 || r.code == 403 {
			errs = append(errs, apiAuthFailed)
			continue
		}
		if r.code >= 400 {
			errs = append(errs, fmt.Sprintf("GET /artifacts/%s failed: %d", t, r.code))
			continue
		}
		var payload map[string]any
		if json.Unmarshal(r.body, &payload) != nil {
			errs = append(errs, fmt.Sprintf("GET /artifacts/%s: response is not JSON", t))
			continue
		}
		items, _ := payload["artifacts"].([]any)
		total += writeItems(t, items)
	}
	if r, msg := a.request("/drg-extensions"); msg != "" {
		errs = append(errs, msg)
	} else if r.code != 404 {
		switch {
		case r.code == 401 || r.code == 403:
			errs = append(errs, apiAuthFailed)
		case r.code >= 400:
			errs = append(errs, fmt.Sprintf("GET /drg-extensions failed: %d", r.code))
		default:
			var payload map[string]any
			if json.Unmarshal(r.body, &payload) != nil {
				errs = append(errs, "GET /drg-extensions: response is not JSON")
			} else if frags, ok := payload["fragments"].([]any); ok && len(frags) > 0 {
				total += writeItems("drg", frags)
			}
		}
	}
	var version *string
	ref := func() *string {
		if a.ref == "" {
			return nil
		}
		return &a.ref
	}
	if r, msg := a.request("/version"); msg != "" {
		version = ref()
	} else if r.code == 404 {
		v := r.hdr.Get("Date")
		if v == "" {
			v = a.ref
		}
		if v == "" {
			v = clock.Now().UTC().Format("2006-01-02T15:04:05Z")
		}
		version = &v
	} else if r.code >= 400 {
		version = ref()
	} else {
		var payload map[string]any
		if json.Unmarshal(r.body, &payload) != nil {
			version = ref()
		} else if v, ok := payload["version"].(string); ok && v != "" {
			version = &v
		} else {
			version = ref()
		}
	}
	if len(errs) > 0 {
		return &FetchResult{ArtifactsWritten: total, PackVersion: version, Errors: errs}
	}
	return &FetchResult{OK: true, ArtifactsWritten: total, PackVersion: version}
}

// ---------------------------------------------------------------- snapshot

func readExistingManifest(local string, subdir *string) *jsonx.Object {
	root := local
	if subdir != nil {
		r, err := ResolveWithinRoot(local, *subdir)
		if err != nil {
			return nil
		}
		root = r
	}
	b, err := os.ReadFile(filepath.Join(root, "pack-manifest.yaml"))
	if err != nil {
		return nil
	}
	v, err := pyyaml.SafeLoad(string(b))
	if err != nil {
		return nil
	}
	o, _ := v.(*jsonx.Object)
	return o
}

func snapshotIntact(local string, subdir *string, m *jsonx.Object) bool {
	stored := m.GetString("snapshot_sha256")
	if len(stored) != 64 || strings.Trim(stored, "0123456789abcdef") != "" {
		return false
	}
	root := local
	if subdir != nil {
		r, err := ResolveWithinRoot(local, *subdir)
		if err != nil {
			return false
		}
		root = r
	}
	return hmac.Equal([]byte(stored), []byte(snapshotSHA256(root)))
}

func manifestMatchesSource(local string, subdir *string, sourceURL, sourceType string) bool {
	m := readExistingManifest(local, subdir)
	if m == nil || m.GetString("source_type") != sourceType {
		return false
	}
	p, err := url.Parse(sourceURL)
	if err != nil || p.RawQuery != "" {
		return false
	}
	if v, _ := m.Get("source_uses_query"); v == true {
		return false
	}
	if !snapshotIntact(local, subdir, m) {
		return false
	}
	exp := StripSourceCredentials(sourceURL)
	if fp := m.GetString("source_fingerprint"); fp != "" {
		s := sha256.Sum256([]byte(exp))
		return fp == hex.EncodeToString(s[:])
	}
	return m.GetString("source_url") == exp
}

func hasRecognisedArtifacts(dir string) bool {
	es, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range es {
		if recognisedArtifactDirs[e.Name()] {
			if st, err := os.Stat(filepath.Join(dir, e.Name())); err == nil && st.IsDir() {
				return true
			}
		}
	}
	return false
}

func sumCounts(o *jsonx.Object) int {
	n := 0
	for _, kv := range o.Pairs() {
		if i, ok := kv.Value.(int); ok {
			n += i
		}
	}
	return n
}

func writeSnapshot(src *httpsSource, api *apiSource, local, sourceURL, sourceType string, subdir *string) *FetchResult {
	_ = os.MkdirAll(filepath.Dir(local), 0o755)
	resolvedURL := sourceURL
	if src != nil {
		if cu := canonicalHTTPSURL(src.url); cu != "" {
			src.url = cu
			resolvedURL = cu
		}
		if src.ifNoneMatch == "" && manifestMatchesSource(local, subdir, resolvedURL, sourceType) {
			m := readExistingManifest(local, subdir)
			isArt := src.sourceType == "artifactory" || strings.Contains(resolvedURL, "/artifactory/")
			migrate := false
			if isArt {
				v, e := strings.TrimSpace(m.GetString("pack_version")), strings.TrimSpace(m.GetString("etag"))
				migrate = v == "" || e == "" || v == e
			}
			if !migrate {
				stored := strings.TrimSpace(m.GetString("etag"))
				if stored == "" && !isArt && src.ref == "" {
					if st := m.GetString("source_type"); st == "https" || st == "artifactory" {
						stored = strings.TrimSpace(m.GetString("pack_version"))
					}
				}
				src.ifNoneMatch = stored
			}
		}
	}
	tmp := filepath.Join(filepath.Dir(local), ".tmp-"+randHex(16))
	var res *FetchResult
	if src != nil {
		res = src.fetch(tmp)
	} else {
		res = api.fetch(tmp)
	}
	if !res.OK {
		os.RemoveAll(tmp)
		return res
	}
	if res.Unchanged {
		os.RemoveAll(tmp)
		return finishUnchanged(local, res, subdir)
	}
	root := tmp
	if subdir != nil {
		r, err := ResolveWithinRoot(tmp, *subdir)
		if err != nil {
			os.RemoveAll(tmp)
			return &FetchResult{ArtifactsWritten: res.ArtifactsWritten, PackVersion: res.PackVersion, ETag: res.ETag, Errors: []string{err.Error()}}
		}
		root = r
	}
	if !hasRecognisedArtifacts(root) {
		os.RemoveAll(tmp)
		loc := " at the snapshot root"
		if subdir != nil {
			loc = " at subdir " + dct.PyRepr(*subdir)
		}
		names := make([]string, 0, len(recognisedArtifactDirs))
		for n := range recognisedArtifactDirs {
			names = append(names, n)
		}
		sortStrings(names)
		return &FetchResult{ArtifactsWritten: res.ArtifactsWritten, PackVersion: res.PackVersion, ETag: res.ETag,
			Errors: []string{"No artifact directories found in fetched snapshot" + loc + ". Expected at least one of: " + strings.Join(names, ", ")}}
	}
	WritePackManifest(root, res.PackVersion, res.ETag, resolvedURL, sourceType)
	var old string
	if _, err := os.Stat(local); err == nil {
		old = filepath.Join(filepath.Dir(local), ".old-"+filepath.Base(local)+"-"+randHex(16))
		if err := os.Rename(local, old); err != nil {
			os.RemoveAll(tmp)
			return &FetchResult{ArtifactsWritten: res.ArtifactsWritten, PackVersion: res.PackVersion, ETag: res.ETag, Errors: []string{"Failed to replace snapshot: " + err.Error()}}
		}
	}
	if err := os.Rename(tmp, local); err != nil {
		os.RemoveAll(tmp)
		errs := []string{"Failed to replace snapshot: " + err.Error()}
		if old != "" {
			if rerr := os.Rename(old, local); rerr != nil {
				errs = append(errs, fmt.Sprintf("Automatic restore failed; the previous snapshot is preserved at %s: %v", old, rerr))
			}
		}
		return &FetchResult{ArtifactsWritten: res.ArtifactsWritten, PackVersion: res.PackVersion, ETag: res.ETag, Errors: errs}
	}
	if old != "" {
		os.RemoveAll(old)
	}
	return res
}

func finishUnchanged(local string, res *FetchResult, subdir *string) *FetchResult {
	fail := func(msg string) *FetchResult {
		return &FetchResult{PackVersion: res.PackVersion, ETag: res.ETag, Errors: []string{msg}}
	}
	if _, err := os.Stat(local); err != nil {
		return fail(fmt.Sprintf("Remote reported unchanged (HTTP 304) but no local snapshot exists at %s.", local))
	}
	root := local
	if subdir != nil {
		r, err := ResolveWithinRoot(local, *subdir)
		if err != nil {
			return fail(err.Error())
		}
		root = r
	}
	if !hasRecognisedArtifacts(root) {
		return fail("Remote reported unchanged (HTTP 304) but the local snapshot has no recognised artifact directories.")
	}
	m := readExistingManifest(local, subdir)
	if m == nil || !snapshotIntact(local, subdir, m) {
		return fail("Remote reported unchanged (HTTP 304) but the local snapshot does not match its recorded integrity digest.")
	}
	out := &FetchResult{OK: true, ArtifactsWritten: sumCounts(countSnapshotArtifacts(root)), Unchanged: true, ETag: res.ETag, PackVersion: res.PackVersion}
	if v := strings.TrimSpace(m.GetString("pack_version")); v != "" {
		out.PackVersion = &v
	}
	return out
}

func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}

// FetchPack is fetch_pack.
func FetchPack(p *OrgPackConfig, repoRoot string) *FetchResult {
	if p.SourceType == nil {
		return fetchErr(fmt.Sprintf("Pack '%s' has no source_type configured; set charter_packs.org.packs[].source_type to one of: git, https, artifactory, api.", p.Name))
	}
	if p.URL == nil || *p.URL == "" {
		return fetchErr(fmt.Sprintf("Pack '%s' has source_type=%s but no url; set charter_packs.org.packs[].url.", p.Name, dct.PyRepr(*p.SourceType)))
	}
	target, err := p.LocalPathRoot(repoRoot)
	if err != nil {
		return fetchErr(err.Error())
	}
	ref := ""
	if p.Ref != nil {
		ref = *p.Ref
	}
	var res *FetchResult
	switch *p.SourceType {
	case "git":
		res = (&gitSource{*p.URL, ref}).fetch(target)
	case "https", "artifactory":
		res = writeSnapshot(&httpsSource{url: *p.URL, ref: ref, sourceType: *p.SourceType}, nil, target, *p.URL, *p.SourceType, p.Subdir)
	case "api":
		res = writeSnapshot(nil, &apiSource{*p.URL, ref}, target, *p.URL, "api", p.Subdir)
	default:
		return fetchErr(fmt.Sprintf("Unknown source_type: %s for pack '%s'", dct.PyRepr(*p.SourceType), p.Name))
	}
	if res.OK {
		eff, err := p.EffectiveRoot(repoRoot)
		if err == nil {
			res.ArtifactsWritten = sumCounts(countSnapshotArtifacts(eff))
		}
	}
	return res
}

// requestsErrorText renders a transport failure the way python-requests'
// ConnectionError stringifies (connection refused / name resolution).
func requestsErrorText(endpoint string, err error) string {
	u, perr := url.Parse(endpoint)
	if perr != nil {
		return err.Error()
	}
	pool, conn := "HTTPConnectionPool", "HTTPConnection"
	port := u.Port()
	if u.Scheme == "https" {
		pool, conn = "HTTPSConnectionPool", "HTTPSConnection"
		if port == "" {
			port = "443"
		}
	} else if port == "" {
		port = "80"
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	msg := err.Error()
	var cause string
	switch {
	case strings.Contains(msg, "connection refused"):
		cause = fmt.Sprintf(`NewConnectionError("%s(host='%s', port=%s): Failed to establish a new connection: [Errno 111] Connection refused")`, conn, u.Hostname(), port)
	case strings.Contains(msg, "no such host"):
		cause = fmt.Sprintf(`NameResolutionError("%s(host='%s', port=%s): Failed to resolve '%s' ([Errno -2] Name or service not known)")`, conn, u.Hostname(), port, u.Hostname())
	default:
		return msg
	}
	return fmt.Sprintf("%s(host='%s', port=%s): Max retries exceeded with url: %s (Caused by %s)", pool, u.Hostname(), port, path, cause)
}
