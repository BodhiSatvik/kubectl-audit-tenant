---
status: accepted
date: 2026-10-10
---
# Read-only by construction

The tool runs on production clusters, so it must never change them. Only four kinds of request may leave the client: GET, LIST, a SelfSubjectAccessReview ("may this identity do X?"), and a create with `dryRun=All` (a rehearsal the server never stores). A wrapper around the HTTP transport refuses anything else, and a unit test proves it. Secret contents are never fetched; only the permission to read them is tested. The trade-off: the tool cannot write its own PolicyReport into the cluster. The user does that with `kubectl apply` if they want it.
