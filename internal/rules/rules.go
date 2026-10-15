// Package rules defines the alert rule file format shared by the agent and the
// dashboard, and compiles it into a ready-to-use, immutable form.
package rules

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
	"text/template"
	"time"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// File is the on-disk / on-the-wire rule document.
type File struct {
	Webhooks []Webhook `yaml:"webhooks" json:"webhooks"`
	Rules    []Rule    `yaml:"rules" json:"rules"`
}

// Webhook is a named alert destination.
type Webhook struct {
	Name string `yaml:"name" json:"name"`
	// URL may reference environment variables as ${VAR} so secrets stay out of git.
	URL     string            `yaml:"url" json:"url"`
	Method  string            `yaml:"method,omitempty" json:"method,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	// Template is an optional Go text/template rendering the request body.
	// When empty a generic JSON payload is sent.
	Template string   `yaml:"template,omitempty" json:"template,omitempty"`
	Timeout  Duration `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

// Rule matches lines in one or more files.
type Rule struct {
	Name        string   `yaml:"name" json:"name"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Disabled    bool     `yaml:"disabled,omitempty" json:"disabled,omitempty"`
	Files       []string `yaml:"files" json:"files"`
	// Kubernetes reads the logs of matching pods instead of files.
	Kubernetes *KubernetesSource `yaml:"kubernetes,omitempty" json:"kubernetes,omitempty"`
	Pattern    string            `yaml:"pattern,omitempty" json:"pattern,omitempty"`
	// Absent makes this an absence rule: it alerts when no line matching
	// Absent has been seen for For, and again when one shows up (recovered).
	// Use it for heartbeats and jobs that should log regularly.
	Absent   string            `yaml:"absent,omitempty" json:"absent,omitempty"`
	For      Duration          `yaml:"for,omitempty" json:"for,omitempty"`
	Exclude  string            `yaml:"exclude,omitempty" json:"exclude,omitempty"`
	Severity string            `yaml:"severity,omitempty" json:"severity,omitempty"`
	Labels   map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Webhooks []string          `yaml:"webhooks" json:"webhooks"`
	Suppress *Suppress         `yaml:"suppress,omitempty" json:"suppress,omitempty"`
}

// KubernetesSource selects pods whose container logs a rule reads, the way
// `kubectl logs -f -l <selector>` would.
type KubernetesSource struct {
	// Namespace of the pods; "*" means every namespace.
	Namespace string `yaml:"namespace" json:"namespace"`
	// Selector is a label selector, e.g. "app=checkout,tier!=canary". Empty
	// selects every pod in the namespace.
	Selector string `yaml:"selector,omitempty" json:"selector,omitempty"`
	// Container limits it to one container of each pod; empty reads all.
	Container string `yaml:"container,omitempty" json:"container,omitempty"`
}

// Key identifies a source: rules with the same key read the same streams.
func (k KubernetesSource) Key() string { return k.Namespace + "|" + k.Selector + "|" + k.Container }

func (k KubernetesSource) String() string {
	s := "namespace " + k.Namespace
	if k.Selector != "" {
		s += ", pods " + k.Selector
	}
	if k.Container != "" {
		s += ", container " + k.Container
	}
	return s
}

var (
	dnsLabel        = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
	selectorPattern = regexp.MustCompile(`^[A-Za-z0-9_./\-=!, ()]*$`)
)

func (k *KubernetesSource) validate() error {
	k.Namespace = strings.TrimSpace(k.Namespace)
	k.Selector = strings.TrimSpace(k.Selector)
	k.Container = strings.TrimSpace(k.Container)
	switch {
	case k.Namespace == "":
		return errors.New(`kubernetes.namespace is required ("*" for every namespace)`)
	case k.Namespace != "*" && !dnsLabel.MatchString(k.Namespace):
		return fmt.Errorf("kubernetes.namespace %q is not a valid namespace name", k.Namespace)
	case !selectorPattern.MatchString(k.Selector):
		return fmt.Errorf("kubernetes.selector %q is not a label selector (e.g. app=checkout,tier!=canary)", k.Selector)
	case k.Container != "" && !dnsLabel.MatchString(k.Container):
		return fmt.Errorf("kubernetes.container %q is not a valid container name", k.Container)
	}
	return nil
}

// Suppress limits how many alerts a rule may send per time window.
type Suppress struct {
	// Window is the suppression period. Zero disables suppression.
	Window Duration `yaml:"window" json:"window"`
	// MaxAlerts is how many alerts may fire per window (default 1).
	MaxAlerts int `yaml:"max_alerts,omitempty" json:"max_alerts,omitempty"`
	// GroupBy names a capture group in Pattern; each distinct value gets its
	// own suppression window (e.g. per host or per error code).
	GroupBy string `yaml:"group_by,omitempty" json:"group_by,omitempty"`
	// Summary sends one "N more matches suppressed" alert when a window that
	// suppressed anything closes. Defaults to true.
	Summary *bool `yaml:"summary,omitempty" json:"summary,omitempty"`
}

// Duration is a time.Duration that (un)marshals as a string like "5m".
type Duration time.Duration

func (d Duration) D() time.Duration { return time.Duration(d) }

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	if n.Value == "" {
		*d = 0
		return nil
	}
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: invalid duration %q", n.Line, n.Value)
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

func (d Duration) MarshalJSON() ([]byte, error) {
	return []byte(`"` + time.Duration(d).String() + `"`), nil
}

// Compiled is an immutable, validated rule set. It is safe for concurrent use.
type Compiled struct {
	Raw      []byte
	Hash     string
	Rules    []*CompiledRule
	Webhooks map[string]*CompiledWebhook
}

type CompiledRule struct {
	Rule
	Re        *regexp.Regexp
	ExcludeRe *regexp.Regexp
	GroupIdx  int        // index of Suppress.GroupBy in Re, or -1
	Pre       *Prefilter // cheap pre-check before Re; nil when none applies
	// Absence is set for absence rules; Re is then the Absent pattern.
	Absence bool
	Hooks   []*CompiledWebhook
}

// SuppressWindow returns the suppression window, or 0 when disabled.
func (r *CompiledRule) SuppressWindow() time.Duration {
	if r.Suppress == nil {
		return 0
	}
	return r.Suppress.Window.D()
}

func (r *CompiledRule) MaxAlerts() int {
	if r.Suppress == nil || r.Suppress.MaxAlerts <= 0 {
		return 1
	}
	return r.Suppress.MaxAlerts
}

func (r *CompiledRule) SendSummary() bool {
	return r.Suppress == nil || r.Suppress.Summary == nil || *r.Suppress.Summary
}

type CompiledWebhook struct {
	Webhook
	Tmpl *template.Template
}

// Hash returns the short content hash used to identify a rule set version.
func Hash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:12]
}

// Options tune validation.
type Options struct {
	// AllowUnresolvedEnv skips URL checks for webhook URLs that contain
	// ${VAR} references. The dashboard validates with this set, because those
	// variables only exist in the agents' environments.
	AllowUnresolvedEnv bool
}

// Parse validates and compiles a YAML rule document.
func Parse(raw []byte) (*Compiled, error) { return ParseWith(raw, Options{}) }

// ParseWith is Parse with options.
func ParseWith(raw []byte, opts Options) (*Compiled, error) {
	// An empty document is a valid (empty) rule set.
	f, err := Decode(raw)
	if err != nil {
		return nil, err
	}
	return compile(raw, f, opts)
}

// Decode parses a YAML rule document without validating it.
func Decode(raw []byte) (*File, error) {
	var f File
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse rules: %w", err)
	}
	return &f, nil
}

// ParseOverlay compiles a base rule set (e.g. a group's rules) plus local
// extra rules (one machine's own) into one set. Local rules and webhooks may
// use the base's webhooks but must not reuse its names: the overlay can only
// add, never silently replace what the group defines. The result's Hash
// covers both documents.
func ParseOverlay(base, local []byte) (*Compiled, error) {
	bf, err := Decode(base)
	if err != nil {
		return nil, err
	}
	lf, err := Decode(local)
	if err != nil {
		return nil, fmt.Errorf("local rules: %w", err)
	}
	var errs []error
	names := map[string]bool{}
	for _, w := range bf.Webhooks {
		names[w.Name] = true
	}
	for _, w := range lf.Webhooks {
		if names[w.Name] {
			errs = append(errs, fmt.Errorf("local rules: webhook %q already exists in the group's rules; use another name", w.Name))
		}
	}
	names = map[string]bool{}
	for _, r := range bf.Rules {
		names[r.Name] = true
	}
	for _, r := range lf.Rules {
		if names[r.Name] {
			errs = append(errs, fmt.Errorf("local rules: rule %q already exists in the group's rules; local rules can only add rules", r.Name))
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	merged := &File{
		Webhooks: append(append([]Webhook{}, bf.Webhooks...), lf.Webhooks...),
		Rules:    append(append([]Rule{}, bf.Rules...), lf.Rules...),
	}
	raw := append(append(append([]byte{}, base...), "\n# local\n"...), local...)
	return compile(raw, merged, Options{})
}

// Compile validates f. raw is kept for hashing and round-tripping.
func Compile(raw []byte, f *File) (*Compiled, error) { return compile(raw, f, Options{}) }

func compile(raw []byte, f *File, opts Options) (*Compiled, error) {
	c := &Compiled{
		Raw:      raw,
		Hash:     Hash(raw),
		Webhooks: make(map[string]*CompiledWebhook, len(f.Webhooks)),
	}
	var errs []error

	for i, w := range f.Webhooks {
		if w.Name == "" {
			errs = append(errs, fmt.Errorf("webhooks[%d]: name is required", i))
			continue
		}
		if _, dup := c.Webhooks[w.Name]; dup {
			errs = append(errs, fmt.Errorf("webhook %q: duplicate name", w.Name))
			continue
		}
		skipURL := opts.AllowUnresolvedEnv && envRef.MatchString(w.URL)
		w.URL = ExpandEnv(w.URL)
		for k, v := range w.Headers {
			w.Headers[k] = ExpandEnv(v)
		}
		if u, err := url.Parse(w.URL); !skipURL && (err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "") {
			errs = append(errs, fmt.Errorf("webhook %q: url must be an absolute http(s) URL", w.Name))
		}
		if w.Method == "" {
			w.Method = "POST"
		}
		if w.Timeout <= 0 {
			w.Timeout = Duration(10 * time.Second)
		}
		cw := &CompiledWebhook{Webhook: w}
		if w.Template != "" {
			t, err := template.New(w.Name).Funcs(TemplateFuncs).Parse(w.Template)
			if err != nil {
				errs = append(errs, fmt.Errorf("webhook %q: template: %w", w.Name, err))
			}
			cw.Tmpl = t
		}
		c.Webhooks[w.Name] = cw
	}

	seen := map[string]bool{}
	for i, r := range f.Rules {
		where := fmt.Sprintf("rules[%d]", i)
		if r.Name != "" {
			where = fmt.Sprintf("rule %q", r.Name)
		}
		if r.Name == "" {
			errs = append(errs, fmt.Errorf("%s: name is required", where))
		} else if seen[r.Name] {
			errs = append(errs, fmt.Errorf("%s: duplicate name", where))
		}
		seen[r.Name] = true
		switch {
		case r.Kubernetes != nil && len(r.Files) > 0:
			errs = append(errs, fmt.Errorf("%s: use either files or kubernetes, not both (make two rules)", where))
		case r.Kubernetes != nil:
			if err := r.Kubernetes.validate(); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", where, err))
			}
		case len(r.Files) == 0:
			errs = append(errs, fmt.Errorf("%s: files (or a kubernetes source) is required", where))
		}
		switch {
		case r.Pattern == "" && r.Absent == "":
			errs = append(errs, fmt.Errorf("%s: pattern (or absent) is required", where))
		case r.Pattern != "" && r.Absent != "":
			errs = append(errs, fmt.Errorf("%s: use either pattern or absent, not both", where))
		case r.Absent != "" && r.For <= 0:
			errs = append(errs, fmt.Errorf("%s: absent needs a positive for: (e.g. for: 10m)", where))
		case r.Absent != "" && r.Suppress != nil:
			errs = append(errs, fmt.Errorf("%s: suppress does not apply to absence rules (they alert once, then once on recovery)", where))
		}
		if len(r.Webhooks) == 0 {
			errs = append(errs, fmt.Errorf("%s: at least one webhook is required", where))
		}
		cr := &CompiledRule{Rule: r, GroupIdx: -1, Absence: r.Absent != ""}
		src, field := r.Pattern, "pattern"
		if cr.Absence {
			src, field = r.Absent, "absent"
		}
		re, err := regexp.Compile(src)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %s: %w", where, field, err))
		}
		cr.Re = re
		if re != nil {
			cr.Pre = NewPrefilter(src)
		}
		if r.Exclude != "" {
			ex, err := regexp.Compile(r.Exclude)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: exclude: %w", where, err))
			}
			cr.ExcludeRe = ex
		}
		if s := r.Suppress; s != nil {
			if s.Window < 0 {
				errs = append(errs, fmt.Errorf("%s: suppress.window must not be negative", where))
			}
			if s.MaxAlerts < 0 {
				errs = append(errs, fmt.Errorf("%s: suppress.max_alerts must not be negative", where))
			}
			if s.GroupBy != "" && re != nil {
				cr.GroupIdx = re.SubexpIndex(s.GroupBy)
				if cr.GroupIdx < 0 {
					errs = append(errs, fmt.Errorf("%s: suppress.group_by %q is not a named group in pattern (use (?P<%s>...))", where, s.GroupBy, s.GroupBy))
				}
			}
		}
		for _, name := range r.Webhooks {
			w, ok := c.Webhooks[name]
			if !ok {
				errs = append(errs, fmt.Errorf("%s: unknown webhook %q", where, name))
				continue
			}
			cr.Hooks = append(cr.Hooks, w)
		}
		if !r.Disabled {
			c.Rules = append(c.Rules, cr)
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return c, nil
}

// ExpandEnv expands ${VAR} references only (not bare $VAR) so literal dollar
// signs in URLs survive.
var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func ExpandEnv(s string) string {
	return envRef.ReplaceAllStringFunc(s, func(m string) string {
		return os.Getenv(m[2 : len(m)-1])
	})
}

// TemplateFuncs are available inside webhook templates.
var TemplateFuncs = template.FuncMap{
	// json renders a value as a JSON string literal, safe to embed in a JSON body.
	"json": func(v any) string {
		b, _ := json.Marshal(v)
		return string(b)
	},
	"upper": strings.ToUpper,
	"lower": strings.ToLower,
	// title capitalizes the first letter: critical -> Critical.
	"title": func(s string) string {
		r, n := utf8.DecodeRuneInString(s)
		if n == 0 {
			return s
		}
		return string(unicode.ToUpper(r)) + s[n:]
	},
	"trunc": func(n int, s string) string {
		if len(s) <= n {
			return s
		}
		return s[:n] + "…"
	},
}
