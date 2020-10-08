package registry

import (
	"bytes"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

// testDir creates a temporary dir for testing.
func testDir(name string) string {
	dir := build.TempDir(name)
	_ = os.RemoveAll(dir)
	err := os.MkdirAll(dir, modules.DefaultDirPerm)
	if err != nil {
		panic(err)
	}
	return dir
}

// TestDeleteEntry is a unit test for managedDeleteEntry.
func TestDeleteEntry(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	dir := testDir(t.Name())

	// Create a new registry.
	registryPath := filepath.Join(dir, "registry")
	r, err := New(registryPath, testingDefaultMaxEntries)
	if err != nil {
		t.Fatal(err)
	}
	defer func(c io.Closer) {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}(r)

	// No bit should be used.
	for i := uint64(0); i < r.usage.Len(); i++ {
		if r.usage.IsSet(i) {
			t.Fatal("no page should be in use")
		}
	}

	// Register a value.
	rv, v, _ := randomValue(0)
	updated, err := r.Update(rv, v.key, v.expiry)
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("key shouldn't have existed before")
	}
	if len(r.entries) != 1 {
		t.Fatal("registry should contain one entry", len(r.entries))
	}
	vExists, exists := r.entries[v.mapKey()]
	if !exists {
		t.Fatal("enry doesn't exist")
	}

	// The bit should be set.
	if !r.usage.IsSet(uint64(vExists.staticIndex) - 1) {
		t.Fatal("bit wasn't set")
	}

	// Delete the value.
	r.managedDeleteFromMemory(vExists)

	// Map should be empty now.
	if len(r.entries) != 0 {
		t.Fatal("registry should be empty", len(r.entries))
	}

	// No bit should be used again.
	for i := uint64(0); i < r.usage.Len(); i++ {
		if r.usage.IsSet(i) {
			t.Fatal("no page should be in use")
		}
	}
}

// TestNew is a unit test for New. It confirms that New can initialize an empty
// registry and load existing items from disk.
func TestNew(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	dir := testDir(t.Name())

	// Create a new registry.
	registryPath := filepath.Join(dir, "registry")
	r, err := New(registryPath, testingDefaultMaxEntries)
	if err != nil {
		t.Fatal(err)
	}
	defer func(c io.Closer) {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}(r)

	// No bit should be used.
	for i := uint64(0); i < r.usage.Len(); i++ {
		if r.usage.IsSet(i) {
			t.Fatal("no page should be in use")
		}
	}

	// The first call should simply init it. Check the size and version.
	expected := make([]byte, PersistedEntrySize)
	copy(expected[:], registryVersion[:])
	b, err := ioutil.ReadFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b[:PersistedEntrySize], expected) {
		t.Fatal("metadata doesn't match")
	}

	// The entries map should be empty.
	if len(r.entries) != 0 {
		t.Fatal("registry shouldn't contain any entries")
	}

	// Save a random unused entry at the first index and a used entry at the
	// second index.
	_, vUnused, _ := randomValue(1)
	_, vUsed, _ := randomValue(2)
	err = r.staticSaveEntry(vUnused, false)
	if err != nil {
		t.Fatal(err)
	}
	err = r.staticSaveEntry(vUsed, true)
	if err != nil {
		t.Fatal(err)
	}

	// Load the registry again. 'New' should load the used entry from disk but
	// not the unused one.
	r, err = New(registryPath, testingDefaultMaxEntries)
	if err != nil {
		t.Fatal(err)
	}
	defer func(c io.Closer) {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}(r)
	if len(r.entries) != 1 {
		t.Fatal("registry should contain one entry", len(r.entries))
	}
	v, exists := r.entries[vUsed.mapKey()]
	if !exists || !reflect.DeepEqual(v, vUsed) {
		t.Log(v)
		t.Log(vUsed)
		t.Fatal("registry contains wrong key-value pair")
	}

	// Loaded page should be in use.
	for i := uint64(0); i < r.usage.Len(); i++ {
		if r.usage.IsSet(i) != (i == uint64(v.staticIndex-1)) {
			t.Fatal("wrong page is set")
		}
	}

	// Try to create a registry at a relative path. This shouldn't work.
	registryPath = "./registry.dat"
	_, err = New(registryPath, testingDefaultMaxEntries)
	if !errors.Contains(err, errPathNotAbsolute) {
		t.Fatal(err)
	}
}

