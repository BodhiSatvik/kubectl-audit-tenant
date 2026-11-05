# Set up for the playbooks

Every playbook needs four tools: a container runtime, [kind](https://kind.sigs.k8s.io/) (Kubernetes in Docker), kubectl, and the plugin. The playbooks use bash or zsh. On Windows, use WSL2.

## 1. Check what you have

```bash
docker info --format 'docker server {{.ServerVersion}}'
kind version
kubectl version --client
kubectl audit-tenant version
```

Each line prints a version. Install what is missing:

| Tool | macOS (Homebrew) | Linux and WSL2 |
|---|---|---|
| Container runtime | [Docker Desktop](https://docs.docker.com/desktop/), or `brew install colima docker` and then `colima start` | [Docker Engine](https://docs.docker.com/engine/install/) |
| kind | `brew install kind` | [kind release binary](https://kind.sigs.k8s.io/docs/user/quick-start/#installation) |
| kubectl | `brew install kubectl` | [kubectl install](https://kubernetes.io/docs/tasks/tools/) |
| krew (optional) | `brew install krew` | [krew install](https://krew.sigs.k8s.io/docs/user-guide/setup/install/) |

The Kyverno playbook also needs [Helm](https://helm.sh/docs/intro/install/) (`brew install helm`), and Docker with at least 4 CPUs and 8 GB of memory.

## 2. Install the plugin

Pick one way.

**With krew**, from the release:

```bash
kubectl krew install --manifest-url https://github.com/BodhiSatvik/kubectl-audit-tenant/releases/download/v0.1.2/audit-tenant.yaml
```

krew puts plugins in `~/.krew/bin`. If kubectl does not find the plugin, add that folder to your `PATH`: `export PATH="${KREW_ROOT:-$HOME/.krew}/bin:$PATH"`.

**With Go** 1.26 or newer:

```bash
go install github.com/BodhiSatvik/kubectl-audit-tenant/cmd/kubectl-audit_tenant@v0.1.2
```

**With neither**: download the release binary and check its checksum.

```bash
v=0.1.2
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m); case $arch in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; esac
f=kubectl-audit-tenant_${v}_${os}_${arch}.tar.gz
curl -fsSLO "https://github.com/BodhiSatvik/kubectl-audit-tenant/releases/download/v$v/$f"
curl -fsSLO "https://github.com/BodhiSatvik/kubectl-audit-tenant/releases/download/v$v/checksums.txt"
grep " $f\$" checksums.txt > sum.txt
sha256sum -c sum.txt    # on macOS without coreutils: shasum -a 256 -c sum.txt
tar -xzf "$f" kubectl-audit_tenant
mkdir -p ~/.local/bin && mv kubectl-audit_tenant ~/.local/bin/
```

The checksum line must print `OK`. `~/.local/bin` must be on your `PATH`.

Check the install:

```bash
kubectl audit-tenant version
```

Expect `audit-tenant 0.1.2` and a commit. The playbooks show output from v0.1.2.

## 3. What the playbooks change on your machine

- Each playbook makes its own kind cluster and nothing else. `kind create cluster` adds a context named `kind-<cluster name>` to your kubeconfig and makes it the current context. The first time, kind downloads its node image (1.3 GB on disk on the test machine), so the first cluster takes a few minutes.
- The last step of each playbook deletes the cluster. kind then removes its context, and your kubeconfig has no current context. Go back to your own cluster with `kubectl config use-context <your context>`.
- The files that a playbook writes go into the folder you run it in. Run each playbook in a new, empty folder.

To uninstall the plugin: `kubectl krew uninstall audit-tenant`, or delete the `kubectl-audit_tenant` binary.
