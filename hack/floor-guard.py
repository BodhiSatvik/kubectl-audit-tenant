#!/usr/bin/env python3
"""floor-guard.py: diff-scoped enforcement of CONSTRAINTS.md and the gate files.

Usage: python3 -I hack/floor-guard.py [--base origin/main]
       python3 -I hack/floor-guard.py --self-test
Exit 0 clean, 1 at least one finding, 2 the guard could not run.

It fails closed (D164): any change to CONSTRAINTS.md or to a gate file (GATE below: the Makefile,
go.mod, .github/, hack/, test/e2e/, test/envtest/, internal/safety/, internal/testutil/, the report
schema, the lint, release, gitleaks and git attribute files) is a finding, because a line-by-line guess
at intent can always be evaded. The one exception is a Measured row's Today cell, a record judged by
its Direction (D158, D161, D165). In every file it also flags suppression markers, carriage returns
and quoted paths; in Go files, stubs, skips, build tags and generated-file headers; in test files,
deleted tests and removed assertions. CI runs origin/main's copy too, so a branch cannot weaken the
guard that judges it. A suppression finding shows only the marker, never the line around it.
"""
import os, re, subprocess, sys

def git(args, allow_exit1=False):
    # Bytes, split on "\n" only: text mode would turn a bare "\r" into a line break and hide what follows it.
    p = subprocess.run(["git", "-c", "core.quotePath=false", *args], capture_output=True)
    if p.returncode == 0 or (allow_exit1 and p.returncode == 1):
        return p.stdout.decode("utf-8", "surrogateescape")
    return None

def bail(msg):
    print("floor-guard: " + msg, file=sys.stderr); sys.exit(2)

