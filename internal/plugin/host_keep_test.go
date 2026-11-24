package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// keepSandbox is a plugins.json with a plugin the host loads (an npm one)
// and one all of which runs in magpie (a path to a *.middleware.js), as
// another magpie writes it: rewritten whole, a second later each time,
// so its stamp moves.
func keepSandbox(t *testing.T) (mw string, write func(body string)) {
	t.Helper()
	dir := storeSandbox(t)
	mw = filepath.Join(dir, "lease.middleware.js")
	if err := os.WriteFile(mw, []byte("export function onRequest(body, ctx) { return body }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !InMagpieOnly(Target(mw)) {
		t.Fatal("the middleware file isn't one all of which runs in magpie: the test checks nothing")
	}
	at := time.Now().Add(-time.Hour)
	write = func(body string) {
		t.Helper()
		writeList(t, strings.ReplaceAll(body, "MW", mw))
		at = at.Add(time.Second)
		if err := os.Chtimes(listPath(), at, at); err != nil {
			t.Fatal(err)
		}
	}
	prev := hostStale.Swap(false)
	t.Cleanup(func() { hostStale.Store(prev) })
	return mw, write
}

func epoch() uint64 {
	provMu.Lock()
	defer provMu.Unlock()
	return provEpoch
}

// TestMiddlewareOptionsLeaveTheHost (2026-11-24): a timer renewed a lease in
// a middleware's options every two minutes from a terminal (`magpie plugin
// options`), and every renewal replaced the plugin host, which is handed
// nothing of that middleware. A request a plugin was answering went on on
// the replaced host, and was cut when its retireWait ran out: every ZCode
// request past ten minutes failed. Another magpie's change to such a
// plugin's options leaves the host as it is, and the providers it lists;
// a change to what the host is handed still replaces it.
func TestMiddlewareOptionsLeaveTheHost(t *testing.T) {
	_, write := keepSandbox(t)
	write(`{"plugins":[{"spec":"@magpie-community/opencode-zcode-auth"},{"spec":"MW","options":{"seatUntil":1}}]}`)
	checkList() // the plugins as this magpie has seen them (a test before may have left another list seen)
	hostStale.Store(false)

	steps := []struct {
		name  string
		body  string
		stale bool
	}{
		{"the middleware's lease renewed", `{"plugins":[{"spec":"@magpie-community/opencode-zcode-auth"},{"spec":"MW","options":{"seatUntil":2}}]}`, false},
		{"the middleware's options taken away", `{"plugins":[{"spec":"@magpie-community/opencode-zcode-auth"},{"spec":"MW"}]}`, false},
		{"the middleware's options set again", `{"plugins":[{"spec":"@magpie-community/opencode-zcode-auth"},{"spec":"MW","options":{"seatUntil":3,"sessions":["a"]}}]}`, false},
		{"the host's plugin given options", `{"plugins":[{"spec":"@magpie-community/opencode-zcode-auth","options":{"x":1}},{"spec":"MW","options":{"seatUntil":3,"sessions":["a"]}}]}`, true},
		{"the middleware turned off", `{"plugins":[{"spec":"@magpie-community/opencode-zcode-auth","options":{"x":1}},{"spec":"MW","off":true,"options":{"seatUntil":3,"sessions":["a"]}}]}`, true},
		{"a plugin added", `{"plugins":[{"spec":"@magpie-community/opencode-zcode-auth","options":{"x":1}},{"spec":"MW","off":true,"options":{"seatUntil":3,"sessions":["a"]}},{"spec":"@magpie-community/opencode-zed-auth"}]}`, true},
		{"the config the host is handed", `{"plugins":[{"spec":"@magpie-community/opencode-zcode-auth","options":{"x":1}},{"spec":"MW","off":true,"options":{"seatUntil":3,"sessions":["a"]}},{"spec":"@magpie-community/opencode-zed-auth"}],"config":{"provider":{"zcode":{"options":{"a":1}}}}}`, true},
		{"the off middleware's lease renewed", `{"plugins":[{"spec":"@magpie-community/opencode-zcode-auth","options":{"x":1}},{"spec":"MW","off":true,"options":{"seatUntil":4,"sessions":["a"]}},{"spec":"@magpie-community/opencode-zed-auth"}],"config":{"provider":{"zcode":{"options":{"a":1}}}}}`, false},
	}
	for _, s := range steps {
		before := epoch()
		write(s.body)
		checkList()
		if got := hostStale.Swap(false); got != s.stale {
			t.Errorf("%s: host stale %v, want %v", s.name, got, s.stale)
		}
		if forgot := epoch() != before; forgot != s.stale {
			t.Errorf("%s: the providers forgotten %v, want %v", s.name, forgot, s.stale)
		}
	}
}

// What bun installed changing with plugins.json's middleware options in
// the same moment still replaces the host: an update's version is in
// package.json and the lockfile, not in the options compared.
func TestMiddlewareOptionsWithAnInstallReplaceTheHost(t *testing.T) {
	_, write := keepSandbox(t)
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(Dir(), "bun.lock")
	if err := os.WriteFile(lock, []byte(`"@magpie-community/opencode-zcode-auth@0.1.69"`), 0o644); err != nil {
		t.Fatal(err)
	}
	write(`{"plugins":[{"spec":"@magpie-community/opencode-zcode-auth"},{"spec":"MW","options":{"seatUntil":1}}]}`)
	checkList()
	hostStale.Store(false)

	if err := os.WriteFile(lock, []byte(`"@magpie-community/opencode-zcode-auth@0.1.70"`), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(lock, later, later); err != nil {
		t.Fatal(err)
	}
	write(`{"plugins":[{"spec":"@magpie-community/opencode-zcode-auth"},{"spec":"MW","options":{"seatUntil":2}}]}`)
	checkList()
	if !hostStale.Swap(false) {
		t.Fatal("a new version installed, and the host running the old one is not stale")
	}
}

// This magpie's own SetOptions on a plugin all of which runs in magpie
// doesn't replace the host either, nor does its next look take its own
// write for another magpie's; on a plugin the host loads it does.
func TestSetOptionsOfAMiddlewareLeavesTheHost(t *testing.T) {
	mw, write := keepSandbox(t)
	write(`{"plugins":[{"spec":"@magpie-community/opencode-zcode-auth"},{"spec":"MW","options":{"seatUntil":1}}]}`)
	checkList()
	hostStale.Store(false)

	before := epoch()
	if err := SetOptions(mw, map[string]any{"seatUntil": float64(2)}); err != nil {
		t.Fatal(err)
	}
	if got := Load().Plugins[1].Options["seatUntil"]; got != float64(2) {
		t.Fatalf("options read back %v, want the lease written", got)
	}
	checkList()
	if hostStale.Load() || epoch() != before {
		t.Fatalf("a middleware's options set here: host stale %v, providers forgotten %v; want neither", hostStale.Load(), epoch() != before)
	}

	if err := SetOptions("@magpie-community/opencode-zcode-auth", map[string]any{"x": float64(1)}); err != nil {
		t.Fatal(err)
	}
	if epoch() == before {
		t.Fatal("the options of a plugin the host loads set, and the host was not started again (no Restart)")
	}
	checkList()
	if hostStale.Swap(false) {
		t.Fatal("this magpie's own restart was taken for another magpie's change")
	}
}

// Another magpie's write that lands between seeList's two reads is not
// taken as seen: its plugin's options would be in what seeList keeps of
// the host, under the stamp from before the write, and the next look
// would leave the host running without them (review on #4).
func TestAWriteBetweenSeeListsReadsReplacesTheHost(t *testing.T) {
	_, write := keepSandbox(t)
	write(`{"plugins":[{"spec":"@magpie-community/opencode-zcode-auth"},{"spec":"MW","options":{"seatUntil":1}}]}`)
	checkList()
	hostStale.Store(false)

	was := hostPartOf
	t.Cleanup(func() { hostPartOf = was })
	hostPartOf = func() string {
		hostPartOf = was
		write(`{"plugins":[{"spec":"@magpie-community/opencode-zcode-auth","options":{"x":1}},{"spec":"MW","options":{"seatUntil":1}}]}`)
		return was()
	}
	seeList(true)
	checkList()
	if !hostStale.Swap(false) {
		t.Fatal("another magpie gave the host's plugin options between seeList's reads, and the host was not replaced")
	}
}
