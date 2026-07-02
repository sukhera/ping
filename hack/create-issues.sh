#!/usr/bin/env bash
# One-time setup: creates labels, milestones, and one GitHub issue per PING-XXX
# ticket defined in TECH-PLAN.md §8.
#
# Prereqs: `gh auth login` completed. Run from the repo root:  ./hack/create-issues.sh
# Idempotent: labels are upserted, existing milestones/issues are skipped.
set -euo pipefail

command -v gh >/dev/null 2>&1 || { echo "error: gh CLI not installed"; exit 1; }
[ -f TECH-PLAN.md ] || { echo "error: run from the repo root (TECH-PLAN.md not found)"; exit 1; }

REPO=$(gh repo view --json nameWithOwner -q .nameWithOwner)
echo "repo: $REPO"

echo "==> labels"
while read -r name color; do
  gh label create "$name" --color "$color" --force >/dev/null
  echo "    $name"
done <<'EOF'
m0-foundation 6E9BF5
m1-heartbeat 2DD4A7
m2-alerts-dashboard F5B84B
m3-http 8B7CF6
m4-ship F4564E
backend 1F6FEB
frontend 3178C6
db 336791
infra 6E7781
docs 0075CA
feature A2EEEF
chore FEF2C0
size:S C2E0C6
size:M FBCA04
size:L D93F0B
EOF

echo "==> milestones"
for m in "M0" "M1" "M2" "M3" "M4"; do
  if gh api "repos/$REPO/milestones" -f title="$m" >/dev/null 2>&1; then
    echo "    $m created"
  else
    echo "    $m already exists"
  fi
done

echo "==> issues (extracted live from TECH-PLAN.md §8)"
python3 - <<'PYEOF'
import json, re, subprocess, sys, tempfile

src = open("TECH-PLAN.md", encoding="utf-8").read()

r = subprocess.run(["gh", "issue", "list", "--limit", "300", "--state", "all",
                    "--json", "title"], capture_output=True, text=True, check=True)
existing = {i["title"] for i in json.loads(r.stdout or "[]")}

milestone_of = {"m0": "M0", "m1": "M1", "m2": "M2", "m3": "M3", "m4": "M4"}
label_of = {"m0": "m0-foundation", "m1": "m1-heartbeat",
            "m2": "m2-alerts-dashboard", "m3": "m3-http", "m4": "m4-ship"}

blocks = re.split(r"\n(?=### PING-\d{3}: )", src)
created = skipped = 0
for b in blocks:
    m = re.match(r"### (PING-\d{3}): (.+)\n", b)
    if not m:
        continue
    tid, title_text = m.group(1), m.group(2).strip()
    title = f"{tid}: {title_text}"
    # body = everything up to the next ticket / milestone heading / section break
    body = re.split(r"\n### PING-\d{3}: |\n### M\d |\n---|\n## \d", b[m.end():])[0].strip()

    first_line = body.splitlines()[0] if body else ""
    tokens = re.findall(r"`([^`]+)`", first_line)
    labels = [label_of.get(t, t) for t in tokens]
    milestone = next((milestone_of[t] for t in tokens if t in milestone_of), None)

    if title in existing:
        print(f"    {tid} already exists — skipped")
        skipped += 1
        continue

    full_body = (f"> Source of truth: [TECH-PLAN.md §8](../blob/main/TECH-PLAN.md) — "
                 f"read the whole section plus §9 (Definition of Done) before starting.\n\n{body}")
    with tempfile.NamedTemporaryFile("w", suffix=".md", delete=False) as f:
        f.write(full_body)
        path = f.name

    cmd = ["gh", "issue", "create", "--title", title, "--body-file", path,
           "--label", ",".join(labels)]
    if milestone:
        cmd += ["--milestone", milestone]
    subprocess.run(cmd, check=True, capture_output=True, text=True)
    print(f"    {title}")
    created += 1

print(f"\ndone: {created} created, {skipped} skipped")
if created + skipped < 24:
    sys.exit(f"warning: expected at least 24 tickets, found {created + skipped} — check TECH-PLAN.md §8")
PYEOF

echo "all set — view with: gh issue list --milestone M0"
