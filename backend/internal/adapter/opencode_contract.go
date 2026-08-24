package adapter

// OpenCode upstream pinning constants.
//
// Single source of truth for which commit of github.com/halfking/opencode
// (or its mirror) the adapter speaks. Any change to the OpenCode wire
// contract requires bumping these and updating docs/opencode-contract.md.
//
// Pinned commit: f12ac6f234ebe31982ee78f3359e8170cb09ffc9
// Pinned release package version (packages/opencode/package.json): 1.17.9
// Pinned release date (commit date): 2026-06-21
// Pinned source repository: /Users/xutaohuang/workspace/ai/opencode
//
// The full contract and route inventory is in docs/opencode-contract.md.
//
// DO NOT introduce a RedClaw task API on top of these constants — Pocket's
// /api/agent/* and /api/opencode/* facades are a separate layer above the
// OpenCode contract, and the contract test (see opencode_http_contract_test.go)
// only validates the OpenCode side.

const (
	// OpenCodePinnedCommit is the upstream commit SHA the adapter speaks.
	OpenCodePinnedCommit = "f12ac6f234ebe31982ee78f3359e8170cb09ffc9"

	// OpenCodePinnedRelease is the upstream packages/opencode/package.json
	// version string at OpenCodePinnedCommit.
	OpenCodePinnedRelease = "1.17.9"

	// OpenCodePinnedReleaseDate is the ISO-8601 commit date of
	// OpenCodePinnedCommit (UTC).
	OpenCodePinnedReleaseDate = "2026-06-21"
)
