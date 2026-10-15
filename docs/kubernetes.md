[← Documentation index](../README.md#documentation)

# Kubernetes

A rule can read the logs of the pods a label selector picks, instead of files: what `kubectl logs -f -l app=checkout` shows, with alerts.

```yaml
rules:
  - name: checkout-errors
    kubernetes:
      namespace: shop            # "*" for every namespace
      selector: app=checkout     # any label selector, e.g. "app=checkout,tier!=canary"; empty = every pod
      container: app             # optional: default every container of the pod
    pattern: 'ERROR (?P<code>\w+)'
    severity: critical
    webhooks: [chat]
    suppress: {window: 5m, group_by: code}
```

Everything else about a rule works the same: captures, templates, suppression, absence rules, dashboard groups and staged rollout. A rule reads either `files` or `kubernetes`; use two rules for both. Alerts say where the line came from: `matched in k8s:shop/checkout-7d9f8-x2lkq/app`.

## How it follows pods

- **Discovery:** every `rescan_interval` (10 s by default), the agent lists the pods each source selects. It follows the log of every **running** container through the Kubernetes API, the same way `kubectl logs -f` does.
- **Pod changes:**
  - new replicas and replaced pods are picked up, and pods that are gone are dropped
  - a restarted container is followed again from where it stopped
- **Where reading starts:**
  - Pods that already run when a rule is loaded are followed from **now**, like files that exist at start (`start_from: end`), so old history doesn't fire a burst of alerts.
  - Pods that appear later are read from their **first line**.
- **Resuming:**
  - A dropped stream (API server timeouts, network blips) reconnects and continues after the last line it handled. Nothing is repeated or lost.
  - The time of that line is saved in the state file, so a restarted agent resumes too. Give the agent a persistent volume for its state file (`/var/lib/grepalert`) to keep that across pod restarts.
- **Overlapping selectors:** two sources selecting the same container share one stream.
- **Status:** the agent's status (dashboard → agent → Overview) shows each source with the number of pods it selects and any error. Each container's stream is listed with the tailed files.

## Deploy it

[examples/kubernetes](../examples/kubernetes) has a ready-to-apply agent:
- a namespace and a service account with **read-only** access to pods and their logs
- the config and rules as a ConfigMap
- a Deployment that runs as non-root, with a read-only root filesystem and no capabilities

[demo-app.yaml](../examples/kubernetes/demo-app.yaml) is a small app that logs errors, to try it:

```bash
kubectl apply -f examples/kubernetes/demo-app.yaml
kubectl apply -f examples/kubernetes/grepalert-agent.yaml
kubectl -n grepalert create secret generic grepalert-webhook --from-literal=url=https://hooks.slack.com/services/...
```

**Changing rules:** edit them with `kubectl -n grepalert edit configmap grepalert-rules`.
- Kubernetes updates the mounted file within about a minute, and the agent applies it without a restart.
- An invalid edit is rejected, and the running rules keep working; the agent logs the reason once.
- A pod that **starts** with an invalid rules file exits and restarts until the file is fixed. On a fresh start there are no last good rules to fall back to.

Run **one** agent per cluster for a set of rules. Every replica follows every selected pod and would send every alert.

## Permissions

The agent needs `get` and `list` on `pods`, and `get` on `pods/log`, nothing else:

```yaml
rules:
  - apiGroups: [""]
    resources: [pods]
    verbs: [get, list]
  - apiGroups: [""]
    resources: [pods/log]
    verbs: [get]
```

The example grants it cluster-wide (ClusterRole), which `namespace: "*"` needs. To limit the agent to some namespaces, bind a Role with these rules in each of them, and use those namespaces in the rules.

## Reaching the cluster

The agent's config has a `kubernetes:` section:

```yaml
kubernetes:
  kubeconfig: ""      # empty: the pod's service account (in-cluster)
  context: ""         # kubeconfig context; empty: its current-context
  max_streams: 200    # most container logs followed at once
```

- **Inside the cluster** (the usual way): nothing to set. The agent uses its pod's service account token, and re-reads it as Kubernetes rotates it.
- **From outside**, with a kubeconfig file: set `kubeconfig` and, optionally, `context`.
  - Supported logins: a token, a token file, a client certificate, or username and password.
  - Cloud logins that run a program (`exec`, as EKS and GKE use) are **not** run by the agent. Use a service account token in the kubeconfig instead. Or run `kubectl proxy` next to the agent and point a kubeconfig at `http://127.0.0.1:8001`.

## Things to know

- **API server load:** each followed container is one open log request to the API server, like a `kubectl logs -f`. `max_streams` (200) caps them; beyond it, the agent follows the first 200 and reports the rest. Prefer selectors that pick what you need over `namespace: "*"` with an empty selector.
- **Dry runs** replay files only. For rules with a Kubernetes source, the dry run says the pod logs were not replayed. The **rule playground** works for any rule: paste lines from `kubectl logs`.
- **With a dashboard:**
  - Set `GREPALERT_DASHBOARD_URL` and `GREPALERT_ENROLL_TOKEN` (see [Install](../README.md#install)), and mount a writable volume at `/var/lib/grepalert` instead of the read-only rules ConfigMap. The dashboard then pushes the group's rules there.
  - A group can mix agents that read files and agents that read pods: each agent only follows the sources it can.