// TestUpdate is a unit test for Update. It makes sure new entries are added
// correctly, old ones are updated and that unused slots on disk are filled.
func TestUpdate(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	dir := testDir(t.Name())

	// Create a new registry.
	registryPath := filepath.Join(dir, "registry")
	r, err := New(registryPath, testingDefaultMaxEntries)
	if err != nil {
		t.Fatal(err)
	}
	defer func(c io.Closer) {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}(r)

	// Register a value.
	rv, v, sk := randomValue(2)
	updated, err := r.Update(rv, v.key, v.expiry)
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("key shouldn't have existed before")
	}
	if len(r.entries) != 1 {
		t.Fatal("registry should contain one entry", len(r.entries))
	}
	vExist, exists := r.entries[v.mapKey()]
	if !exists {
		t.Fatal("entry doesn't exist")
	}
	v.staticIndex = vExist.staticIndex
	if !reflect.DeepEqual(vExist, v) {
		t.Log(v)
		t.Log(vExist)
		t.Fatal("registry contains wrong key-value pair")
	}

	// Update the same key again. This shouldn't work cause the revision is the
	// same.
	_, err = r.Update(rv, v.key, v.expiry)
	if !errors.Contains(err, errInvalidRevNum) {
		t.Fatal("expected invalid rev number")
	}

	// Try again with a higher revision number. This should work.
	v.revision++
	rv.Revision++
	rv = rv.Sign(sk)
	v.signature = rv.Signature
	updated, err = r.Update(rv, v.key, v.expiry)
	if err != nil {
		t.Fatal(err)
	}
	if !updated {
		t.Fatal("key should have existed before")
	}
	r, err = New(registryPath, testingDefaultMaxEntries)
	if err != nil {
		t.Fatal(err)
	}
	defer func(c io.Closer) {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}(r)
	if len(r.entries) != 1 {
		t.Fatal("registry should contain one entry", len(r.entries))
	}
	vExist, exists = r.entries[v.mapKey()]
	if !exists {
		t.Fatal("entry doesn't exist")
	}
	if !reflect.DeepEqual(vExist, v) {
		t.Log(v)
		t.Log(vExist)
		t.Fatal("registry contains wrong key-value pair")
	}

	// Try another update with too much data.
	v.revision++
	rv.Revision++
	rv = rv.Sign(sk)
	v.data = make([]byte, modules.RegistryDataSize+1)
	rv.Data = v.data
	_, err = r.Update(rv, v.key, v.expiry)
	if !errors.Contains(err, errTooMuchData) {
		t.Fatal("expected too much data")
	}
	v.data = make([]byte, modules.RegistryDataSize)

	// Add a second entry.
	rv2, v2, _ := randomValue(2)
	v2.staticIndex = 2 // expected index
	updated, err = r.Update(rv2, v2.key, v2.expiry)
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("key shouldn't have existed before")
	}
	if len(r.entries) != 2 {
		t.Fatal("registry should contain two entries", len(r.entries))
	}
	vExist, exists = r.entries[v2.mapKey()]
	if !exists {
		t.Fatal("entry doesn't exist")
	}
	v2.staticIndex = vExist.staticIndex
	if !reflect.DeepEqual(vExist, v2) {
		t.Log(v2)
		t.Log(vExist)
		t.Fatal("registry contains wrong key-value pair")
	}

	// Mark the first entry as unused and save it to disk.
	err = r.staticSaveEntry(v, false)
	if err != nil {
		t.Fatal(err)
	}

	// Reload the registry. Only the second entry should exist.
	r, err = New(registryPath, testingDefaultMaxEntries)
	if err != nil {
		t.Fatal(err)
	}
	defer func(c io.Closer) {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}(r)
	if len(r.entries) != 1 {
		t.Fatal("registry should contain one entries", len(r.entries))
	}
	if vExist, exists := r.entries[v2.mapKey()]; !exists || !reflect.DeepEqual(vExist, v2) {
		t.Log(v2)
		t.Log(vExist)
		t.Fatal("registry contains wrong key-value pair")
	}

	// Update the registry with a third entry. It should get the index that the
	// first entry had before.
	rv3, v3, sk3 := randomValue(2)
	v3.staticIndex = v.staticIndex // expected index
	updated, err = r.Update(rv3, v3.key, v3.expiry)
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("key shouldn't have existed before")
	}
	if len(r.entries) != 2 {
		t.Fatal("registry should contain two entries", len(r.entries))
	}
	vExist, exists = r.entries[v3.mapKey()]
	if !exists {
		t.Fatal("entry doesn't exist")
	}
	v3.staticIndex = vExist.staticIndex
	if !reflect.DeepEqual(vExist, v3) {
		t.Log(v3)
		t.Log(vExist)
		t.Fatal("registry contains wrong key-value pair")
	}

	// Update the registry with the third entry again but increment the revision
	// number without resigning. This should fail.
	rv3.Revision++
	updated, err = r.Update(rv3, v3.key, v3.expiry)
	if !errors.Contains(err, errInvalidSignature) {
		t.Fatal(err)
	}

	// Mark v3 invalid and try to update it. This should fail.
	rv3.Revision++
	rv3 = rv3.Sign(sk3)
	vExist, exists = r.entries[v3.mapKey()]
	if !exists {
		t.Fatal("entry doesn't exist")
	}
	vExist.invalid = true
	updated, err = r.Update(rv3, v3.key, v3.expiry)
	if !errors.Contains(err, errInvalidEntry) {
		t.Fatal("should fail with invalid entry error")
	}
}

