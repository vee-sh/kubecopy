# kubectl-copy

A kubectl/oc plugin that intelligently copies Kubernetes resources across namespaces and clusters.

Handles the tedious parts automatically: stripping server-set metadata, resetting hardcoded
ClusterIPs and NodePorts, removing PV bindings, and detecting conflicts before they happen.

## Installation

### Via Homebrew

```bash
brew install vee-sh/tap/kube-copy
```

### From source

```bash
git clone <repo-url>
cd kubecopy
make install
```

### Via krew

```bash
kubectl krew install copy
```

## Usage

```
kubectl copy <resource>/<name> [flags]
```

### Flags

| Flag | Short | Description |
|------|-------|-------------|
| `--to-namespace` | `--to-ns` | Target namespace (defaults to source namespace) |
| `--to-name` | | New resource name (required for same-namespace copy) |
| `--to-context` | | Target kubeconfig context (for cross-cluster copy) |
| `--to-kubeconfig` | | Target kubeconfig file (for cross-cluster copy) |
| `--recursive` | `-r` | Copy the full dependency graph |
| `--dry-run` | | Preview what would be copied without making changes |
| `--on-conflict` | | Conflict strategy: `skip` (default), `warn`, `overwrite` |
| `--output` | `-o` | Dry-run output format: `table` (default), `yaml`, `json` |
| `--namespace` | `-n` | Source namespace |
| `--context` | | Source kubeconfig context |
| `--kubeconfig` | | Path to kubeconfig file |

### Examples

Copy a Deployment to another namespace:

```bash
kubectl copy deployment/myapp --to-namespace staging
```

Copy with a new name in the same namespace:

```bash
kubectl copy deployment/myapp --to-name myapp-v2
```

Copy to another cluster:

```bash
kubectl copy deployment/myapp --to-context prod-cluster --to-namespace default
```

Recursive copy (also copies related ConfigMaps, Secrets, Services, Ingresses, HPAs):

```bash
kubectl copy deployment/myapp --to-namespace staging -r
```

Dry-run to see what would happen:

```bash
kubectl copy deployment/myapp --to-namespace staging -r --dry-run
```

Dry-run with YAML output (useful for piping to `kubectl apply`):

```bash
kubectl copy deployment/myapp --to-namespace staging -r --dry-run -o yaml
```

Overwrite existing resources in the target:

```bash
kubectl copy deployment/myapp --to-namespace staging --on-conflict overwrite
```

## What Gets Sanitized

Every copied resource goes through a sanitization pipeline that strips fields
which would cause conflicts or errors when creating a copy.

### Universal (all resources)

- `metadata.uid`, `resourceVersion`, `creationTimestamp`, `generation`, `selfLink`, `managedFields`
- `metadata.ownerReferences`
- `status` (entire block)
- `kubectl.kubernetes.io/last-applied-configuration` annotation

### Resource-specific

| Resource | Sanitization |
|----------|-------------|
| **Service** | Resets `clusterIP`/`clusterIPs`, clears `nodePorts`, warns on `loadBalancerIP` |
| **Pod** | Removes `nodeName`, strips auto-injected SA token volumes |
| **PVC** | Removes `volumeName` (PV binding), strips PV-bind annotations |
| **Ingress** | Warns about hardcoded hostnames and TLS entries |
| **ServiceAccount** | Removes auto-generated token secret references |
| **Job** | Strips controller-generated labels and auto-generated selector |

## Conflict Detection

Before creating each resource, the plugin checks for:

- **Existence conflicts** -- resource already exists in target (behavior controlled by `--on-conflict`)
- **Address conflicts** -- hardcoded ClusterIP, NodePort, or LoadBalancer IP
- **Reference conflicts** -- referenced ConfigMap, Secret, PVC, or ServiceAccount does not exist in target (suggests using `--recursive`)

## Recursive Mode

When `--recursive` / `-r` is specified, the plugin discovers and copies the full
dependency graph:

**Forward references** (what the resource depends on):
- ConfigMaps, Secrets referenced in volumes, `envFrom`, `env.valueFrom`
- PVCs referenced in volumes
- ServiceAccounts

**Reverse references** (what depends on the resource):
- Services whose selector matches the pod template labels
- Ingresses whose backends reference those Services
- HPAs targeting the resource

Owner-managed resources (like ReplicaSets created by Deployments) are intentionally
skipped -- controllers will recreate them automatically.

## Supported Resource Types

The plugin works with any Kubernetes resource via the dynamic client. Common types
have built-in aliases:

`deployment`/`deploy`, `statefulset`/`sts`, `daemonset`/`ds`, `replicaset`/`rs`,
`pod`/`po`, `service`/`svc`, `configmap`/`cm`, `secret`, `serviceaccount`/`sa`,
`persistentvolumeclaim`/`pvc`, `ingress`/`ing`, `job`, `cronjob`/`cj`,
`horizontalpodautoscaler`/`hpa`, `networkpolicy`/`netpol`

## Development

```bash
# Build
make build

# Run tests
make test

# Cross-compile for all platforms (produces tarballs + sha256 in dist/)
make cross-build

# Lint
make lint
```

## Releasing

Releases are automated via GitLab CI. To cut a new release:

```bash
git tag v0.1.0
git push origin v0.1.0
```

The pipeline will:

1. Run tests and linting
2. Cross-compile binaries for linux/darwin on amd64/arm64
3. Upload tarballs to the GitLab generic package registry
4. Create a GitLab release with download links
5. Push an updated Homebrew formula to the `vee-sh/homebrew-tap` repository

### CI/CD Variables

The following variable must be set in **Settings > CI/CD > Variables** for the
Homebrew tap publish step to work:

| Variable | Description |
|----------|-------------|
| `HOMEBREW_TAP_TOKEN` | GitLab personal or project access token with write access to `vee-sh/homebrew-tap` |

### Setting up the Homebrew tap repo

Create the repository `vee-sh/homebrew-tap` on GitLab (if it does not exist yet).
The CI pipeline will automatically create and update `Formula/kube-copy.rb` on
each tagged release. Users install via:

```bash
brew install vee-sh/tap/kube-copy
```

## License

Apache 2.0
