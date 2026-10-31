package main

import (
	"encoding/json"
	"testing"
)

func env1(subj string, n int) envelope {
	return envelope{Subject: subj, Topic: subj, TS: int64(n), Data: json.RawMessage(`{}`)}
}

// A slow client must see the latest value per subject, in first-arrival order, and never
// lose a subject — regardless of how many updates arrived while it was not reading.
func TestClientCoalescesLatestWins(t *testing.T) {
	h := newHub(2)
	c := h.register()
	if c == nil {
		t.Fatal("register returned nil under cap")
	}
	for i := 1; i <= 1000; i++ {
		h.publish(env1("cluster/nodes", i))
		h.publish(env1("visit/x", i))
	}
	got := c.take()
	if len(got) != 2 {
		t.Fatalf("want 2 coalesced envelopes, got %d", len(got))
	}
	if got[0].Subject != "cluster/nodes" || got[1].Subject != "visit/x" {
		t.Fatalf("arrival order lost: %v %v", got[0].Subject, got[1].Subject)
	}
	if got[0].TS != 1000 || got[1].TS != 1000 {
		t.Fatalf("not latest-wins: %d %d", got[0].TS, got[1].TS)
	}
	if len(c.take()) != 0 {
		t.Fatal("take did not clear the queue")
	}
	h.unregister(c)
}

func TestHubConnectionCap(t *testing.T) {
	h := newHub(2)
	a, b := h.register(), h.register()
	if a == nil || b == nil {
		t.Fatal("first two registrations must succeed")
	}
	if h.register() != nil {
		t.Fatal("third registration must be refused at cap 2")
	}
	h.unregister(a)
	if h.register() == nil {
		t.Fatal("slot must free up after unregister")
	}
}

func TestHubDrainWaitsForSessions(t *testing.T) {
	h := newHub(4)
	c := h.register()
	done := make(chan bool)
	go func() { done <- h.drain(2e9) }()
	h.unregister(c)
	if !<-done {
		t.Fatal("drain should return true once the last session unregisters")
	}
}
