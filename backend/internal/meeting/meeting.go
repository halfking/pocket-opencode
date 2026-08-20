// Package meeting provides PostgreSQL-backed meeting metadata cache for
// cloud sync. Full transcript/segments stay on-device (lobster architecture);
// pocketd stores summary, status, and note linkage for multi-device list.
//
// The canonical `Meeting` struct, request types, and the in-memory `Store`
// live in types.go and store.go respectively (the S0-A tenant-scoped schema).
// This file exists only to keep the package doc-comment grouped together for
// godoc; the legacy duplicate `Meeting` definition was removed when the
// pre-S0 metadata fields (UserID / int64 timestamps / Location / Participants
// / RefinedTranscript / NoteID) were retired in favor of OwnerID + WorkspaceID
// + time.Time on the scoped Meeting type.
package meeting
