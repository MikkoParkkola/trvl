package cache

import (
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	c := New()
	defer func() { c.Close() }()

	if c.Len() != 0 {
		t.Errorf("new cache Len() = %d, want 0", c.Len())
	}
}

func TestSetAndGet(t *testing.T) {
	c := New()
	defer func() { c.Close() }()

	c.Set("key1", []byte("value1"), 5*time.Minute)

	data, ok := c.Get("key1")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if string(data) != "value1" {
		t.Errorf("got %q, want value1", string(data))
	}
}

func TestGet_Miss(t *testing.T) {
	c := New()
	defer func() { c.Close() }()

	_, ok := c.Get("nonexistent")
	if ok {
		t.Error("expected cache miss for nonexistent key")
	}
}

func TestGet_Expired(t *testing.T) {
	c := New()
	defer func() { c.Close() }()

	c.Set("key1", []byte("value1"), 1*time.Millisecond)
	time.Sleep(5 * time.Millisecond)

	_, ok := c.Get("key1")
	if ok {
		t.Error("expected cache miss for expired key")
	}
}

func TestSet_Overwrite(t *testing.T) {
	c := New()
	defer func() { c.Close() }()

	c.Set("key1", []byte("value1"), 5*time.Minute)
	c.Set("key1", []byte("value2"), 5*time.Minute)

	data, ok := c.Get("key1")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if string(data) != "value2" {
		t.Errorf("got %q, want value2", string(data))
	}
}

func TestLen(t *testing.T) {
	c := New()
	defer func() { c.Close() }()

	c.Set("a", []byte("1"), 5*time.Minute)
	c.Set("b", []byte("2"), 5*time.Minute)
	c.Set("c", []byte("3"), 5*time.Minute)

	if c.Len() != 3 {
		t.Errorf("Len() = %d, want 3", c.Len())
	}
}

func TestCleanup(t *testing.T) {
	c := New()
	defer func() { c.Close() }()

	c.Set("a", []byte("1"), 1*time.Millisecond)
	c.Set("b", []byte("2"), 5*time.Minute)

	time.Sleep(5 * time.Millisecond)
	c.cleanup()

	if c.Len() != 1 {
		t.Errorf("Len() after cleanup = %d, want 1", c.Len())
	}
}

func TestKey(t *testing.T) {
	k1 := Key("endpoint1", "payload1")
	k2 := Key("endpoint1", "payload2")
	k3 := Key("endpoint1", "payload1")

	if k1 == k2 {
		t.Error("different payloads should produce different keys")
	}
	if k1 != k3 {
		t.Error("same inputs should produce same key")
	}
	if len(k1) != 32 {
		t.Errorf("key length = %d, want 32 (hex-encoded 128 bits)", len(k1))
	}
}

func TestEviction_MaxEntries(t *testing.T) {
	c := NewWithMax(3)
	defer func() { c.Close() }()

	// Use explicit sleep between inserts to ensure distinct insertedAt
	// timestamps on Windows (time.Now() has ~15ms granularity).
	c.Set("a", []byte("1"), 5*time.Minute)
	time.Sleep(20 * time.Millisecond)
	c.Set("b", []byte("2"), 5*time.Minute)
	time.Sleep(20 * time.Millisecond)
	c.Set("c", []byte("3"), 5*time.Minute)

	if c.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", c.Len())
	}

	// Fourth entry should trigger eviction of the oldest ("a").
	c.Set("d", []byte("4"), 5*time.Minute)

	if c.Len() != 3 {
		t.Errorf("Len() after eviction = %d, want 3", c.Len())
	}

	// "a" should have been evicted (it has the earliest insertedAt).
	if _, ok := c.Get("a"); ok {
		t.Error("expected 'a' to be evicted")
	}

	// "d" should be present.
	if _, ok := c.Get("d"); !ok {
		t.Error("expected 'd' to be present")
	}
}

func TestNewWithMax_DefaultsOnZero(t *testing.T) {
	c := NewWithMax(0)
	defer func() { c.Close() }()

	if c.maxEntries != defaultMaxEntries {
		t.Errorf("maxEntries = %d, want %d", c.maxEntries, defaultMaxEntries)
	}
}

func TestConcurrentAccess(t *testing.T) {
	c := New()
	defer func() { c.Close() }()

	done := make(chan bool, 10)
	for i := range 10 {
		go func(n int) {
			key := Key("ep", string(rune(n)))
			c.Set(key, []byte("data"), 5*time.Minute)
			c.Get(key)
			done <- true
		}(i)
	}

	for range 10 {
		<-done
	}
}
