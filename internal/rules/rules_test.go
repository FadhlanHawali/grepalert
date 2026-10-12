package rules

import (
	"strings"
	"testing"
)

func TestParseValid(t *testing.T) {
	t.Setenv("HOOK_URL", "https://hooks.example.com/abc")
	c, err := Parse([]byte(`
webhooks:
  - name: slack
    url: ${HOOK_URL}
    template: '{"text": {{ json .Text }}}'
rules:
  - name: oom
    files: ["/var/log/*.log"]
    pattern: 'OutOfMemory$'
    webhooks: [slack]
    suppress: {window: 5m}
  - name: off
    disabled: true
    files: [x]
    pattern: x
    webhooks: [slack]
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Rules) != 1 {
		t.Fatalf("active rules = %d, want 1 (disabled skipped)", len(c.Rules))
	}
	if got := c.Webhooks["slack"].URL; got != "https://hooks.example.com/abc" {
		t.Fatalf("url = %q", got)
	}
	if c.Rules[0].Pattern != "OutOfMemory$" {
		t.Fatal("pattern must not be env-expanded")
	}
	if c.Rules[0].SuppressWindow().String() != "5m0s" || c.Rules[0].MaxAlerts() != 1 {
		t.Fatal("suppress defaults wrong")
	}
}

func TestParseErrors(t *testing.T) {
	_, err := Parse([]byte(`
webhooks:
  - name: w
    url: not-a-url
rules:
  - name: a
    files: [f]
    pattern: '('
    webhooks: [missing]
    suppress: {window: 1m, group_by: host}
  - name: a
    pattern: x
    webhooks: [w]
    typo_field: 1
`))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"typo_field"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}

	_, err = Parse([]byte(`
webhooks:
  - name: w
    url: not-a-url
rules:
  - name: a
    files: [f]
    pattern: '('
    webhooks: [missing]
  - name: a
    pattern: 'x(?P<h>y)'
    webhooks: [w]
    suppress: {window: 1m, group_by: host}
`))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"url must be", "pattern:", "unknown webhook", "duplicate name", "at least one file", "group_by"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%v", want, err)
		}
	}
}

func TestEmptyDocumentIsValid(t *testing.T) {
	c, err := Parse(nil)
	if err != nil || len(c.Rules) != 0 {
		t.Fatalf("Parse(nil) = %v, %v", c, err)
	}
}
