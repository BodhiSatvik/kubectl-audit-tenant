#!/usr/bin/env python3
"""floor-guard.py: diff-scoped enforcement of the CONSTRAINTS.md floor.

Usage: python3 hack/floor-guard.py [--base origin/main]
       python3 hack/floor-guard.py --self-test
Exit 0 clean, 1 at least one floor violation, 2 the guard could not run.
Adapted for Go from the constraint-driven-development reference. Reports rule and location, never a secret value.
"""
import re, subprocess, sys

def git(args, allow_exit1=False):
    p = subprocess.run(["git", *args], capture_output=True, text=True)
    if p.returncode == 0 or (allow_exit1 and p.returncode == 1):
        return p.stdout
    return None

def bail(msg):
    print("floor-guard: " + msg, file=sys.stderr); sys.exit(2)

if "--self-test" in sys.argv:
    import os, tempfile
    row = "| Coverage | At least {floor}% of statements | make unit | CI |\n## Measured, not yet enforced\n| Project coverage | {today} | must not fall ({tol}% tolerance) |\n"
    cases = [("recording a Today value", dict(floor=80, today="98.0% (make unit, 2026-10-21)", tol=0.5), 0),
             ("changing a Direction number", dict(floor=80, today="98.0%", tol=2), 1),
             ("lowering an enforced floor", dict(floor=70, today="not measured yet", tol=0.5), 1)]
    for name, edit, want in cases:
        with tempfile.TemporaryDirectory() as d:
            sh = lambda *a: subprocess.run(["git", "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false", *a], cwd=d, check=True, capture_output=True)
            path = os.path.join(d, "CONSTRAINTS.md")
            with open(path, "w") as fh: fh.write(row.format(floor=80, today="not measured yet", tol=0.5))
            sh("init", "-q"); sh("add", "."); sh("commit", "-qm", "base")
            with open(path, "w") as fh: fh.write(row.format(**edit))
            got = subprocess.run([sys.executable, os.path.abspath(__file__), "--base", "HEAD"], cwd=d, capture_output=True).returncode
        if got != want: print(f"floor-guard self-test: FAIL, {name} exited {got}, want {want}"); sys.exit(1)
    print("floor-guard self-test: ok"); sys.exit(0)

base = "origin/main"
if "--base" in sys.argv:
    base = sys.argv[sys.argv.index("--base") + 1]
top = (git(["rev-parse", "--show-toplevel"]) or "").strip()
if not top: bail("not inside a git work tree")
import os; os.chdir(top)
merge_base = (git(["merge-base", base, "HEAD"]) or "").strip()
if not merge_base: bail("no merge base against " + base)
tracked = git(["diff", "--unified=0", merge_base, "--"])
if tracked is None: bail("could not diff against " + merge_base)
untracked_list = git(["ls-files", "--others", "--exclude-standard"])
if untracked_list is None: bail("could not list untracked files")
parts = [tracked]
for f in [x for x in untracked_list.split("\n") if x]:
    d = git(["diff", "--no-index", "--unified=0", "/dev/null", f], allow_exit1=True)
    if d is None: bail("could not diff untracked file " + f)
    parts.append(d)
diff = "\n".join(parts)

added, removed, deleted = [], [], []
path_of = lambda s: re.sub(r"^[ab]/", "", s)
file = old_file = ""; in_header = False
for line in diff.split("\n"):
    if line.startswith("diff "): in_header = True
    elif line.startswith("@@"): in_header = False
    elif in_header:
        if line.startswith("--- "): old_file = path_of(line[4:])
        elif line.startswith("+++ "):
            new_file = path_of(line[4:])
            file = old_file if new_file == "/dev/null" else new_file
            if new_file == "/dev/null": deleted.append(file)
    elif line.startswith("+"): added.append((file, line[1:]))
    elif line.startswith("-"): removed.append((file, line[1:]))

findings = []
flag = lambda rule, f, text: findings.append((rule, f, text.strip()[:120]))
is_go = lambda f: f.endswith(".go")
is_test = lambda f: f.endswith("_test.go")
is_constraints = lambda f: f.endswith("CONSTRAINTS.md")

SUPPRESSIONS = re.compile(r"//\s*nolint(?![:\w].*//\s*\S)|#nosec|gitleaks:allow|coverage:ignore")
STUBS = re.compile(r'panic\("(not implemented|TODO|todo|unimplemented)"?\)|//\s*(TODO|FIXME|XXX|HACK)\b')
SKIPS = re.compile(r"\bt\.Skip(Now|f)?\(|\bb\.Skip\(|testing\.Short\(\)")
ASSERTS = re.compile(r"\b(t\.(Error|Errorf|Fatal|Fatalf)|require\.|assert\.|if got != want|cmp\.Diff)\b")

