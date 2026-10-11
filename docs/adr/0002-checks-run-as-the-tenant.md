---
status: accepted
date: 2026-10-10
---
# Checks run as the tenant, not from the admin's view

Existing scanners read RBAC rules and policies from the admin's side and infer what a tenant could do. We instead ask the cluster directly, as the tenant identity: "would you let me do this forbidden thing?" (SelfSubjectAccessReview for permissions, server-side dry-run for pod and service creation). A PASS means the cluster itself said no, through whichever layer decided (RBAC, admission, or configuration). This catches gaps between what policies say and what RBAC allows, and it lets a team with only its own kubeconfig check its own fences. The cost: behavioral checks cannot test things that need running pods (network behavior), so those stay out of scope.