// TestRegistryLimit checks if the bitfield of the limit enforces its
// preallocated size.
func TestRegistryLimit(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	dir := testDir(t.Name())

	// Create a new registry.
	registryPath := filepath.Join(dir, "registry")
	limit := uint64(128)
	r, err := New(registryPath, limit)
	if err != nil {
		t.Fatal(err)
	}
	defer func(c io.Closer) {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}(r)

	// Add entries up until the limit.
	for i := uint64(0); i < limit; i++ {
		rv, v, _ := randomValue(0)
		_, err = r.Update(rv, v.key, v.expiry)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Next one should fail.
	rv, v, _ := randomValue(0)
	_, err = r.Update(rv, v.key, v.expiry)
	if !errors.Contains(err, ErrNoFreeBit) {
		t.Fatal(err)
	}
}

// TestPrune is a unit test for Prune.
func TestPrune(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	dir := testDir(t.Name())

	// Create a new registry.
	registryPath := filepath.Join(dir, "registry")
	r, err := New(registryPath, testingDefaultMaxEntries)
	if err != nil {
		t.Fatal(err)
	}
	defer func(c io.Closer) {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}(r)

	// Add 2 entries with different expiries.
	rv1, v1, _ := randomValue(0)
	v1.expiry = 1
	_, err = r.Update(rv1, v1.key, v1.expiry)
	if err != nil {
		t.Fatal(err)
	}
	rv2, v2, _ := randomValue(0)
	v2.expiry = 2
	_, err = r.Update(rv2, v2.key, v2.expiry)
	if err != nil {
		t.Fatal(err)
	}

	// Should have 2 entries.
	if len(r.entries) != 2 {
		t.Fatal("wrong number of entries")
	}

	// Remember the entries for later.
	var entrySlice []*value
	for _, entry := range r.entries {
		entrySlice = append(entrySlice, entry)
	}

	// Check bitfield.
	inUse := 0
	for i := uint64(0); i < r.usage.Len(); i++ {
		if r.usage.IsSet(i) {
			inUse++
		}
	}
	if inUse != len(r.entries) {
		t.Fatalf("expected %v bits to be in use", len(r.entries))
	}

	// Prune 1 of them.
	n, err := r.Prune(1)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("1 entry should have been pruned")
	}

	// Should have 1 entry.
	if len(r.entries) != 1 {
		t.Fatal("wrong number of entries")
	}
	vExist, exists := r.entries[v2.mapKey()]
	if !exists || vExist.invalid {
		t.Fatal("entry doesn't exist or is marked invalid")
	}
	v2.staticIndex = vExist.staticIndex
	if !reflect.DeepEqual(vExist, v2) {
		t.Log(v2)
		t.Log(vExist)
		t.Fatal("registry contains wrong key-value pair")
	}

	// One entry should be invalid and the other one good.
	for _, entry := range entrySlice {
		if entry.invalid != (entry.mapKey() == v1.mapKey()) {
			t.Fatal("v1 should be invalid and v2 should be valid")
		}
	}

	// Check bitfield.
	inUse = 0
	for i := uint64(0); i < r.usage.Len(); i++ {
		if r.usage.IsSet(i) {
			inUse++
		}
	}
	if inUse != len(r.entries) {
		t.Fatalf("expected %v bits to be in use", len(r.entries))
	}

	// Restart.
	r, err = New(registryPath, testingDefaultMaxEntries)
	if err != nil {
		t.Fatal(err)
	}
	defer func(c io.Closer) {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}(r)

	// Should have 1 entry.
	if len(r.entries) != 1 {
		t.Fatal("wrong number of entries")
	}
	if vExist, exists := r.entries[v2.mapKey()]; !exists || !reflect.DeepEqual(vExist, v2) {
		t.Log(v2)
		t.Log(vExist)
		t.Fatal("registry contains wrong key-value pair")
	}

	// Check bitfield.
	inUse = 0
	for i := uint64(0); i < r.usage.Len(); i++ {
		if r.usage.IsSet(i) {
			inUse++
		}
	}
	if inUse != len(r.entries) {
		t.Fatalf("expected %v bits to be in use", len(r.entries))
	}
}