for f, text in added:
    if is_go(f):
        if SUPPRESSIONS.search(text): flag("silenced-checker", f, text)
        if not is_test(f) and STUBS.search(text): flag("unfinished-work", f, text)
        if is_test(f) and SKIPS.search(text): flag("test-made-easier", f, text)
    if is_constraints(f) and re.match(r"^\| *(W|E)\d+ *\|", text): flag("new-exception", f, text)
for f in deleted:
    if is_test(f): flag("test-deleted", f, "file deleted")
for f, text in removed:
    if is_test(f) and f not in deleted and ASSERTS.search(text): flag("assertion-removed", f, text)

def rule_key(t):
    s = t.strip()
    if s.startswith("|"):
        cells = [c.strip() for c in s.split("|") if c.strip()]
        return cells[0] if cells else ""
    if re.match(r"^[-*] ", s): return s[2:].split(":")[0].strip()
    return None
is_exception = lambda t: re.match(r"^\| *(W|E)\d+ *\|", t.strip()) is not None
MIN_BEFORE = re.compile(r"(>=|>|≥|at least|minimum|\bmin\b|no less than|not fall|not drop)\s*$")
MAX_BEFORE = re.compile(r"(<=|<|≤|at most|maximum|\bmax\b|no more than|under|below|not grow|not exceed)\s*$")
MIN_AFTER = re.compile(r"^\s*\S*\s*(or more|or higher|must not fall|must not drop)")
MAX_AFTER = re.compile(r"^\s*\S*\s*(or less|or lower|must not grow|must not exceed)")
def thresholds(t):
    out = []
    for m in re.finditer(r"\d+(?:\.\d+)?", t):
        before = t[max(0, m.start() - 24):m.start()].lower(); after = t[m.end():m.end() + 40].lower()
        d = "min" if (MIN_BEFORE.search(before) or MIN_AFTER.search(after)) else "max" if (MAX_BEFORE.search(before) or MAX_AFTER.search(after)) else None
        out.append((float(m.group(0)), d))
    return out
# A row of the Measured table holds a record in its Today cell, not a rule: only Metric and Direction are compared (D158).
measured = {}
for f in {f for f, _ in removed + added if is_constraints(f) and os.path.exists(f)}:
    inside = False; measured[f] = set()
    for line in open(f):
        if line.startswith("## "): inside = line.strip() == "## Measured, not yet enforced"
        elif inside and line.strip().startswith("|"): measured[f].add(rule_key(line))
def ruled(f, t):
    if rule_key(t) not in measured.get(f, ()): return t
    cells = t.strip().strip("|").split("|")
    return "|".join(cells[:1] + cells[2:])
removed_rules = [(f, t) for f, t in removed if is_constraints(f) and rule_key(t) is not None]
added_rules = [(f, t) for f, t in added if is_constraints(f) and rule_key(t) is not None]
for f, r in removed_rules:
    match = next((a for _, a in added_rules if rule_key(a) == rule_key(r)), None)
    if match is None:
        if not is_exception(r): flag("rule-removed", f, r)
        continue
    before, after = thresholds(ruled(f, r)), thresholds(ruled(f, match)); verdict = None
    for d in ("min", "max", None):
        was = [x for x in before if x[1] == d]; now = [x for x in after if x[1] == d]
        for i, b in enumerate(was):
            if verdict: break
            n = now[i] if i < len(now) else None
            if n is None: verdict = "threshold-removed"
            elif n[0] == b[0]: continue
            elif (d == "min" and n[0] < b[0]) or (d == "max" and n[0] > b[0]) or d is None:
                verdict = "threshold-loosened" if d else "threshold-changed"
    if verdict: flag(verdict, f, r + "  ->  " + match)

if not findings:
    print("floor-guard: clean"); sys.exit(0)
print(f"floor-guard: {len(findings)} floor violation(s):", file=sys.stderr)
for rule, f, text in findings: print(f"  [{rule}] {f}: {text}", file=sys.stderr)
print("\nEach is a move that lowers the bar. Fix the code, or route it through a tracked exception in CONSTRAINTS.md.", file=sys.stderr)
sys.exit(1)
