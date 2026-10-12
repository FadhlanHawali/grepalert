// Command webhook-sink is a demo helper for the docker compose example. It
// records webhook calls (shown at / and in its log) and serves a directory over
// HTTP as a stand-in for a git repository holding rules.
package main

import (
	"flag"
	"html/template"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

type call struct {
	Time time.Time
	Path string
	Body string
}

var (
	mu    sync.Mutex
	calls []call // newest first, capped
)

const keep = 200

var page = template.Must(template.New("p").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><meta http-equiv="refresh" content="3">
<title>webhook sink</title>
<style>body{font:14px system-ui,sans-serif;margin:20px;background:#f6f7f9;color:#1d2330}
table{border-collapse:collapse;width:100%;background:#fff}td,th{padding:6px 10px;border-bottom:1px solid #e2e5eb;text-align:left;vertical-align:top}
td.b{font:12px ui-monospace,Consolas,monospace;white-space:pre-wrap;word-break:break-all}</style></head>
<body><h2>Webhooks received ({{len .}}, newest first, auto-refresh)</h2>
<table><tr><th>Time</th><th>Path</th><th>Body</th></tr>
{{range .}}<tr><td>{{.Time.Format "15:04:05"}}</td><td>{{.Path}}</td><td class="b">{{.Body}}</td></tr>{{end}}
</table></body></html>`))

func main() {
	addr := flag.String("listen", ":9599", "listen address")
	repo := flag.String("repo", "/repo", "directory served at /repo/ (simulated git)")
	flag.Parse()

	http.HandleFunc("POST /hook/", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 64*1024))
		c := call{Time: time.Now(), Path: r.URL.Path, Body: strings.TrimSpace(string(b))}
		log.Printf("ALERT %s %s", c.Path, c.Body)
		mu.Lock()
		calls = append([]call{c}, calls...)
		if len(calls) > keep {
			calls = calls[:keep]
		}
		mu.Unlock()
	})
	http.Handle("GET /repo/", http.StripPrefix("/repo/", http.FileServer(http.Dir(*repo))))
	http.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		snapshot := append([]call(nil), calls...)
		mu.Unlock()
		page.Execute(w, snapshot)
	})
	log.Printf("webhook sink on %s, serving %s at /repo/", *addr, *repo)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