// TestFullRegistry tests filling up a whole registry, reloading it and pruning
// it.
func TestFullRegistry(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	dir := testDir(t.Name())

	// Create a new registry.
	registryPath := filepath.Join(dir, "registry")
	numEntries := uint64(128)
	r, err := New(registryPath, numEntries)
	if err != nil {
		t.Fatal(err)
	}
	defer func(c io.Closer) {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}(r)

	// Fill it completely.
	vals := make([]*value, 0, numEntries)
	for i := uint64(0); i < numEntries; i++ {
		rv, v, _ := randomValue(0)
		v.expiry = types.BlockHeight(i)
		v.signature = rv.Signature
		u, err := r.Update(rv, v.key, v.expiry)
		if err != nil {
			t.Fatal(err)
		}
		if u {
			t.Fatal("entry shouldn't exist")
		}
		vals = append(vals, v)
	}

	// Try one more entry. This should fail.
	rv, v, _ := randomValue(0)
	_, err = r.Update(rv, v.key, v.expiry)
	if !errors.Contains(err, ErrNoFreeBit) {
		t.Fatal(err)
	}

	// Reload it.
	r, err = New(registryPath, numEntries)
	if err != nil {
		t.Fatal(err)
	}
	defer func(c io.Closer) {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}(r)

	// Check number of entries.
	if uint64(len(r.entries)) != numEntries {
		t.Fatal(err)
	}
	for _, val := range vals {
		valExist, exists := r.entries[val.mapKey()]
		if !exists {
			t.Fatal("entry not found")
		}
		val.staticIndex = valExist.staticIndex
		if !reflect.DeepEqual(valExist, val) {
			t.Log(valExist)
			t.Log(val)
			t.Fatal("vals don't match")
		}
		if val.invalid {
			t.Fatal("entry shouldn't be invalid")
		}
		// Verify signatures.
		rv := modules.NewSignedRegistryValue(val.tweak, val.data, val.revision, val.signature)
		err = rv.Verify(val.key.ToPublicKey())
		if err != nil {
			t.Fatal(err)
		}
	}

	// Remember the entries for after the prune + reload.
	entryMap := make(map[crypto.Hash]*value)
	for k, v := range r.entries {
		entryMap[k] = v
	}

	// Prune expiry numEntries-1. This should leave half the entries.
	n, err := r.Prune(types.BlockHeight(numEntries/2 - 1))
	if err != nil {
		t.Fatal(err)
	}
	if n != numEntries/2 {
		t.Fatal("expected half of the entries to be pruned")
	}

	// Reload it.
	r, err = New(registryPath, numEntries)
	if err != nil {
		t.Fatal(err)
	}
	defer func(c io.Closer) {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}(r)

	// Check number of entries. Second half should still be in there.
	if uint64(len(r.entries)) != numEntries/2 {
		t.Fatal(len(r.entries), numEntries/2)
	}
	for _, val := range vals[numEntries/2:] {
		valExist, exists := r.entries[val.mapKey()]
		if !exists {
			t.Fatal("entry not found")
		}
		val.staticIndex = valExist.staticIndex
		if !reflect.DeepEqual(valExist, val) {
			t.Fatal("vals don't match")
		}
		if val.invalid {
			t.Fatal("entry shouldn't be invalid")
		}
	}

	// First half should be marked invalid.
	for _, val := range vals[:numEntries/2] {
		entry, exists := entryMap[val.mapKey()]
		if !exists {
			t.Fatal("entry doesn't exist")
		}
		if !entry.invalid {
			t.Fatal("entry should be invalid")
		}
	}
}

