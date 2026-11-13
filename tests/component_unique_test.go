package tests

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackmon/otc-status-dashboard/internal/db"
)

func newProbeComponent(name, region string) *db.Component {
	return &db.Component{
		Name: name,
		Attrs: []db.ComponentAttr{
			{Name: "region", Value: region},
			{Name: "category", Value: "Compute"},
			{Name: "type", Value: "ecs"},
		},
	}
}

// TestSaveComponentWaitsForNameRegionLock pins the lock key: a session that
// already holds the transaction lock for the same (name, region) pair must keep
// SaveComponent blocked until it releases it.
func TestSaveComponentWaitsForNameRegionLock(t *testing.T) {
	ctx := context.Background()
	d, g := newNotifDB(t)

	name := "lock probe " + uuid.NewString()
	comp := newProbeComponent(name, "EU-DE")

	conn, err := g.Conn(ctx)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	lockTx, err := conn.BeginTx(ctx, nil)
	require.NoError(t, err)

	_, err = lockTx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", name+"|EU-DE")
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		_, saveErr := d.SaveComponent(ctx, comp)
		done <- saveErr
	}()

	select {
	case err = <-done:
		t.Fatalf("SaveComponent completed while the name lock was held, err = %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	require.NoError(t, lockTx.Rollback())

	select {
	case err = <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("SaveComponent did not finish after the name lock was released")
	}

	assert.Equal(t, int64(1), tableCount(t, g, "component", "name = $1", name))
}

// TestSaveComponentConcurrentInsertKeepsSingleRow creates the same (name, region)
// pair from several goroutines: one call wins, the rest report ErrDBComponentExists.
func TestSaveComponentConcurrentInsertKeepsSingleRow(t *testing.T) {
	ctx := context.Background()
	d, g := newNotifDB(t)

	const workers = 8

	name := "concurrent probe " + uuid.NewString()
	comps := make([]*db.Component, workers)
	for i := range comps {
		comps[i] = newProbeComponent(name, "EU-DE")
	}

	start := make(chan struct{})
	errs := make([]error, workers)

	var wg sync.WaitGroup
	wg.Add(workers)

	for i := range comps {
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = d.SaveComponent(ctx, comps[i])
		}(i)
	}

	close(start)
	wg.Wait()

	created := 0
	for _, err := range errs {
		if err == nil {
			created++
			continue
		}
		require.ErrorIs(t, err, db.ErrDBComponentExists)
	}

	assert.Equal(t, 1, created, "exactly one creation must succeed")
	assert.Equal(t, int64(1), tableCount(t, g, "component", "name = $1", name))
	assert.Equal(t, int64(3), tableCount(t, g, "component_attribute",
		"component_id = (SELECT id FROM component WHERE name = $1)", name))
}
