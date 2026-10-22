package doctrine_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// TestParityCharterFetchUntrustedTLS covers the python-requests transport
// error texts for certificate failures (DOC-09): the https source reports
// the exception class, the api source the full requests message.
func TestParityCharterFetchUntrustedTLS(t *testing.T) {
	t.Parallel()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{}"))
	}))
	defer srv.Close()
	p := newOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		appendConfig(s, "charter_packs:\n  org:\n    packs:\n"+
			"    - name: web\n      local_path: packs/web\n      source_type: https\n      url: "+srv.URL+"/x.tar.gz\n"+
			"    - name: api\n      local_path: packs/api\n      source_type: api\n      url: "+srv.URL+"/v1\n")
	})
	for _, n := range []string{"web", "api"} {
		comparePackTool(t, p, "charter", "fetch", "--pack", n)
		comparePackTool(t, p, "charter", "fetch", "--pack", n, "--json")
	}
}