// TestRegistryRace is a multithreaded test to make sure the registry is not
// suffering from race conditions when updating and pruning several entries from
// multiple threads each.
func TestRegistryRace(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	dir := testDir(t.Name())

	// Create a new registry.
	registryPath := filepath.Join(dir, "registry")
	r, err := New(registryPath, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer func(c io.Closer) {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}(r)

	// Add 3 entries to it.
	numEntries := 3
	rvs := make([]modules.SignedRegistryValue, 0, numEntries)
	keys := make([]types.SiaPublicKey, 0, numEntries)
	skeys := make([]crypto.SecretKey, 0, numEntries)

	for i := 0; i < numEntries; i++ {
		rv, v, sk := randomValue(0)
		rv.Revision = 0 // set revision number to 0
		rv = rv.Sign(sk)
		_, err = r.Update(rv, v.key, 0)
		if err != nil {
			t.Fatal(err)
		}
		rvs = append(rvs, rv)
		keys = append(keys, v.key)
		skeys = append(skeys, sk)
	}

	// Atomically increment the revision and expiry with every update to make
	// sure they always work.
	var successes, iterations, prunes, prunedEntries uint64
	nextRevs := make([]uint64, numEntries)
	nextExps := make([]uint64, numEntries)

	// Declare worker thread.
	done := make(chan struct{})
	worker := func(key types.SiaPublicKey, sk crypto.SecretKey, rv modules.SignedRegistryValue, nextExpiry, nextRevision *uint64) {
		for {
			atomic.AddUint64(&iterations, 1)
			// Flip a coin. 'False' means update. 'True' means prune.
			// 10% chance to prune.
			op := fastrand.Intn(10) < 1

			// Prune nextExpiry.
			if op {
				atomic.AddUint64(&prunes, 1)
				n, err := r.Prune(types.BlockHeight(atomic.LoadUint64(nextExpiry)))
				if err != nil {
					t.Error(err)
					return
				}
				atomic.AddUint64(&prunedEntries, n)
				continue
			}

			// Update
			rev := atomic.AddUint64(nextRevision, 1)
			rv.Revision = rev
			exp := types.BlockHeight(atomic.AddUint64(nextExpiry, 1))
			rv = rv.Sign(sk)
			_, err := r.Update(rv, key, exp)
			if errors.Contains(err, errInvalidRevNum) {
				continue // invalid revision numbers are expected
			}
			if errors.Contains(err, errInvalidEntry) {
				continue // invalid entries are expected
			}
			if err != nil {
				t.Error(err)
				return
			}

			atomic.AddUint64(&successes, 1)

			// Check stop condition. We check here to make sure the last
			// operation was a successful update. That way we can later check
			// for numEntries valid entries in the registry.
			select {
			case <-done:
				return
			default:
			}
		}
	}

	// Spawn workers. Assign them the different entries.
	var wg sync.WaitGroup
	for i := 0; i < 5*numEntries; i++ {
		wg.Add(1)
		go func(i int) {
			worker(keys[i], skeys[i], rvs[i], &nextExps[i], &nextRevs[i])
			wg.Done()
		}(i % numEntries)
	}

	// Run for 10 seconds.
	time.Sleep(10 * time.Second)
	close(done)
	wg.Wait()

	// Log info.
	t.Logf("%v out of %v iterations successful", successes, iterations)
	t.Logf("%v pruned entries in %v prunes", prunedEntries, prunes)

	// Check that the entries have the latest revision numbers and expiries.
	for i := 0; i < numEntries; i++ {
		rv := rvs[i]
		key := keys[i]
		v, exists := r.entries[valueMapKey(key, rv.Tweak)]
		if !exists {
			t.Fatal("entry doesn't exist")
		}
		if v.expiry != types.BlockHeight(nextExps[i%numEntries]) {
			t.Fatal("wrong expiry")
		}
		if v.revision != nextExps[i%numEntries] {
			t.Fatal("wrong expiry")
		}
	}

	// Reload registry.
	r, err = New(registryPath, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer func(c io.Closer) {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}(r)

	// Check again.
	for i := 0; i < numEntries; i++ {
		rv := rvs[i]
		key := keys[i]
		v, exists := r.entries[valueMapKey(key, rv.Tweak)]
		if !exists {
			t.Fatal("entry doesn't exist")
		}
		if v.expiry != types.BlockHeight(nextExps[i%numEntries]) {
			t.Fatal("wrong expiry")
		}
		if v.revision != nextExps[i%numEntries] {
			t.Fatal("wrong expiry")
		}
	}
}

// BenchmarkRegistryUpdate is a benchmark for the Update method. It updates
// NumCPU entries from NumCPU goroutines in parallel.
//
// CPU | DiskType | #CPUs | #Updates/s | Commit
//
// i9  | SSD      | 16    | 196        | 1a862b7bace95e968f04f0a2151e5a572c948f22
//
func BenchmarkRegistryUpdate(b *testing.B) {
	b.StopTimer()
	dir := testDir(b.Name())

	// Create a new registry.
	registryPath := filepath.Join(dir, "registry")
	r, err := New(registryPath, 64)
	if err != nil {
		b.Fatal(err)
	}
	defer func(c io.Closer) {
		if err := c.Close(); err != nil {
			b.Fatal(err)
		}
	}(r)

	// Declare a number of entries to run. We try to mimic real world
	// application. That means each entry will be updated by a single thread
	// sequentially and have multiple threads read from it in parallel.
	nEntries := runtime.NumCPU()

	// Add entries.
	rvs := make([]modules.SignedRegistryValue, 0, nEntries)
	keys := make([]types.SiaPublicKey, 0, nEntries)
	skeys := make([]crypto.SecretKey, 0, nEntries)
	for i := 0; i < nEntries; i++ {
		rv, v, sk := randomValue(0)
		rv.Revision = 0 // set revision number to 0
		rvs = append(rvs, rv)
		keys = append(keys, v.key)
		skeys = append(skeys, sk)
	}

	// Declare writing thread.
	start := make(chan struct{})
	var iters uint64
	writer := func(i int) {
		// Grab vars.
		rv := rvs[i]
		key := keys[i]
		sk := skeys[i]
		var revision uint64
		var expiry types.BlockHeight

		// Wait for start signal.
		<-start
		for i := atomic.AddUint64(&iters, 1); i < uint64(b.N); i = atomic.AddUint64(&iters, 1) {
			// Update
			rv.Revision = revision
			_, err := r.Update(rv.Sign(sk), key, expiry)
			if err != nil {
				b.Error(err)
				return
			}
			revision++
		}
	}

	// Spawn workers. Assign them the different entries.
	var wg sync.WaitGroup
	for i := 0; i < nEntries; i++ {
		wg.Add(1)
		go func(i int) {
			writer(i)
			wg.Done()
		}(i % nEntries)
	}
	b.ResetTimer()
	b.StartTimer()
	close(start)
	wg.Wait()
}

// TestTruncate is a unit test for the registry's Truncate method.
func TestTruncate(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	dir := testDir(t.Name())

	// Create a new registry.
	registryPath := filepath.Join(dir, "registry")
	r, err := New(registryPath, 128)
	if err != nil {
		t.Fatal(err)
	}
	defer func(c io.Closer) {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}(r)

	// Capacity should be 128 and length 0.
	if r.Cap() != 128 || r.Len() != 0 {
		t.Fatal("wrong capacity/length for test", r.Cap(), r.Len())
	}

	// Add 64 entries to it.
	numEntries := 64
	entries := make([]modules.SignedRegistryValue, 0, numEntries)
	keys := make([]types.SiaPublicKey, 0, numEntries)
	for i := 0; i < numEntries; i++ {
		rv, v, sk := randomValue(0)
		rv.Revision = 0 // set revision number to 0
		rv = rv.Sign(sk)
		_, err = r.Update(rv, v.key, 0)
		if err != nil {
			t.Fatal(err)
		}
		rv, _ = r.Get(v.key, v.tweak)
		entries = append(entries, rv)
		keys = append(keys, v.key)
	}

	// Check capacity and length again.
	if r.Cap() != 128 || r.Len() != 64 {
		t.Fatal("wrong capacity/length for test", r.Cap(), r.Len())
	}

	// Truncate the registry to 63 entries. This shouldn't work.
	if err := r.Truncate(63); !errors.Contains(err, ErrInvalidTruncate) {
		t.Fatal(err)
	}

	// Truncate to 192. This should work.
	if err := r.Truncate(192); err != nil {
		t.Fatal(err)
	}

	// Check capacity and length again.
	if r.Cap() != 192 || r.Len() != 64 {
		t.Fatal("wrong capacity/length for test", r.Cap(), r.Len())
	}

	// Check file size.
	fi, err := r.staticFile.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() != 193*PersistedEntrySize {
		t.Fatal("wrong size", fi.Size(), 193*PersistedEntrySize)
	}

	// Entries should be the same as before.
	for i, entry := range entries {
		entryExist, exists := r.Get(keys[i], entry.Tweak)
		if !exists {
			t.Fatal("entry doesn't exist")
		}
		if !reflect.DeepEqual(entry, entryExist) {
			t.Log(entry)
			t.Log(entryExist)
			t.Fatal("entries don't match")
		}
	}

	// Reload registry.
	r, err = New(registryPath, 192)
	if err != nil {
		t.Fatal(err)
	}
	defer func(c io.Closer) {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}(r)

	// Check capacity and length again.
	if r.Cap() != 192 || r.Len() != 64 {
		t.Fatal("wrong capacity/length for test", r.Cap(), r.Len())
	}

	// Entries should be the same as before.
	for i, entry := range entries {
		entryExist, exists := r.Get(keys[i], entry.Tweak)
		if !exists {
			t.Fatal("entry doesn't exist")
		}
		if !reflect.DeepEqual(entry, entryExist) {
			t.Log(entry)
			t.Log(entryExist)
			t.Fatal("entries don't match")
		}
	}

	// Truncate to 64. This should work.
	if err := r.Truncate(64); err != nil {
		t.Fatal(err)
	}

	// Check capacity and length again.
	if r.Cap() != 64 || r.Len() != 64 {
		t.Fatal("wrong capacity/length for test", r.Cap(), r.Len())
	}

	// Check file size.
	fi, err = r.staticFile.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() != 65*PersistedEntrySize {
		t.Fatal("wrong size", fi.Size(), 65*PersistedEntrySize)
	}

	// Entries should be the same as before.
	for i, entry := range entries {
		entryExist, exists := r.Get(keys[i], entry.Tweak)
		if !exists {
			t.Fatal("entry doesn't exist")
		}
		if !reflect.DeepEqual(entry, entryExist) {
			t.Log(entry)
			t.Log(entryExist)
			t.Fatal("entries don't match")
		}
	}

	// Reload registry.
	r, err = New(registryPath, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer func(c io.Closer) {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}(r)

	// Check capacity and length again.
	if r.Cap() != 64 || r.Len() != 64 {
		t.Fatal("wrong capacity/length for test", r.Cap(), r.Len())
	}

	// Entries should be the same as before.
	for i, entry := range entries {
		entryExist, exists := r.Get(keys[i], entry.Tweak)
		if !exists {
			t.Fatal("entry doesn't exist")
		}
		if !reflect.DeepEqual(entry, entryExist) {
			t.Log(entry)
			t.Log(entryExist)
			t.Fatal("entries don't match")
		}
	}
}

// TestMigrate is a unit test for the registry's Migrate method.
func TestMigrate(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	dir := testDir(t.Name())

	// Prepare a source and destination path for the registry.
	registryPathSrc := filepath.Join(dir, "registrySrc")
	registryPathDst := filepath.Join(dir, "registryDst")

	// Create a new registry.
	r, err := New(registryPathSrc, 128)
	if err != nil {
		t.Fatal(err)
	}

	// Add 64 entries to it.
	numEntries := 64
	entries := make([]modules.SignedRegistryValue, 0, numEntries)
	keys := make([]types.SiaPublicKey, 0, numEntries)
	for i := 0; i < numEntries; i++ {
		rv, v, sk := randomValue(0)
		rv.Revision = 0 // set revision number to 0
		rv = rv.Sign(sk)
		_, err = r.Update(rv, v.key, 0)
		if err != nil {
			t.Fatal(err)
		}
		rv, _ = r.Get(v.key, v.tweak)
		entries = append(entries, rv)
		keys = append(keys, v.key)
	}

	// Check capacity and length.
	if r.Cap() != 128 || r.Len() != 64 {
		t.Fatal("wrong capacity/length for test", r.Cap(), r.Len())
	}

	// Migrate the registry.
	err = r.Migrate(registryPathDst)
	if err != nil {
		t.Fatal(err)
	}

	// Make sure the old file is gone.
	if _, err := os.Stat(registryPathSrc); !os.IsNotExist(err) {
		t.Fatal(err)
	}

	// Close registry
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	// Reload the registry.
	r, err = New(registryPathDst, 128)
	if err != nil {
		t.Fatal(err)
	}
	defer func(c io.Closer) {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}(r)

	// Check capacity and length.
	if r.Cap() != 128 || r.Len() != 64 {
		t.Fatal("wrong capacity/length for test", r.Cap(), r.Len())
	}

	// Entries should be the same as before.
	for i, entry := range entries {
		entryExist, exists := r.Get(keys[i], entry.Tweak)
		if !exists {
			t.Fatal("entry doesn't exist")
		}
		if !reflect.DeepEqual(entry, entryExist) {
			t.Log(entry)
			t.Log(entryExist)
			t.Fatal("entries don't match")
		}
	}

	// Try to migrate a registry to a relative path. This shouldn't work.
	err = r.Migrate("./registry.dat")
	if !errors.Contains(err, errPathNotAbsolute) {
		t.Fatal(err)
	}

	// Try to migrate a registry to its own path. This shouldn't work.
	err = r.Migrate(registryPathDst)
	if !errors.Contains(err, errSamePath) {
		t.Fatal(err)
	}
}
