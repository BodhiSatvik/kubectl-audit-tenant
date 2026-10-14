---
status: accepted
date: 2026-10-10
---
# Read-only by construction

The tool runs on production clusters, so it must never change them. Only four kinds of request may leave the client: GET, LIST, a SelfSubjectAccessReview ("may this identity do X?"), and a create with `dryRun=All` (a rehearsal the server never stores). A wrapper around the HTTP transport refuses anything else, and a test with a real client against a local HTTP server proves it. GET is not a read-only verb by itself (`pods/exec`, `attach`, `portforward` and `proxy` are GET upgrades), so the wrapper allows exact paths, not verbs, and refuses every subresource (eng review, 2026-10-11). Secret contents are never fetched; only the permission to read them is tested. The trade-off: the tool cannot write its own PolicyReport into the cluster. The user does that with `kubectl apply` if they want it.

Amended in the Phase 2 /ship review (D79, D101): the transport guarantees that `dryRun=All` leaves the client, not that every hop on the way honors it. A proxy or gateway can drop the flag, and the server then stores the object. Amended again before the Phase 2 merge (D103): the transport checks every create answer. After the first one that is not a verified dry run (no generated name, a `resourceVersion`, an unreadable or oversized answer, a failed send, a 429 or a 5xx), it refuses every later create in the run. It also removes `Retry-After` from create answers, so client-go cannot resend a create. A hop that drops the flag can therefore store at most one object per run. When the answer shows that object, the check's ERROR names it so the user can delete it.
