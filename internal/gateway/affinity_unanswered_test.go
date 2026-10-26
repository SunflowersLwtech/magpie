package gateway

import (
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// A conversation kept to a member (session affinity) whose answerer starts
// failing: the upstream error forgets it, so the next request goes by
// routing again rather than leading with the dead member every turn — the
// storm a pinned 429/402 used to cause.
func TestAffinityForgetsAFailingAnswerer(t *testing.T) {
	codexSignedIn(t, "spare@example.com")
	sticks.Lock()
	sticks.m, sticks.opening, sticks.from = map[string]stick{}, map[string]stick{}, ""
	sticks.Unlock()
	t.Cleanup(func() {
		sticks.Lock()
		sticks.m, sticks.opening, sticks.from = map[string]stick{}, map[string]stick{}, ""
		sticks.Unlock()
	})
	b := newResetBackend(t)
	if err := provider.SetRouting("codex", provider.Ordered); err != nil {
		t.Fatal(err)
	}
	if err := provider.SetAffinity("codex", provider.AffinitySession); err != nil {
		t.Fatal(err)
	}
	srv := New()

	// acct-1 down for the first turn: acct-2 answers, and the
	// conversation stays with it
	b.mu.Lock()
	b.fails["acct-1"] = 500
	b.mu.Unlock()
	if code, body, tried := strictPost(t, srv, b, "s1"); code != 200 || !strings.Contains(body, "pong") || tried != "acct-1,acct-2" {
		t.Fatalf("first turn: %d %s tried %s", code, body, tried)
	}

	// both down now: the pinned acct-2 is asked first, then acct-1 —
	// nothing answers and the request itself fails
	b.mu.Lock()
	b.fails["acct-2"] = 500
	b.mu.Unlock()
	if code, _, tried := strictPost(t, srv, b, "s1"); code < 400 || !strings.HasPrefix(tried, "acct-2") {
		t.Fatalf("pinned to the dead member: %d tried %s", code, tried)
	}

	// acct-1 healthy again, acct-2 still dead: the failed turn forgot
	// the stick, so routing leads with acct-1 — before the fix the dead
	// member went first every time
	b.mu.Lock()
	b.fails["acct-1"] = 0
	b.mu.Unlock()
	if code, body, tried := strictPost(t, srv, b, "s1"); code != 200 || !strings.Contains(body, "pong") || !strings.HasPrefix(tried, "acct-1") || strings.Contains(tried, "acct-2") {
		t.Fatalf("after the failure: %d %s tried %s", code, body, tried)
	}
}
