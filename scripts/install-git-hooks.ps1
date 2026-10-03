# install-git-hooks.ps1 -- activate the versioned pre-push hook for this clone.
#
# WHY THIS FILE IS ASCII-ONLY (do not add Chinese comments here):
# PowerShell 5.1 reads a BOM-less .ps1 using the system ANSI code page. UTF-8 Chinese
# comment bytes can decode to a trailing backslash, which turns the comment line into a
# line-continuation and silently swallows the NEXT line -- no error, exit code 0.
# That has already bitten this repo: a `secret-scan-ok` exemption whose reason was written
# in Chinese swallowed the very next line, which set POCKET_JWT_SECRET, changing script
# behaviour with no diagnostic. The write tool emits UTF-8 without BOM, so Chinese comments
# in a .ps1 are a live hazard, not a style question.
# Put Chinese explanation in docs/ instead; this file stays ASCII.
#
# What the hook does:
#   backend/** changed -> go test ./...   (full suite, not a hand-picked subset)
#   frontend/** changed -> npm run gates
# A red suite blocks the push. SKIP_PREPUSH=1 requires SKIP_PREPUSH_REASON and prints it.
#
# Why a hook instead of a commit template:
#   A template still asks a human to enumerate what was verified. That enumeration is
#   exactly what failed on 2026-10-03: the message for a5de5f96 said "internal/rss,
#   flashcards, config, server all pass", which was true, but it silently excluded
#   internal/repohygiene -- where the red actually was. Any list a human writes has a
#   boundary. A hook computes the answer at push time and nobody can shrink it.
#
# READ THIS BEFORE RUNNING IT -- the setting is REPO-WIDE, not per-worktree:
#   `git config core.hooksPath` is written to the shared .git/config. Measured 2026-10-03:
#   setting it from one worktree makes every other worktree of this repo see it too
#   (git rev-parse --git-common-dir resolves to the same .git in all of them).
#   Consequence: while a parallel session is pushing, its push would suddenly run the
#   full backend suite, and a pre-existing red would block it for reasons it cannot see.
#   Install only when no parallel session is pushing. Undo with:
#     git config --unset core.hooksPath

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$RepoRoot = (& git rev-parse --show-toplevel).Trim()
if (-not $RepoRoot) { throw 'not inside a git repository' }

$HooksDir = Join-Path $RepoRoot '.githooks'
$Hook = Join-Path $HooksDir 'pre-push'
if (-not (Test-Path $Hook)) { throw "missing $Hook" }

# A hook shipped in the repo is often not executable after a Windows checkout; the sh shebang
# is what matters, but make the bit correct anyway so a Linux/macOS clone works too.
try { & git update-index --chmod=+x -- $Hook 2>$null | Out-Null } catch { }

& git config core.hooksPath .githooks
if ($LASTEXITCODE -ne 0) { throw 'git config core.hooksPath failed' }

$Now = (& git config core.hooksPath).Trim()
Write-Output "[install-git-hooks] core.hooksPath = $Now"
if ($Now -ne '.githooks') { throw "core.hooksPath did not take ($Now)" }

Write-Output '[install-git-hooks] active: backend/** -> go test ./... ; frontend/** -> npm run gates'
Write-Output '[install-git-hooks] escape hatch: SKIP_PREPUSH=1 SKIP_PREPUSH_REASON="..." git push'
Write-Output '[install-git-hooks] a real pre-push run is NOT exercised here on purpose:'
Write-Output '[install-git-hooks] it would run the full backend suite. Verify with:'
Write-Output '[install-git-hooks]   node --test <hook-ab harness>  /  or just push a scratch branch.'
