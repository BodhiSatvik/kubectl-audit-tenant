# Run it without cluster-admin

`kubectl audit-tenant` sends every request as the tenant. So the person or pod that runs it needs no read rights of its own:

- **Self mode** (no `--as`): no extra rights. The tool runs as your own login.
- **`--as`**: one right, `impersonate`, on the tenant's identity only. The roles below grant that and nothing else.

Bind the role to the runner (a platform engineer, or the service account of a pipeline pod), not to the tenant. The examples use the runner service account `runner` in the namespace `platform-audit`. For a person, use a subject such as `{kind: User, name: jane@example.com, apiGroup: rbac.authorization.k8s.io}`.

## A service-account tenant

To test `dev` in `team-a`, put a Role and a RoleBinding in `team-a`:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: {name: audit-tenant-impersonate, namespace: team-a}
rules:
- apiGroups: [""]
  resources: [serviceaccounts]
  verbs: [impersonate]
  resourceNames: [dev]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: {name: audit-tenant-impersonate, namespace: team-a}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: audit-tenant-impersonate}
subjects:
- {kind: ServiceAccount, name: runner, namespace: platform-audit}
```

Run it with no `--as-group`:

```sh
kubectl audit-tenant -n team-a --as system:serviceaccount:team-a:dev
```

The API server adds the service account's groups itself and does not check them against the role.

## A user tenant

Users and groups are not namespaced, so this is a ClusterRole and a ClusterRoleBinding. List the user and every group that the identity provider gives it (`kubectl auth whoami` with the tenant's own login shows them):

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata: {name: audit-tenant-impersonate-team-a}
rules:
- apiGroups: [""]
  resources: [users]
  verbs: [impersonate]
  resourceNames: [team-a-admin]
- apiGroups: [""]
  resources: [groups]
  verbs: [impersonate]
  resourceNames: [team-a-admins]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata: {name: audit-tenant-impersonate-team-a}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: audit-tenant-impersonate-team-a}
subjects:
- {kind: ServiceAccount, name: runner, namespace: platform-audit}
```

```sh
kubectl audit-tenant -n team-a --as team-a-admin --as-group team-a-admins
```

The API server refuses an `--as` user or an `--as-group` that the role does not name. It adds `system:authenticated` itself.

## Check the rights

```sh
kubectl auth can-i impersonate serviceaccounts/dev -n team-a --as system:serviceaccount:platform-audit:runner
kubectl auth can-i impersonate users/team-a-admin --as system:serviceaccount:platform-audit:runner
kubectl auth can-i impersonate groups/team-a-admins --as system:serviceaccount:platform-audit:runner
```

Each prints `yes`.

## What the role allows

`impersonate` is not read-only. Whoever holds it can send any request as the tenant, with any tool. Keep `resourceNames` on every rule: without it, `impersonate` on `groups` lets the runner act as `system:masters`, which is cluster-admin (AT-03 reports that grant as a FAIL). Give the role only to the runner. In a pod, run the pod in a namespace where only platform admins may create pods or exec into pods.

Checked on kind with Kubernetes 1.37: with only these roles, the runner service account's runs gave the same 14 results as cluster-admin with the same `--as` and `--as-group`, for a service-account tenant and for a user tenant. The API server refused `--as` another user or service account, and `--as-group system:masters`.
