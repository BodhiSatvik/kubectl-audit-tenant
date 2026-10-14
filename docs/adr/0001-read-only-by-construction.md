---
status: accepted
date: 2026-10-10
---
# Read-only by construction

The tool runs on production clusters, so it must never change them. Only four kinds of request may leave the client: GET, LIST, a SelfSubjectAccessReview ("may this identity do X?"), and a create with `dryRun=All` (a rehearsal the server never stores). A wrapper around the HTTP transport refuses anything else, and a test with a real client against a local HTTP server proves it. GET is not a read-only verb by itself (`pods/exec`, `attach`, `portforward` and `proxy` are GET upgrades), so the wrapper allows exact paths, not verbs, and refuses every subresource (eng review, 2026-10-11). Secret contents are never fetched; only the permission to read them is tested. The trade-off: the tool cannot write its own PolicyReport into the cluster. The user does that with `kubectl apply` if they want it.

Amended in the Phase 2 /ship review (D79, D101): the transport guarantees that `dryRun=All` leaves the client, not that every hop on the way honors it. A proxy or gateway can drop the flag, and the server then stores the object. The tool checks every accepted dry-run answer: an answer with a `resourceVersion` is ERROR naming the object to delete, and the run sends no further create. Answers the latch does not catch yet (no generated name, unreadable, a 5xx, client-go retries) are listed in TODOS.md before the Phase 2 merge.
