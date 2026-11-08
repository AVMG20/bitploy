package app

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestBrokerFansOutToEveryStreamOfAUser(t *testing.T) {
	b := newBroker()
	s1, s2 := b.Subscribe("u1"), b.Subscribe("u1")
	b.Publish("u1", Event{Type: EventApp, App: "blog"})
	for i, s := range []*Subscription{s1, s2} {
		select {
		case <-s.Wake():
		case <-time.After(time.Second):
			t.Fatalf("stream %d was not woken", i)
		}
		got := s.Drain()
		if len(got) != 1 || got[0].App != "blog" {
			t.Fatalf("stream %d got %+v", i, got)
		}
	}
}

func TestBrokerNeverCrossesUsers(t *testing.T) {
	b := newBroker()
	a, other := b.Subscribe("alice"), b.Subscribe("bob")
	b.Publish("alice", Event{Type: EventDeployment, App: "a-app", ID: "dep_1", State: "building"})
	b.buildLine([]string{"alice"}, "a-app", "dep_1", "secret line")
	b.flushBuild("dep_1")
	if got := other.Drain(); len(got) != 0 {
		t.Fatalf("bob received alice's events: %+v", got)
	}
	select {
	case <-other.Wake():
		t.Fatal("bob was woken")
	default:
	}
	if got := a.Drain(); len(got) != 2 {
		t.Fatalf("alice got %+v", got)
	}
}

func TestBrokerCoalescesEventsAboutTheSameThing(t *testing.T) {
	b := newBroker()
	s := b.Subscribe("u")
	for _, st := range []string{"queued", "building", "starting"} {
		b.Publish("u", Event{Type: EventDeployment, App: "x", ID: "dep_1", State: st})
	}
	b.Publish("u", Event{Type: EventApp, App: "x"})
	b.Publish("u", Event{Type: EventApp, App: "x"})
	b.Publish("u", Event{Type: EventBuildLog, App: "x", ID: "dep_1", Lines: []string{"a"}})
	b.Publish("u", Event{Type: EventBuildLog, App: "x", ID: "dep_1", Lines: []string{"b", "c"}})
	got := s.Drain()
	if len(got) != 3 {
		t.Fatalf("want 3 events, got %+v", got)
	}
	if got[0].State != "starting" {
		t.Errorf("deployment: latest state must win: %+v", got[0])
	}
	if !reflect.DeepEqual(got[2].Lines, []string{"a", "b", "c"}) {
		t.Errorf("build lines must be joined: %+v", got[2])
	}
	if len(s.Drain()) != 0 {
		t.Error("drain must empty the queue")
	}
}

func TestBrokerSlowSubscriberDoesNotBlockAndGetsResync(t *testing.T) {
	b := newBroker()
	slow := b.Subscribe("u") // never drained
	fast := b.Subscribe("u")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 5000; i++ {
			b.Publish("u", Event{Type: EventApp, App: fmt.Sprintf("app-%d", i)})
			fast.Drain()
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a slow subscriber blocked the publisher")
	}
	got := slow.Drain()
	if len(got) != 1 || got[0].Type != EventResync {
		t.Fatalf("slow stream must get one resync, got %d events: %+v", len(got), got[:min(len(got), 3)])
	}
	// After the resync the stream works again.
	b.Publish("u", Event{Type: EventApp, App: "later"})
	if got := slow.Drain(); len(got) != 1 || got[0].App != "later" {
		t.Fatalf("stream did not recover: %+v", got)
	}
}

func TestBrokerUnsubscribe(t *testing.T) {
	b := newBroker()
	if b.Active() {
		t.Fatal("empty broker is active")
	}
	s := b.Subscribe("u")
	if !b.Active() || !b.listening("u") {
		t.Fatal("subscription not registered")
	}
	s.Close()
	s.Close() // twice is fine
	if b.Active() || b.listening("u") {
		t.Fatal("subscription still registered after Close")
	}
	b.Publish("u", Event{Type: EventApp, App: "x"}) // must not panic or queue
	if got := s.Drain(); len(got) != 0 {
		t.Fatalf("closed subscription received %+v", got)
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if len(b.subs) != 0 {
		t.Fatalf("subs map leaks: %v", b.subs)
	}
}

func TestBuildLinesAreBatched(t *testing.T) {
	b := newBroker()
	// Nobody listens: nothing is kept.
	b.buildLine([]string{"u"}, "x", "dep_1", "ignored")
	b.bmu.Lock()
	n := len(b.batches)
	b.bmu.Unlock()
	if n != 0 {
		t.Fatalf("lines kept without a listener: %d", n)
	}
	s := b.Subscribe("u")
	for i := 0; i < 3; i++ {
		b.buildLine([]string{"u"}, "x", "dep_1", fmt.Sprintf("line %d", i))
	}
	if got := s.Drain(); len(got) != 0 {
		t.Fatalf("lines sent before the batch interval: %+v", got)
	}
	select {
	case <-s.Wake():
	case <-time.After(2 * time.Second):
		t.Fatal("batch was not flushed")
	}
	got := s.Drain()
	if len(got) != 1 || got[0].Type != EventBuildLog || got[0].ID != "dep_1" || got[0].App != "x" ||
		!reflect.DeepEqual(got[0].Lines, []string{"line 0", "line 1", "line 2"}) {
		t.Fatalf("batch: %+v", got)
	}
	// flushBuild sends at once.
	b.buildLine([]string{"u"}, "x", "dep_2", "last")
	b.flushBuild("dep_2")
	if got := s.Drain(); len(got) != 1 || got[0].Lines[0] != "last" {
		t.Fatalf("flush: %+v", got)
	}
}

func TestNotifyDeploymentPublishesToTheOwnerOnly(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	other := e.addUser(t, "usr_other", "other@example.nl", "user")
	app := e.mustApp(t, CreateAppInput{})
	mine := e.s.Events().Subscribe(e.user.UserID)
	theirs := e.s.Events().Subscribe(other.UserID)
	defer mine.Close()
	defer theirs.Close()

	d, err := e.s.DeployFiles(ctx, e.user, app.Slug, staticFiles())
	if err != nil {
		t.Fatal(err)
	}
	e.exec(t, `UPDATE deployments SET state = 'building' WHERE id = ?`, d.ID)
	e.s.NotifyDeployment(d.ID)

	got := mine.Drain()
	if len(got) != 1 || got[0].Type != EventDeployment || got[0].App != app.Slug || got[0].ID != d.ID || got[0].State != "building" {
		t.Fatalf("owner got %+v", got)
	}
	if got := theirs.Drain(); len(got) != 0 {
		t.Fatalf("another user got %+v", got)
	}
}
