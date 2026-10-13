package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fadhlanhawali/grepalert/internal/rules"
)

type job struct {
	alert Alert
	hook  *rules.CompiledWebhook
}

// notifier delivers alerts through a bounded queue drained by a fixed worker
// pool. When webhooks are slow or down the queue fills and new alerts are
// dropped (and counted) instead of piling up in memory.
type notifier struct {
	client  *http.Client
	queue   chan job
	events  *ring
	retries int

	sent    atomic.Uint64
	failed  atomic.Uint64
	dropped atomic.Uint64

	lastDropEvent atomic.Int64 // unix nanos

	wg sync.WaitGroup
}

func newNotifier(queueSize, retries int, events *ring) *notifier {
	return &notifier{
		client: &http.Client{
			Transport: &http.Transport{
				Proxy:               http.ProxyFromEnvironment,
				MaxIdleConns:        8,
				MaxIdleConnsPerHost: 2,
				IdleConnTimeout:     60 * time.Second,
				TLSHandshakeTimeout: 10 * time.Second,
			},
		},
		queue:   make(chan job, queueSize),
		events:  events,
		retries: retries,
	}
}

// shutdownGrace is how long deliveries may continue after stop is requested.
const shutdownGrace = 5 * time.Second

func (n *notifier) start(ctx context.Context, workers int) {
	// Deliveries use their own context: stopping must not cut off a webhook
	// request already in flight. It ends shutdownGrace after ctx is done.
	dctx, dcancel := context.WithCancel(context.Background())
	go func() {
		<-ctx.Done()
		time.AfterFunc(shutdownGrace, dcancel)
	}()
	for i := 0; i < workers; i++ {
		n.wg.Add(1)
		go func() {
			defer n.wg.Done()
			for {
				select {
				case <-ctx.Done():
					n.flush(dctx)
					return
				case j := <-n.queue:
					n.deliver(dctx, j)
				}
			}
		}()
	}
}

func (n *notifier) wait() { n.wg.Wait() }

// flush delivers what is still queued at shutdown, within shutdownGrace so
// a dead webhook cannot hold the process open.
func (n *notifier) flush(ctx context.Context) {
	for ctx.Err() == nil {
		select {
		case j := <-n.queue:
			n.deliver(ctx, j)
		default:
			return
		}
	}
}

func (n *notifier) enqueue(a Alert, r *rules.CompiledRule) {
	for _, h := range r.Hooks {
		select {
		case n.queue <- job{alert: a, hook: h}:
		default:
			total := n.dropped.Add(1)
			// One event per 10s at most, so a storm does not evict the event log.
			now := time.Now().UnixNano()
			if last := n.lastDropEvent.Load(); now-last >= int64(10*time.Second) && n.lastDropEvent.CompareAndSwap(last, now) {
				n.events.add(Event{Time: time.Now(), Type: "webhook_error", Rule: a.Rule,
					Message: fmt.Sprintf("webhook queue full: alerts are being dropped (%d dropped so far)", total)})
			}
		}
	}
}

func (n *notifier) deliver(ctx context.Context, j job) {
	body, ctype, err := renderBody(j.alert, j.hook)
	if err != nil {
		n.failed.Add(1)
		n.events.add(Event{Time: time.Now(), Type: "webhook_error", Rule: j.alert.Rule,
			Message: fmt.Sprintf("webhook %q: render: %v", j.hook.Name, err)})
		return
	}
	backoff := time.Second
	for attempt := 0; ; attempt++ {
		err = n.send(ctx, j.hook, body, ctype)
		if err == nil {
			n.sent.Add(1)
			return
		}
		if attempt >= n.retries || ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	n.failed.Add(1)
	n.events.add(Event{Time: time.Now(), Type: "webhook_error", Rule: j.alert.Rule,
		Message: fmt.Sprintf("webhook %q: %v", j.hook.Name, err)})
}

func (n *notifier) send(ctx context.Context, h *rules.CompiledWebhook, body []byte, ctype string) error {
	ctx, cancel := context.WithTimeout(ctx, h.Timeout.D())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, h.Method, h.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("User-Agent", "grepalert-agent/"+Version)
	for k, v := range h.Headers {
		req.Header.Set(k, v)
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Drain a bounded amount so the connection can be reused, never more.
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return nil
}

func renderBody(a Alert, h *rules.CompiledWebhook) ([]byte, string, error) {
	if h.Tmpl == nil {
		b, err := json.Marshal(a)
		return b, "application/json", err
	}
	var buf bytes.Buffer
	if err := h.Tmpl.Execute(&buf, a); err != nil {
		return nil, "", err
	}
	ctype := "text/plain; charset=utf-8"
	if t := bytes.TrimSpace(buf.Bytes()); len(t) > 0 && (t[0] == '{' || t[0] == '[') {
		ctype = "application/json"
	}
	return buf.Bytes(), ctype, nil
}

func alertText(a Alert) string {
	var b strings.Builder
	sev := ""
	if a.Severity != "" {
		sev = strings.ToUpper(a.Severity) + " "
	}
	if a.Kind == "summary" {
		fmt.Fprintf(&b, "%s[%s] %s: %d more matches were suppressed", sev, a.Agent, a.Rule, a.Suppressed)
		if a.GroupKey != "" {
			fmt.Fprintf(&b, " (%s)", a.GroupKey)
		}
		fmt.Fprintf(&b, ". Last: %s", a.Line)
		return b.String()
	}
	fmt.Fprintf(&b, "%s[%s] %s matched in %s: %s", sev, a.Agent, a.Rule, a.File, a.Line)
	if a.Suppressed > 0 {
		fmt.Fprintf(&b, " (+%d suppressed in previous window)", a.Suppressed)
	}
	return b.String()
}