if "--self-test" in sys.argv:
    import tempfile
    from concurrent.futures import ThreadPoolExecutor
    C = "CONSTRAINTS.md"
    E = "## Enforced with numbers\n| Coverage | At least {}% of statements | make unit | CI |\n"
    MH = "## Measured, not yet enforced\n| Metric | Today | Direction |\n|---|---|---|\n"
    row = lambda metric, today, direction: f"| {metric} | {today} | {direction} |\n"
    COV, SIZE, WALL = "must not fall (0.5% tolerance)", "must not grow more than 10% per minor release", "stays under 90 s"
    e80 = E.format(80)
    cov = lambda today: {C: e80 + MH + row("Project coverage", today, COV)}
    size = lambda today: {C: e80 + MH + row("Binary size", today, SIZE)}
    wall = lambda today: {C: e80 + MH + row("make check wall time", today, WALL)}
    b0, b98 = cov("not measured yet"), cov("98.0%")
    run, go = "test/e2e/run.sh", "internal/x/x_test.go"
    test = "package x\n\nfunc TestX(tb *testing.T) {\n\tif 1 != 1 {\n\t\ttb.Fatal(\"no\")\n\t}\n\trequire.NoError(tb, nil)\n}\n"
    PC = "[protected-change]"
    # name, files at the base commit, files in the working tree, the text the guard must print ("clean" passes), and
    # whether to stage the working tree (git add -A), as a committed branch is in CI
    cases = [("recording a first coverage value", b0, cov("98.0% (make unit, 2026-10-21)"), "clean"),
             ("recording a better coverage value", b98, cov("98.4%"), "clean"),
             ("recording a drop within the tolerance", b98, cov("97.6%"), "clean"),
             ("recording a drop at the tolerance", b98, cov("97.5%"), "clean"),
             ("recording a worse coverage value", b98, cov("60.0%"), "[record-worse]"),
             ("erasing a recorded value", b98, cov("re-measure after the refactor"), "[record-removed]"),
             ("rewording a placeholder", b0, cov("not measured yet (no code)"), "clean"),
             ("hiding a worse value after the number", b98, cov("99.9 (target; measured 61.0%)"), "[record-removed]"),
             ("writing a time in scientific notation", wall("16.9 s"), wall("1e3 s"), "[record-removed]"),
             ("changing a time's unit", wall("16.9 s"), wall("1.5 min"), "[record-unit-changed]"),
             ("changing a size's unit", size("41.3 MB"), size("0.05 GB"), "[record-unit-changed]"),
             ("recording a first size over a placeholder that names a version", size("record at v0.1.0"), size("41.3 MB (v0.1.0)"), "clean"),
             ("growing a size within its allowance", size("41.3 MB"), size("45.0 MB"), "clean"),
             ("growing a size past its allowance", size("41.3 MB"), size("46.0 MB"), "[record-worse]"),
             ("recording a smaller size", size("41.3 MB"), size("40.0 MB"), "clean"),
             ("recording a slower time under its bound", wall("16.9 s"), wall("17.4 s"), "clean"),
             ("recording a faster time", wall("16.9 s"), wall("12.0 s"), "clean"),
             ("recording a time at its bound", wall("16.9 s"), wall("90.0 s"), "[record-worse]"),
             ("recording a first time past its bound", wall("not measured yet"), wall("95 s"), "[record-worse]"),
             ("changing a Direction", b98, {C: b98[C].replace("must not fall", "may fall")}, PC),
             ("renaming a Measured metric with a better value", b98, {C: e80 + MH + row("Project coverage (one package)", "98.4%", COV)}, PC),
             ("adding a trailing cell to a Measured row", b98, {C: e80 + MH + "| Project coverage | 98.4% | " + COV + " | x |\n"}, PC),
             ("dropping a Measured row", b98, {C: e80 + MH}, PC),
             ("adding a second coverage row", b98, {C: b98[C] + row("Project coverage", "60.0%", COV)}, PC),
             ("editing the Measured header row", b98, {C: b98[C].replace("| Today |", "| Advisory notes only |")}, PC),
             ("a three-cell row outside the Measured table", {C: e80 + "## Stages\n" + row("fast", "5 s", WALL) + MH}, {C: e80 + "## Stages\n" + row("fast", "4 s", WALL) + MH}, PC),
             ("lowering an enforced floor", b0, {C: E.format(70) + b0[C][len(e80):]}, PC),
             ("raising an enforced floor (loud too, D164)", b0, {C: E.format(90) + b0[C][len(e80):]}, PC),
             ("adding a blank line", b0, {C: b0[C] + "\n"}, PC),
             ("renaming the file, untracked", b0, {"QUALITY.md": b0[C]}, PC),
             ("renaming the file with git mv", b0, {"QUALITY.md": b0[C]}, PC, True),
             ("moving the file to _archive/", b0, {"_archive/CONSTRAINTS.md": b0[C]}, PC),
             ("hiding a change with .gitattributes", b0, {C: E.format(70) + b0[C][len(e80):], ".gitattributes": "CONSTRAINTS.md -diff\n"}, "CONSTRAINTS.md: - | Coverage | At least 80%"),
             ("hiding a line behind a carriage return", {**b0, "docs/x.md": "a\n"}, {**b0, "docs/x.md": "a\n\rexit 0\n"}, "[carriage-return]"),
             ("naming a file with a quote", b0, {**b0, 'internal/x/a"b_test.go': "package x\n"}, "[quoted-path]", True),
             ("removing one line from a gate script", {**b0, run: "set -e\nmake bench\n"}, {**b0, run: "set -e\n"}, PC),
             ("editing only a comment in a gate script", {**b0, run: "# old note\nset -e\n"}, {**b0, run: "# new note\nset -e\n"}, PC),
             ("a Makefile comment that ends in a backslash", {**b0, "Makefile": "check: unit\n"}, {**b0, "Makefile": "# note \\\ncheck: unit\n"}, PC),
             ("only a mode change on a gate script", {**b0, run: "set -e\n"}, {**b0, run: "set -e\n"}, PC, "chmod"),
             ("a test skipped", {**b0, go: test}, {**b0, go: test.replace("{\n\tif", "{\n\ttb.Skip(\"later\")\n\tif")}, "[test-made-easier]"),
             ("a test skipped with a space", {**b0, go: test}, {**b0, go: test.replace("{\n\tif", "{\n\ttb.Skip (\"later\")\n\tif")}, "[test-made-easier]"),
             ("a skip in a helper outside a test file", b0, {**b0, "internal/x/helper.go": "package x\n\nfunc Later(tb testing.TB) { tb.Skip(\"later\") }\n"}, "[test-made-easier]"),
             ("naming the SKIP verdict", {**b0, go: test}, {**b0, go: test.replace("\trequire", "\t_ = report.Skip\n\trequire")}, "clean"),
             ("a build tag in a test file", {**b0, go: test}, {**b0, go: "//go:build never\n\n" + test}, "[build-tag]"),
             ("a build tag in a non-test file", b0, {**b0, "internal/x/x.go": "//go:build !race\n\npackage x\n"}, "[build-tag]"),
             ("a generated-file header", b0, {**b0, "internal/x/x.go": "// Code generated by hand. DO NOT EDIT.\n\npackage x\n"}, "[generated-header]"),
             ("a bare nolint", b0, {**b0, "internal/x/x.go": "package x\n\nvar v = 1 //nolint\n"}, "[silenced-checker]"),
             ("a nolint of every linter", b0, {**b0, "internal/x/x.go": "package x\n\nvar v = 1 //nolint:all // reason\n"}, "[silenced-checker]"),
             ("a gitleaks marker outside Go", b0, {**b0, "docs/x.yaml": "key: value # gitleaks:allow\n"}, "[silenced-checker] docs/x.yaml: gitleaks:allow"),
             ("a stub in shipped code", b0, {**b0, "internal/x/x.go": "package x\n\nfunc F() { panic(\"TODO\") }\n"}, "[unfinished-work]"),
             ("deleting a test file", {**b0, go: test}, b0, "[test-deleted]"),
             ("removing an assertion", {**b0, go: test}, {**b0, go: test.replace("\t\ttb.Fatal(\"no\")\n", "")}, "[assertion-removed]"),
             ("removing a require assertion", {**b0, go: test}, {**b0, go: test.replace("\trequire.NoError(tb, nil)\n", "")}, "[assertion-removed]")]
    for path in ["Makefile", "GNUmakefile", "build/x.mk", "go.mod", "go.work", "vendor/modules.txt", ".github/workflows/e2e.yml", "hack/x.sh",
                 "hack/floor-guard.py", "test/e2e/expected/run1.txt", "test/envtest/x_test.go", "internal/safety/x.go", "internal/testutil/server.go",
                 "docs/report.schema.json", ".golangci.yml", ".golangci.toml", ".golangci.json", ".goreleaser.yaml", ".gitleaks.toml",
                 ".gitattributes", "test/.gitattributes"]:
        cases.append((f"changing {path}", {**b0, path: "a = 1\n"}, {**b0, path: "a = 1\nb = 2\n"}, PC))
    env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}  # a git hook's GIT_DIR or GIT_INDEX_FILE would aim these at the real repo
    def put(d, files):
        for path, text in files.items():
            os.makedirs(os.path.join(d, os.path.dirname(path)), exist_ok=True)
            with open(os.path.join(d, path), "w", newline="") as fh: fh.write(text)
    def one(case):
        name, before, after, tag, *how = case
        with tempfile.TemporaryDirectory() as d:
            sh = lambda *a: subprocess.run(["git", "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false", *a], cwd=d, env=env, check=True, capture_output=True)
            put(d, before); sh("init", "-q"); sh("add", "."); sh("commit", "-qm", "base")
            for path in set(before) - set(after): os.remove(os.path.join(d, path))
            put(d, after)
            if how == ["chmod"]: os.chmod(os.path.join(d, run), 0o755)
            if how == [True]: sh("add", "-A")
            p = subprocess.run([sys.executable, "-I", os.path.abspath(__file__), "--base", "HEAD"], cwd=d, env=env, capture_output=True, text=True)
        want = 0 if tag == "clean" else 1
        if p.returncode != want or tag not in (p.stdout if want == 0 else p.stderr):  # the text, so a crash (also exit 1) cannot pass
            return f"floor-guard self-test: FAIL, {name} exited {p.returncode}, want {want} with {tag}:\n{p.stdout}{p.stderr}"
        return None
    for failure in ThreadPoolExecutor(8).map(one, cases):
        if failure: print(failure); sys.exit(1)
    print(f"floor-guard self-test: ok ({len(cases)} cases)"); sys.exit(0)

