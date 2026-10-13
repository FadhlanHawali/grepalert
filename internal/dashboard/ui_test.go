package dashboard

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestUIHandler(t *testing.T) {
	get := func(h http.Handler, path string) (*http.Response, string) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		body, _ := io.ReadAll(rec.Result().Body)
		return rec.Result(), string(body)
	}

	built := newUIHandler(fstest.MapFS{
		"web/not-built.html":       {Data: []byte("not built")},
		"web/dist/index.html":      {Data: []byte("<div id=root></div>")},
		"web/dist/assets/app-1.js": {Data: []byte("console.log(1)")},
		"web/dist/favicon.svg":     {Data: []byte("<svg/>")},
	})
	for _, p := range []string{"/", "/login"} {
		if resp, body := get(built, p); resp.StatusCode != 200 || body != "<div id=root></div>" || resp.Header.Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: %d %q cache=%q", p, resp.StatusCode, body, resp.Header.Get("Cache-Control"))
		}
	}
	if resp, _ := get(built, "/assets/app-1.js"); resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Errorf("asset: %d cache=%q", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
	if resp, _ := get(built, "/nope.js"); resp.StatusCode != 404 {
		t.Errorf("missing file: %d", resp.StatusCode)
	}

	bare := newUIHandler(fstest.MapFS{"web/not-built.html": {Data: []byte("not built")}})
	if resp, body := get(bare, "/"); resp.StatusCode != http.StatusServiceUnavailable || body != "not built" {
		t.Errorf("without a build: %d %q", resp.StatusCode, body)
	}
	if !isUIAsset("/assets/app-1.js") || isUIAsset("/api/overview") {
		t.Error("isUIAsset")
	}
}
