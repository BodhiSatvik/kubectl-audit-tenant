---
status: accepted
date: 2026-10-10
---
# AT-09 requires the Pod Security Admission label even when another gatekeeper blocks pods

A namespace guarded by Kyverno or Gatekeeper but with no `pod-security.kubernetes.io/enforce` label passes AT-04 and fails AT-09. We keep AT-09 strict: the built-in gate is free and standard, two gates are safer than one, and the label documents intent. To keep it fair, the FAIL reason says when AT-04 shows admission blocked every probe anyway, names the gap as the missing label, and the hint gives the one-line label command. Teams that disagree use `--skip AT-09`, and the report shows the SKIP. The alternative, passing AT-09 whenever AT-04 passed through admission, would make the check stop testing what its name says. The same holds when a cluster-wide default PSA level is configured with no namespace label (CEO review, 2026-10-10): AT-09 still FAILs, and the reason notes when AT-04 showed PSA blocking the probes, because the label is what tells the people who read the namespace which level applies.
