package dashboard

import (
	"context"
	"testing"
)

func TestSourceRequest(t *testing.T) {
	cases := []struct {
		src     GitSource
		url     string
		hdr, hv string
	}{
		{GitSource{Provider: "github", Repo: "acme/rules", Ref: "main", Path: "prod/web rules.yaml", Token: "t"},
			"https://api.github.com/repos/acme/rules/contents/prod/web%20rules.yaml?ref=main", "Authorization", "Bearer t"},
		{GitSource{Provider: "github", BaseURL: "https://ghe.local/api/v3/", Repo: "a/b", Ref: "v1", Path: "r.yaml"},
			"https://ghe.local/api/v3/repos/a/b/contents/r.yaml?ref=v1", "Accept", "application/vnd.github.raw"},
		{GitSource{Provider: "gitlab", Repo: "group/sub/proj", Ref: "main", Path: "rules/prod.yaml", Token: "g"},
			"https://gitlab.com/api/v4/projects/group%2Fsub%2Fproj/repository/files/rules%2Fprod.yaml/raw?ref=main", "PRIVATE-TOKEN", "g"},
	}
	for _, c := range cases {
		req, err := sourceRequest(context.Background(), c.src)
		if err != nil {
			t.Fatal(err)
		}
		if got := req.URL.String(); got != c.url {
			t.Errorf("url = %s\nwant  %s", got, c.url)
		}
		if got := req.Header.Get(c.hdr); got != c.hv {
			t.Errorf("%s = %q, want %q", c.hdr, got, c.hv)
		}
	}
}
