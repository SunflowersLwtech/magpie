package gui

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Until the warm-up is over, a page's first ask and each of its reads
// take turns: none holds a first read of the history beside another
// (#2278). Once it is over, a page asks at once.
func TestColdTurnsTaken(t *testing.T) {
	coldTurnsTaken = true
	coldTurns.over.Store(false)
	t.Cleanup(func() { coldTurnsTaken = false; coldTurns.over.Store(false) })
	var inStep, inPage, both atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	page := coldTurn(func(http.ResponseWriter, *http.Request) {
		inPage.Add(1)
		if inStep.Load() > 0 {
			both.Add(1)
		}
		inPage.Add(-1)
	})
	ask := func() {
		page(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/usage/heatmap", nil))
	}
	warmed := make(chan struct{})
	go func() {
		defer close(warmed)
		warmUp(func() {
			inStep.Add(1)
			defer inStep.Add(-1)
			close(started)
			<-release
		}, func() {
			inStep.Add(1)
			defer inStep.Add(-1)
			if inPage.Load() > 0 {
				both.Add(1)
			}
		})
	}()
	answered := make(chan struct{})
	go func() {
		<-started
		ask()
		close(answered)
	}()
	select {
	case <-answered:
		t.Fatal("the page was answered while the warm-up's step read")
	case <-time.After(50 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	<-answered
	<-warmed
	if both.Load() != 0 {
		t.Fatal("a page and a warm-up step read at once")
	}
	// once the warm-up is over, nothing waits
	coldTurns.turn <- struct{}{}
	defer func() { <-coldTurns.turn }()
	done := make(chan struct{})
	go func() { ask(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a page after the warm-up waited for a turn")
	}
}
