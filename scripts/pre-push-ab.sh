#!/bin/sh
# A/B harness for .githooks/pre-push, v2.
#
# v1 fed the hook through exported PRE_PUSH_* environment variables. That was the whole
# problem: git sets no such variables, so the harness supplied the interface it was
# testing and could never detect that the interface was fictional. v2 feeds the hook the
# way git actually does -- one ref line on STDIN -- and never sets those variables.
set -u

SH="/c/Program Files/Git/bin/sh.exe"
HOOK_SRC=$(cygpath -u "$1")
SBX=$(cygpath -u "$2")

rm -rf "$SBX"
mkdir -p "$SBX/repo/backend" "$SBX/repo/frontend" "$SBX/repo/docs" "$SBX/bin"

cd "$SBX/repo"
git init -q
git config user.email t@t.t
git config user.name t
mkdir -p .githooks
cp "$HOOK_SRC" .githooks/pre-push
chmod +x .githooks/pre-push
git config core.hooksPath .githooks

echo base > docs/readme.md
echo "package backend/x" > backend/x.go
echo "{}" > frontend/package.json
git add -A >/dev/null
git commit -qm base
BASE=$(git rev-parse HEAD)

# push a real ref through git, exactly as git does: <local ref> <sha> <remote ref> <sha>
remote_publish() { # $1 = branch, $2 = remote ref name
  "$SH" -c "cd '$SBX_U_REPO' && git push -q '$SBX_U' \"$1:refs/heads/$2\"" >/dev/null 2>&1 || true
}
SBX_U_REPO=$SBX/repo
SBX_U=$SBX

cat > "$SBX/bin/go" <<'STUB'
#!/bin/sh
echo "[stub go] $*"
exit ${STUB_GO_EXIT:-0}
STUB
cat > "$SBX/bin/npm" <<'STUB'
#!/bin/sh
echo "[stub npm] $*"
exit ${STUB_NPM_EXIT:-0}
STUB
chmod +x "$SBX/bin/go" "$SBX/bin/npm"
export PATH="$SBX/bin:$PATH"

pass=0; fail=0

# publish_to_remote <commit> <refname> : create the ref on the bare remote without the hook
publish() { git push -q --no-verify "$SBX_U" "$1:refs/heads/$2" >/dev/null 2>&1; }

# run_hook <remote_sha> <local_sha> <label> : feed the hook the way git does
run_hook() {
  printf 'refs/heads/probe %s refs/heads/probe %s\n' "$2" "$1" \
    | env -u PRE_PUSH_REMOTE_SHA -u PRE_PUSH_LOCAL_SHA "$SH" .githooks/pre-push > "$SBX/out.txt" 2>&1
  echo $?
}
expect() { # name want actual
  if [ "$2" = "$3" ]; then
    echo "PASS  $1  (exit=$3)"; pass=$((pass+1))
  else
    echo "FAIL  $1  want=$2 got=$3"; sed -n '1,10p' "$SBX/out.txt"; fail=$((fail+1))
  fi
}

publish "$BASE" probe

# T1 docs-only diff -> allow without running anything
echo doc > docs/readme.md; git add -A >/dev/null; git commit -qm docs
T1=$(git rev-parse HEAD)
expect "T1 docs-only diff is allowed" 0 "$(run_hook "$BASE" "$T1")"

# T2 backend diff, go green -> allow
echo "package backend/y" > backend/y.go; git add -A >/dev/null; git commit -qm be
T2=$(git rev-parse HEAD)
STUB_GO_EXIT=0 expect "T2 backend diff, go test green" 0 "$(run_hook "$T1" "$T2")"

# T3 backend diff, go red -> block
echo "package backend/z" > backend/z.go; git add -A >/dev/null; git commit -qm be2
T3=$(git rev-parse HEAD)
STUB_GO_EXIT=1 expect "T3 backend diff, go test red -> BLOCK" 1 "$(STUB_GO_EXIT=1 run_hook "$T2" "$T3")"

# T4 frontend diff, node_modules absent -> block
mkdir -p frontend/src; echo "x" > frontend/src/a.ts; git add -A >/dev/null; git commit -qm fe
T4=$(git rev-parse HEAD)
expect "T4 frontend diff, node_modules absent -> BLOCK" 1 "$(run_hook "$T3" "$T4")"

# T5 frontend diff, gates red -> block
mkdir -p frontend/node_modules
expect "T5 frontend diff, gates red -> BLOCK" 1 "$(STUB_NPM_EXIT=1 run_hook "$T3" "$T4")"

# T6 frontend diff, gates green -> allow
expect "T6 frontend diff, gates green -> allowed" 0 "$(STUB_NPM_EXIT=0 run_hook "$T3" "$T4")"

# T7 escape hatch without a reason -> block
expect "T7 SKIP_PREPUSH=1 without reason -> BLOCK" 2 "$(SKIP_PREPUSH=1 run_hook "$T2" "$T3")"

# T8 escape hatch with a reason -> allow, and print it
c=$(SKIP_PREPUSH=1 SKIP_PREPUSH_REASON="pre-existing red in pkg X, verified on origin/main" run_hook "$T2" "$T3")
expect "T8 SKIP_PREPUSH=1 with reason -> allowed" 0 "$c"
if grep -q "pre-existing red in pkg X" "$SBX/out.txt"; then
  echo "PASS  T8b the reason is printed, not silent"; pass=$((pass+1))
else
  echo "FAIL  T8b the reason was NOT printed"; fail=$((fail+1))
fi

# T9 go missing on a backend diff -> block (never assume green)
mv "$SBX/bin/go" "$SBX/bin/go.away"
expect "T9 go missing on a backend diff -> BLOCK" 1 "$(run_hook "$T2" "$T3")"
mv "$SBX/bin/go.away" "$SBX/bin/go"

# T10 brand-new remote ref (all-zero sha) must be treated as FULL scope, not as an empty diff
#     A diff from the empty tree touches everything; if this ever silently became
#     "nothing changed", a new branch would ship with no gate at all.
publish "$BASE" probe2
c=$(run_hook "0000000000000000000000000000000000000000" "$T3")
if grep -q "backend" "$SBX/out.txt"; then
  echo "PASS  T10 all-zero remote sha is treated as full scope"; pass=$((pass+1))
else
  echo "FAIL  T10 all-zero remote sha was NOT treated as full scope (exit=$c)"; fail=$((fail+1))
fi

# T11 no ref lines on stdin -> must say so and fall back, not silently allow
: | "$SH" .githooks/pre-push > "$SBX/out.txt" 2>&1
c=$?
if grep -q "no ref lines on stdin" "$SBX/out.txt"; then
  echo "PASS  T11 empty stdin is announced and falls back (exit=$c)"; pass=$((pass+1))
else
  echo "FAIL  T11 empty stdin was not announced"; fail=$((fail+1))
fi

echo "----"
echo "pass=$pass fail=$fail"
[ "$fail" -eq 0 ] || exit 1
