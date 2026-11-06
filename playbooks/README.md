# Playbooks

Step-by-step ways to try `kubectl audit-tenant` for your role, from zero, on a throwaway [kind](https://kind.sigs.k8s.io/) cluster (Kubernetes in Docker) on your laptop. Each playbook builds what it needs, shows the real output to expect, and deletes the cluster at the end. When you trust what you saw, [real-cluster.md](real-cluster.md) takes you to your own cluster, one read-only step at a time.

## Pick a playbook

| You are | You want to | Playbook | Time |
|---|---|---|---|
| Anyone curious | See what it does | [README quick start](../README.md#quick-start-kind-about-five-minutes) | 5 min |
| A platform team | Build a minimal shared cluster from zero, give every team a fenced namespace, and refuse a namespace that is not fenced | [platform-team.md](platform-team.md) | 15 min |
| A startup that runs each customer or environment in its own namespace | Show, with dated evidence, that customers are kept apart | [platform-team.md](platform-team.md), then [security-review.md](security-review.md) | 35 min |
| A security engineer, an auditor or a consultant | Find RBAC leaks between teams, and keep a dated page of verdicts per team | [security-review.md](security-review.md) | 20 min |
| The person who must approve a new tool | Prove from the API server's own audit log that it changes nothing | [security-review.md](security-review.md), part 6 | 10 min |
| An application developer | Check your own namespace with your own login, and see whether your next rollout fits the quota | [app-developer.md](app-developer.md) | 10 min |
| A DevOps or CI owner | Fail a pull request when the namespace template drops a fence, and audit every team each night | [ci-and-nightly.md](ci-and-nightly.md) | 20 min |
| A Kyverno user | Show what each tenant can really do next to Kyverno's results in Policy Reporter | [kyverno-dashboard.md](kyverno-dashboard.md) | 20 min |
| A Capsule user | Check each tenant's namespaces as the tenant owner | [capsule.md](capsule.md) | 15 min |
| A meetup host, a teacher or a student | Run a 60-minute hands-on lab: find and fix six broken fences | [workshop.md](workshop.md) | 60 min |
| Anyone who finished a playbook | Use it on a real cluster safely, then on every namespace | [real-cluster.md](real-cluster.md) | 30 min |

First time? Do [setup.md](setup.md): it checks and installs the four tools that every playbook needs.

## What every playbook promises

- It makes one new kind cluster with its own name, and works only there. Commands that install software, weaken a cluster on purpose or delete objects name the kind context (`--context`, or `--kube-context` for Helm). If the kind cluster was not created, they fail instead of touching another cluster.
- It shows the output it expects, copied from a real run, and says which version, Kubernetes version and date it was tested with.
- It ends with a clean-up step that deletes the cluster.
- It never asks for a cloud account, a token or a password.

The tool itself is read-only: it asks the API server "may I?" questions and sends dry runs that the server checks and stores nothing ([Safety](../README.md#safety)).

## Run a playbook with an AI agent

Each playbook starts with a prompt for a coding agent (Claude Code, Codex, Cursor, Copilot, Gemini CLI or another). Copy it as it is, or use this one and change the file name:

```text
Follow this playbook step by step, in a new empty folder on this machine:
https://github.com/BodhiSatvik/kubectl-audit-tenant/blob/main/playbooks/platform-team.md
Use only the kind cluster that the playbook creates. If the current kubectl context is anything else
before a kubectl or helm command, stop and ask me. Ask me before you install a tool. After each audit,
compare its summary line with the playbook's "Expect" line and tell me if they differ. Do not star the
repository, open issues or post anything for me. At the end, run the clean-up step and tell me, in a
few lines, what each audit found.
```

### Rules for agents that run these playbooks

If you are an AI agent and a person asked you to run one of these playbooks:

1. Run the steps in order. Do not skip the context checks.
2. Run cluster commands only against the kind cluster that the playbook creates. If the current context is any other cluster, stop and ask the person.
3. Never run a step that adds RBAC, changes labels, or deletes objects against a cluster that the playbook did not create.
4. Ask the person before you install software, push to a repository, or use a cloud account.
5. Compare each audit's summary line with the "Expect" line. If they differ, stop and show the person both.
6. Do not star the repository, open issues, submit the report form, or post anything for the person. Tell them that these options exist; the choice is theirs. If they ask you to draft their report, use only what happened in the run, and let them edit it and submit it from their own account.
7. At the end, run the clean-up step, summarize what each audit found and what did not match, and give the person the report link from the playbook's last section.

## Tell us

- **You ran a playbook**: fill in a [playbook report](https://github.com/BodhiSatvik/kubectl-audit-tenant/issues/new?template=playbook-report.yml). It asks where you ran it, what worked, what broke, and what the audit found; each playbook's last section links to it with the playbook name filled in.
- **Something else broke**: open an [issue](https://github.com/BodhiSatvik/kubectl-audit-tenant/issues/new) with the step and what you saw. Rough edges in a playbook are bugs.
- **You use it**: open an ["I use this" issue](https://github.com/BodhiSatvik/kubectl-audit-tenant/issues/new?template=i-use-this.yml). Say what you use it for and, if you can, what it found and what you changed. You choose whether we may list you in [ADOPTION.md](../ADOPTION.md).
- **It helped**: a star on the repository helps other platform teams find it.
- **A security problem**: do not open a public issue; see [SECURITY.md](../SECURITY.md).
