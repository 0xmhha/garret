package clock

import (
	"testing"
	"time"
)

func TestSystemNow(t *testing.T) {
	before := time.Now()
	got := System{}.Now()
	after := time.Now()
	if got.Before(before) || got.After(after) {
		t.Fatalf("System.Now() = %v, want between %v and %v", got, before, after)
	}
}

func TestSystemImplementsClock(t *testing.T) {
	var _ Clock = System{}
}
