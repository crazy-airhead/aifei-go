package db_test

import (
	"sync"
	"testing"

	"github.com/crazy-airhead/aifei-go/db"

	_ "modernc.org/sqlite"
)

// TestConcurrentPoolInit verifies that concurrent first calls to Pool all
// receive the same lazily-created *sql.DB (previously a check-then-act race
// could open the pool twice and leak connections). Run with -race.
func TestConcurrentPoolInit(t *testing.T) {
	db.ResetConfigs()
	if err := db.InitWithID("race_pool", "sqlite", "file::memory:?cache=shared"); err != nil {
		t.Fatal(err)
	}
	c := db.GetConfig("race_pool")

	const n = 16
	pools := make([]interface{}, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			p, err := c.Pool()
			if err != nil {
				t.Errorf("Pool error: %v", err)
				return
			}
			pools[i] = p
		}(i)
	}
	close(start)
	wg.Wait()

	for i := 1; i < n; i++ {
		if pools[i] != pools[0] {
			t.Fatalf("concurrent Pool calls returned different instances: [0]=%p [%d]=%p", pools[0], i, pools[i])
		}
	}
	c.Close()
}

// TestConcurrentCloseAndPool exercises Close racing Pool on the same config;
// with the internal mutex the operations serialize and stay race-free.
func TestConcurrentCloseAndPool(t *testing.T) {
	db.ResetConfigs()
	if err := db.InitWithID("race_close", "sqlite", "file::memory:?cache=shared"); err != nil {
		t.Fatal(err)
	}
	c := db.GetConfig("race_close")

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, err := c.Pool(); err != nil {
				t.Errorf("Pool error: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			c.Close()
		}()
	}
	wg.Wait()
	// Final state must still be usable: Pool succeeds after the storm.
	if _, err := c.Pool(); err != nil {
		t.Fatalf("Pool after concurrent Close: %v", err)
	}
	c.Close()
	c.Close() // idempotent
}

// TestConcurrentRegistryAccess mixes InitWithID / GetConfig / ResetConfigs
// against the package-level config registry (previously an unguarded map).
func TestConcurrentRegistryAccess(t *testing.T) {
	db.ResetConfigs()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		id := "cfg"
		switch i % 3 {
		case 0:
			id = "cfg_writer"
		case 1:
			id = "main"
		}
		wg.Add(2)
		go func(id string) {
			defer wg.Done()
			if err := db.InitWithID(id, "sqlite", "file::memory:?cache=shared"); err != nil {
				t.Errorf("InitWithID error: %v", err)
			}
		}(id)
		go func() {
			defer wg.Done()
			_ = db.GetConfig("main")
			_ = db.GetConfig("cfg_writer")
			_ = db.GetConfig()
		}()
	}
	wg.Wait()
	if db.GetConfig("main") == nil || db.GetConfig("cfg_writer") == nil {
		t.Fatal("expected configs to survive concurrent access")
	}
	db.ResetConfigs()
}
