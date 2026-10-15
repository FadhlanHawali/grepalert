package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// A small client for the two Kubernetes API calls the agent needs (list
// pods, follow a container's log), so the agent does not pull in client-go.
// It authenticates with the pod's service account when running in a
// cluster, or with a kubeconfig file (token or client certificate). Cloud
// "exec" logins (EKS, GKE) are not supported: use a token, or point the
// kubeconfig at `kubectl proxy`.

// KubernetesConfig is the agent's `kubernetes:` section.
type KubernetesConfig struct {
	// Kubeconfig is a kubeconfig file. Empty: the in-cluster service account.
	Kubeconfig string `yaml:"kubeconfig"`
	// Context in the kubeconfig; empty uses its current-context.
	Context string `yaml:"context"`
	// MaxStreams caps how many container logs are followed at once.
	MaxStreams int `yaml:"max_streams"`
}

const inClusterDir = "/var/run/secrets/kubernetes.io/serviceaccount"

type kubeClient struct {
	server string
	http   *http.Client // no timeout: log streams stay open; requests use contexts
	token  func() (string, error)
	basic  [2]string
}

func newKubeClient(cfg KubernetesConfig) (*kubeClient, error) {
	if cfg.Kubeconfig != "" {
		return kubeFromConfig(cfg.Kubeconfig, cfg.Context)
	}
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" {
		return nil, errors.New("not running inside Kubernetes: set kubernetes.kubeconfig in the agent config")
	}
	ca, err := os.ReadFile(filepath.Join(inClusterDir, "ca.crt"))
	if err != nil {
		return nil, fmt.Errorf("in-cluster CA: %w", err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca)
	c := &kubeClient{server: "https://" + net.JoinHostPort(host, port), http: kubeHTTP(&tls.Config{RootCAs: pool})}
	// Service account tokens are rotated; re-read the file now and then.
	c.token = cachedFile(filepath.Join(inClusterDir, "token"), time.Minute)
	return c, nil
}

func kubeHTTP(tlsCfg *tls.Config) *http.Client {
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: tlsCfg,
		MaxIdleConnsPerHost: 4, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 10 * time.Second}
	return &http.Client{Transport: tr}
}

func cachedFile(path string, ttl time.Duration) func() (string, error) {
	var mu sync.Mutex
	var val string
	var at time.Time
	return func() (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if val != "" && time.Since(at) < ttl {
			return val, nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		val, at = strings.TrimSpace(string(b)), time.Now()
		return val, nil
	}
}

type kubeconfigFile struct {
	CurrentContext string `yaml:"current-context"`
	Clusters       []struct {
		Name    string `yaml:"name"`
		Cluster struct {
			Server   string `yaml:"server"`
			CAData   string `yaml:"certificate-authority-data"`
			CA       string `yaml:"certificate-authority"`
			Insecure bool   `yaml:"insecure-skip-tls-verify"`
		} `yaml:"cluster"`
	} `yaml:"clusters"`
	Users []struct {
		Name string `yaml:"name"`
		User struct {
			Token        string `yaml:"token"`
			TokenFile    string `yaml:"tokenFile"`
			CertData     string `yaml:"client-certificate-data"`
			KeyData      string `yaml:"client-key-data"`
			Cert         string `yaml:"client-certificate"`
			Key          string `yaml:"client-key"`
			Username     string `yaml:"username"`
			Password     string `yaml:"password"`
			Exec         any    `yaml:"exec"`
			AuthProvider any    `yaml:"auth-provider"`
		} `yaml:"user"`
	} `yaml:"users"`
	Contexts []struct {
		Name    string `yaml:"name"`
		Context struct {
			Cluster string `yaml:"cluster"`
			User    string `yaml:"user"`
		} `yaml:"context"`
	} `yaml:"contexts"`
}

func kubeFromConfig(path, context string) (*kubeClient, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("kubeconfig: %w", err)
	}
	var kc kubeconfigFile
	if err := yaml.Unmarshal(raw, &kc); err != nil {
		return nil, fmt.Errorf("kubeconfig %s: %w", path, err)
	}
	if context == "" {
		context = kc.CurrentContext
	}
	var clusterName, userName string
	found := false
	for _, c := range kc.Contexts {
		if c.Name == context {
			clusterName, userName, found = c.Context.Cluster, c.Context.User, true
		}
	}
	if !found {
		return nil, fmt.Errorf("kubeconfig %s: context %q not found", path, context)
	}
	dir := filepath.Dir(path)
	rel := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(dir, p)
	}
	c := &kubeClient{}
	tlsCfg := &tls.Config{}
	for _, cl := range kc.Clusters {
		if cl.Name != clusterName {
			continue
		}
		c.server = strings.TrimSuffix(cl.Cluster.Server, "/")
		tlsCfg.InsecureSkipVerify = cl.Cluster.Insecure
		var pem []byte
		switch {
		case cl.Cluster.CAData != "":
			pem, err = base64.StdEncoding.DecodeString(cl.Cluster.CAData)
		case cl.Cluster.CA != "":
			pem, err = os.ReadFile(rel(cl.Cluster.CA))
		}
		if err != nil {
			return nil, fmt.Errorf("kubeconfig CA: %w", err)
		}
		if pem != nil {
			pool := x509.NewCertPool()
			pool.AppendCertsFromPEM(pem)
			tlsCfg.RootCAs = pool
		}
	}
	if c.server == "" {
		return nil, fmt.Errorf("kubeconfig %s: cluster %q not found", path, clusterName)
	}
	for _, u := range kc.Users {
		if u.Name != userName {
			continue
		}
		us := u.User
		if us.Exec != nil || us.AuthProvider != nil {
			return nil, fmt.Errorf("kubeconfig user %q logs in with an exec/auth-provider plugin, which the agent does not run; use a service account token, or run `kubectl proxy` and point a kubeconfig at it", userName)
		}
		switch {
		case us.Token != "":
			tok := us.Token
			c.token = func() (string, error) { return tok, nil }
		case us.TokenFile != "":
			c.token = cachedFile(rel(us.TokenFile), time.Minute)
		case us.Username != "":
			c.basic = [2]string{us.Username, us.Password}
		}
		certPEM, keyPEM := []byte(nil), []byte(nil)
		if us.CertData != "" {
			certPEM, _ = base64.StdEncoding.DecodeString(us.CertData)
			keyPEM, _ = base64.StdEncoding.DecodeString(us.KeyData)
		} else if us.Cert != "" {
			certPEM, _ = os.ReadFile(rel(us.Cert))
			keyPEM, _ = os.ReadFile(rel(us.Key))
		}
		if certPEM != nil {
			cert, err := tls.X509KeyPair(certPEM, keyPEM)
			if err != nil {
				return nil, fmt.Errorf("kubeconfig client certificate: %w", err)
			}
			tlsCfg.Certificates = []tls.Certificate{cert}
		}
	}
	c.http = kubeHTTP(tlsCfg)
	return c, nil
}

