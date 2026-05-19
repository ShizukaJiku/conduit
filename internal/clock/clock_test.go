package clock

import (
	"testing"
	"time"
)

func TestSystemBasic(t *testing.T) {
	var c Clock = System{}
	start := c.Now()
	c.Sleep(time.Millisecond)
	if !c.Now().After(start) && c.Now().Equal(start) {
		// time is monotonic-ish; just ensure no panic and After works
	}
	select {
	case <-c.After(time.Millisecond):
	case <-time.After(time.Second):
		t.Fatal("System.After did not fire")
	}
}

func TestFakeSleepIsInstantAndRecorded(t *testing.T) {
	f := NewFake()
	start := f.Now()
	f.Sleep(200 * time.Millisecond)
	f.Sleep(5 * time.Second)
	if got := f.Sleeps(); len(got) != 2 || got[0] != 200*time.Millisecond || got[1] != 5*time.Second {
		t.Fatalf("Sleeps = %v", got)
	}
	if !f.Now().Equal(start.Add(200*time.Millisecond + 5*time.Second)) {
		t.Errorf("virtual clock did not advance: %v", f.Now())
	}
}

func TestFakeAfterFiresOnDemand(t *testing.T) {
	f := NewFake()
	ch := f.After(30 * time.Second)
	select {
	case <-ch:
		t.Fatal("After channel must not fire until Fire() is called")
	default:
	}
	if !f.Fire() {
		t.Fatal("Fire() should report a pending channel")
	}
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("After channel did not fire after Fire()")
	}
	if f.Fire() {
		t.Fatal("Fire() with nothing pending should return false")
	}
}
