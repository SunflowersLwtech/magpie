package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

// hostLost answers every request, after after, with the 502 a plugin's
// request reaches the gateway with when the plugin host was lost under
// it: the provider's name and the host's own words.
type hostLost struct {
	after time.Duration
	words string
	n     atomic.Int32
}

func (h *hostLost) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	io.ReadAll(r.Body)
	h.n.Add(1)
	time.Sleep(h.after)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadGateway)
	io.WriteString(w, `{"type":"error","error":{"type":"api_error","message":"ZCode: `+h.words+`"}}`)
}

// A plugin host lost under a request that had run a while is not asked the
// request again (2026-11-24: a GLM rewrite of ten minutes and more was cut
// when its replaced host's wait ran out, and asked twice more, each time
// charged in full and cut again). One lost at once is asked again as any
// 502 is, and so is a 502 of other words after the same while: only the
// host's loss after hostCutAfter changes.
func TestPluginHostLostAfterAWhileIsNotAskedAgain(t *testing.T) {
	oldPause, oldCut := retryPause, hostCutAfter
	retryPause, hostCutAfter = time.Millisecond, 80*time.Millisecond
	t.Cleanup(func() { retryPause, hostCutAfter = oldPause, oldCut })
	cases := []struct {
		name  string
		after time.Duration
		words string
		again bool // asked again, after a pause (how many times is the retries' and the patience's)
	}{
		{"writing to it failed, after a while", 150 * time.Millisecond, "writing to the plugin host: write |1: file already closed", false},
		{"it quit, after a while", 150 * time.Millisecond, "the plugin host quit: signal: killed", false},
		{"it was gone, after a while", 150 * time.Millisecond, "the plugin host is gone", false},
		{"writing to it failed at once", 0, "writing to the plugin host: write |1: file already closed", true},
		{"another 502, after a while", 150 * time.Millisecond, "upstream connect error or disconnect/reset before headers", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fresh(t)
			up := &hostLost{after: c.after, words: c.words}
			srv := httptest.NewServer(up)
			t.Cleanup(srv.Close)
			if err := provider.Save(provider.Provider{ID: "z", Name: "ZCode", Key: "k", Models: []string{"m"}, Anthropic: srv.URL}); err != nil {
				t.Fatal(err)
			}
			code, body := post(t, "/v1/messages", `{"model":"z/m","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`)
			if code != http.StatusBadGateway {
				t.Fatalf("status %d (%s), want the 502", code, body)
			}
			if !strings.Contains(body, c.words) {
				t.Errorf("the agent was told %s, want the host's words", body)
			}
			if n := up.n.Load(); c.again && n < 2 || !c.again && n != 1 {
				t.Errorf("the request was sent %d times, want it asked again %v", n, c.again)
			}
		})
	}
}

// A host lost under a group's member that had run a while has the next
// member asked, and the member it was lost under doesn't rest for it: the
// next request is sent to it first again.
func TestPluginHostLostGoesOnToTheNextMemberAndRestsNobody(t *testing.T) {
	oldPause, oldCut := retryPause, hostCutAfter
	retryPause, hostCutAfter = time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { retryPause, hostCutAfter = oldPause, oldCut })
	fresh(t)
	lost := &hostLost{after: 100 * time.Millisecond, words: "writing to the plugin host: write |1: file already closed"}
	a := httptest.NewServer(lost)
	t.Cleanup(a.Close)
	var bn atomic.Int32
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		bn.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	t.Cleanup(b.Close)
	for id, url := range map[string]string{"a": a.URL, "b": b.URL} {
		if err := provider.Save(provider.Provider{ID: id, Name: strings.ToUpper(id), Key: "k", Models: []string{"m"}, Anthropic: url}); err != nil {
			t.Fatal(err)
		}
	}
	if err := provider.SaveGroup(provider.Group{Name: "G", Members: []string{"a/m", "b/m"}, Routing: provider.Ordered}); err != nil {
		t.Fatal(err)
	}
	for round := 1; round <= 2; round++ {
		code, body := post(t, "/v1/messages", `{"model":"group/g","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`)
		if code != http.StatusOK || !strings.Contains(body, `"ok"`) {
			t.Fatalf("round %d: status %d (%s), want the next member's answer", round, code, body)
		}
		if n := lost.n.Load(); n != int32(round) {
			t.Fatalf("round %d: the member the host was lost under was asked %d times, want %d (asked once a request, and not rested)", round, n, round)
		}
		if n := bn.Load(); n != int32(round) {
			t.Fatalf("round %d: the next member was asked %d times, want %d", round, n, round)
		}
	}
}