func (c *kubeClient) get(ctx context.Context, path string, q url.Values) (*http.Response, error) {
	u := c.server + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if c.token != nil {
		tok, err := c.token()
		if err != nil {
			return nil, fmt.Errorf("service account token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	} else if c.basic[0] != "" {
		req.SetBasicAuth(c.basic[0], c.basic[1])
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		var st struct {
			Message string `json:"message"`
		}
		json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&st)
		if st.Message == "" {
			st.Message = resp.Status
		}
		return nil, &kubeError{Code: resp.StatusCode, Msg: st.Message}
	}
	return resp, nil
}

type kubeError struct {
	Code int
	Msg  string
}

func (e *kubeError) Error() string {
	switch e.Code {
	case http.StatusUnauthorized:
		return "Kubernetes rejected the agent's credentials (401): " + e.Msg
	case http.StatusForbidden:
		return "the agent may not do this (403): give its service account get/list on pods and get on pods/log. " + e.Msg
	}
	return fmt.Sprintf("Kubernetes API: HTTP %d: %s", e.Code, e.Msg)
}

// kubePod is the part of a pod the agent uses.
type kubePod struct {
	Namespace  string
	Name       string
	Containers []string // running containers
}

func (c *kubeClient) listPods(ctx context.Context, namespace, selector string) ([]kubePod, error) {
	path := "/api/v1/namespaces/" + url.PathEscape(namespace) + "/pods"
	if namespace == "*" {
		path = "/api/v1/pods"
	}
	q := url.Values{}
	if selector != "" {
		q.Set("labelSelector", selector)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := c.get(ctx, path, q)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var list struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Status struct {
				ContainerStatuses []struct {
					Name  string `json:"name"`
					State struct {
						Running *struct{} `json:"running"`
					} `json:"state"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("pod list: %w", err)
	}
	out := make([]kubePod, 0, len(list.Items))
	for _, it := range list.Items {
		p := kubePod{Namespace: it.Metadata.Namespace, Name: it.Metadata.Name}
		for _, cs := range it.Status.ContainerStatuses {
			if cs.State.Running != nil {
				p.Containers = append(p.Containers, cs.Name)
			}
		}
		out = append(out, p)
	}
	return out, nil
}

// followLog opens a container's log stream with a timestamp on every line.
// since resumes after a known time; tailZero starts at the end.
func (c *kubeClient) followLog(ctx context.Context, ns, pod, container string, since time.Time, tailZero bool) (io.ReadCloser, error) {
	q := url.Values{"container": {container}, "follow": {"true"}, "timestamps": {"true"}}
	switch {
	case !since.IsZero():
		// sinceTime has second precision: lines of that second come again and
		// are skipped by timestamp.
		q.Set("sinceTime", since.UTC().Format(time.RFC3339))
	case tailZero:
		q.Set("tailLines", "0")
	}
	resp, err := c.get(ctx, "/api/v1/namespaces/"+url.PathEscape(ns)+"/pods/"+url.PathEscape(pod)+"/log", q)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}
