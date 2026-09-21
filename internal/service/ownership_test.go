package service

import (
	"strings"
	"testing"
)

// TestDecideOwnership covers the rule that makes "mint only your own profile"
// enforceable: a profile is owned only when the signed-in login matches the
// analysed username, case-insensitively.
//
// This is the pure decision factored out of Analyze, so it can be verified
// without a live GitHub call.
func TestDecideOwnership(t *testing.T) {
	cases := []struct {
		name         string
		viewer       string
		analyzed     string
		wantOwned    bool
		wantStoredAs string
	}{
		{"signed in as self", "alice", "alice", true, "alice"},
		{"different case is still self", "Alice", "alice", true, "Alice"},
		{"looking at somebody else", "alice", "torvalds", false, ""},
		{"anonymous viewer", "", "torvalds", false, ""},
		{"anonymous on own name", "", "alice", false, ""},
		{"whitespace in viewer login", "  ", "alice", false, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			owned, stored := decideOwnership(tc.viewer, tc.analyzed)
			if owned != tc.wantOwned {
				t.Errorf("owned = %v, want %v", owned, tc.wantOwned)
			}
			if stored != tc.wantStoredAs {
				t.Errorf("stored login = %q, want %q", stored, tc.wantStoredAs)
			}
		})
	}
}

// TestClaimMismatchIsUnverified pins the wording used by VerifyClaim so the UI
// and the API stay consistent.
func TestClaimMismatchReason(t *testing.T) {
	reason := claimMismatchReason("torvalds", "alice")

	if !strings.Contains(reason, "torvalds") || !strings.Contains(reason, "alice") {
		t.Errorf("reason should name both identities, got %q", reason)
	}
	if !strings.Contains(reason, "unverified") && !strings.Contains(reason, "signed in") {
		t.Errorf("reason should explain the mismatch, got %q", reason)
	}
}

// TestAPIClientCannotForgeUsernameByApostrophe is a light input-hardening check:
// usernames never contain quotes or markup, so any that do must be rejected
// before reaching the metadata builder.
func TestUsernameRejectsInjectionAttempts(t *testing.T) {
	bad := []string{
		`alice" onload="alert(1)`,
		`<script>alert(1)</script>`,
		`alice&bob`,
		`alice bob`,
		`../../etc/passwd`,
	}

	for _, u := range bad {
		if ValidUsername(u) {
			t.Errorf("%q must not be accepted as a GitHub username", u)
		}
	}
}