base = "origin/main"
if "--base" in sys.argv:
    base = sys.argv[sys.argv.index("--base") + 1]
top = (git(["rev-parse", "--show-toplevel"]) or "").strip()
if not top: bail("not inside a git work tree")
os.chdir(top)
merge_base = (git(["merge-base", base, "HEAD"]) or "").strip()
if not merge_base: bail("no merge base against " + base)
# Raw text with fixed prefixes, whatever the attributes or the local git config say; no rename pairing.
DIFF = ["diff", "--no-renames", "--no-color", "--no-ext-diff", "--text", "--no-textconv", "--src-prefix=a/", "--dst-prefix=b/", "--unified=0"]
tracked = git(DIFF + [merge_base, "--"])
if tracked is None: bail("could not diff against " + merge_base)
untracked_list = git(["ls-files", "-z", "--others", "--exclude-standard"])
if untracked_list is None: bail("could not list untracked files")
parts = [tracked]
for f in [x for x in untracked_list.split("\0") if x]:
    d = git(DIFF + ["--no-index", "/dev/null", f], allow_exit1=True)
    if d is None: bail("could not diff untracked file " + f)
    parts.append(d)
diff = "\n".join(parts)

findings = []
flag = lambda rule, f, text: findings.append((rule, f, text.strip()[:120]))
added, removed, deleted, touched = [], [], [], set()
file = old_file = ""; in_header = False
for line in diff.split("\n"):
    if line.startswith("diff "):
        in_header = True
        rest = line[len("diff --git a/"):]
        if '"' in line: flag("quoted-path", line, "git quoted this path; rename it"); file = old_file = ""
        else: touched.add(rest[:(len(rest) - 3) // 2])  # "X b/X": with --no-renames both halves name one path
    elif line.startswith("@@"): in_header = False
    elif in_header:
        if line.startswith("--- "): old_file = re.sub(r"^a/", "", line[4:])
        elif line.startswith("+++ "):
            new_file = re.sub(r"^b/", "", line[4:])
            file = old_file if new_file == "/dev/null" else new_file
            if new_file == "/dev/null": deleted.append(file)
    elif line.startswith("+"): added.append((file, line[1:]))
    elif line.startswith("-"): removed.append((file, line[1:]))

is_go = lambda f: f.endswith(".go")
is_test = lambda f: f.endswith("_test.go")
GATE = re.compile(r"^(Makefile|GNUmakefile|makefile|.*\.mk|go\.mod|go\.work(\.sum)?|vendor/.*|\.github/.*|hack/.*|test/e2e/.*|test/envtest/.*"
                  r"|internal/safety/.*|internal/testutil/.*|docs/report\.schema\.json|\.golangci\.(ya?ml|json|toml)|\.goreleaser\.ya?ml"
                  r"|\.gitleaks.*|(.*/)?\.gitattributes)$")
SUPPRESSIONS = re.compile(r"//\s*nolint:all\b|//\s*nolint(?![:\w].*//\s*\S)|#nosec|gitleaks:allow|coverage:ignore")
STUBS = re.compile(r'panic\("(not implemented|TODO|todo|unimplemented)"?\)|//\s*(TODO|FIXME|XXX|HACK)\b')
SKIPS = re.compile(r"\.Skip(Now|f)?\s*\(|testing\.Short\(\)")  # a call: report.Skip, the verdict, is not one
BUILD_TAGS = re.compile(r"^\s*//\s*(go:build|\+build)\b")
GENERATED = re.compile(r"^\s*//\s*Code generated .*DO NOT EDIT")
ASSERTS = re.compile(r"\b(\w+\.(Error|Errorf|Fatal|Fatalf)|require\.|assert\.|if got != want|cmp\.Diff)\b")

for f, text in added + removed:
    if "\r" in text: flag("carriage-return", f, "a changed line holds a carriage return")
for f, text in added:
    m = SUPPRESSIONS.search(text)
    if m and f != "hack/floor-guard.py": flag("silenced-checker", f, m.group(0))  # the marker only: the line may hold a secret
    if is_go(f):
        if not is_test(f) and STUBS.search(text): flag("unfinished-work", f, text)
        if SKIPS.search(text): flag("test-made-easier", f, text)
        if BUILD_TAGS.match(text): flag("build-tag", f, text)
        if GENERATED.match(text): flag("generated-header", f, text)
for f in deleted:
    if is_test(f): flag("test-deleted", f, "file deleted")
for f, text in removed:
    if is_test(f) and f not in deleted and ASSERTS.search(text): flag("assertion-removed", f, text)

# Gate files (D164): any change at all, content or mode; one finding per file.
for f in sorted(p for p in touched if GATE.match(p)):
    a, r = sum(1 for g, _ in added if g == f), sum(1 for g, _ in removed if g == f)
    flag("protected-change", f, f"{a} line(s) added, {r} removed" if a or r else "mode or empty-file change")

# CONSTRAINTS.md: every changed line is a finding, except a Measured row whose only change is its Today
# cell, a record (D158). A row counts as Measured by its exact line under that heading, after the header
# and separator rows, in the base file for a removed line and in the working tree for an added one.
def measured_rows(text):
    rows, inside, seen = set(), False, 0
    for line in text.split("\n"):
        if line.startswith("## "): inside, seen = line.strip() == "## Measured, not yet enforced", 0
        elif inside and line.strip().startswith("|"):
            seen += 1
            if seen > 2: rows.add(line.strip())
    return rows
cells = lambda t: [c.strip() for c in t.strip().strip("|").split("|")]
def record(t):  # (value, unit) when the Today cell is exactly "<number> <unit>" with an optional "(note)"; else None
    m = re.fullmatch(r"(\d+(?:\.\d+)?)\s*(%|[A-Za-z]+)(\s+\(.*\))?", cells(t)[1])
    return (float(m.group(1)), m.group(2)) if m else None
def worse(old, new, direction):  # D161, D165
    if new is None: return "record-removed" if old is not None else None
    if old is not None and old[1] != new[1]: return "record-unit-changed"
    under = re.search(r"stays under (\d+(?:\.\d+)?)\s*(%|[A-Za-z]+)", direction)
    if under and new[1] != under.group(2): return "record-unit-changed"
    if under and new[0] >= float(under.group(1)): return "record-worse"
    if old is None: return None  # the first measurement is free
    tol = re.search(r"(\d+(?:\.\d+)?)% tolerance", direction)
    grow = re.search(r"not grow more than (\d+(?:\.\d+)?)%", direction)
    if "not fall" in direction and new[0] < old[0] - (float(tol.group(1)) if tol else 0): return "record-worse"
    if grow and new[0] > old[0] * (1 + float(grow.group(1)) / 100): return "record-worse"
    return None
was_measured = measured_rows(git(["show", merge_base + ":CONSTRAINTS.md"]) or "")
is_measured = set()
if os.path.exists("CONSTRAINTS.md"):
    with open("CONSTRAINTS.md", encoding="utf-8", errors="surrogateescape", newline="") as fh: is_measured = measured_rows(fh.read())
new_rows = [t for f, t in added if f == "CONSTRAINTS.md"]
for f, r in removed:
    if f != "CONSTRAINTS.md": continue
    def same_row(a):  # the same Measured row with only its Today cell changed
        return a.strip() in is_measured and len(cells(a)) == len(cells(r)) == 3 and cells(a)[0] == cells(r)[0] and cells(a)[2] == cells(r)[2]
    match = next((a for a in new_rows if same_row(a)), None) if r.strip() in was_measured else None
    if match is None:
        flag("protected-change", f, "- " + r); continue
    new_rows.remove(match)
    verdict = worse(record(r), record(match), cells(r)[2].lower())
    if verdict: flag(verdict, f, r + "  ->  " + match)
for t in new_rows:
    flag("protected-change", "CONSTRAINTS.md", "+ " + t)

if not findings:
    print("floor-guard: clean"); sys.exit(0)
print(f"floor-guard: {len(findings)} finding(s):", file=sys.stderr)
for rule, f, text in findings: print(f"  [{rule}] {f}: {text}", file=sys.stderr)
print("\nEach is a change to the quality bar or to a gate (D164). Undo it, or ask Satvik to read it and merge on purpose; the guard has no override.", file=sys.stderr)
sys.exit(1)
