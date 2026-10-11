---
status: accepted
date: 2026-10-10
---
# The tenant identity is whatever the API server says it is

The cluster's authentication layer turns every caller, human or machine, into a username and groups. The tool works only with those, so it needs no cloud SDKs. Service accounts use `--as system:serviceaccount:<ns>:<name>`; cloud identities (Entra, IAM, Google) use the username and groups the cluster maps them to, which the README shows how to find per cloud; a pipeline that runs as a principal simply runs the tool with its own login and no `--as`. `--as-group` without `--as` is refused with exit 2, because the API requires `Impersonate-User` with `Impersonate-Group`; the README shows the probe-username pattern (`--as audit-probe --as-group team-a`) for testing a group alone. Every report states the identity from a SelfSubjectReview (`authentication.k8s.io/v1`, Kubernetes 1.28+), which is the name and groups the server actually used; if that call is forbidden or unavailable, the report prints the kubeconfig and `--as` values marked "unverified".
