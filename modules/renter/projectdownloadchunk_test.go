package renter

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"strings"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/persist"
	"go.sia.tech/siad/types"
)

// TestProjectDownloadChunk_finalize is a unit test for the 'finalize' function
// on the pdc. It verifies whether the returned data is properly offset to
// include only the pieces requested by the user.
func TestProjectDownloadChunk_finalize(t *testing.T) {
	t.Parallel()

	// create data
	originalData := fastrand.Bytes(int(modules.SectorSize))
	sectorRoot := crypto.MerkleRoot(originalData)

	// create an EC and a passhtrough cipher key
	ec := modules.NewRSSubCodeDefault()
	ck, err := crypto.NewSiaKey(crypto.TypePlain, nil)
	if err != nil {
		t.Fatal(err)
	}

	// RS encode the data
	data := make([]byte, modules.SectorSize)
	copy(data, originalData)
	pieces, err := ec.Encode(data)
	if err != nil {
		t.Fatal(err)
	}

	// create PCWS manually
	pcws := &projectChunkWorkerSet{
		staticChunkIndex:   0,
		staticErasureCoder: ec,
		staticMasterKey:    ck,
		staticPieceRoots:   []crypto.Hash{sectorRoot},

		staticCtx:    context.Background(),
		staticRenter: new(Renter),
	}

	// download a random amount of data at random offset
	length := (fastrand.Uint64n(5) + 1) * crypto.SegmentSize
	offset := fastrand.Uint64n(modules.SectorSize - length)
	pieceOffset, pieceLength := getPieceOffsetAndLen(ec, offset, length)

	sliced := make([][]byte, len(pieces))
	for i, piece := range pieces {
		sliced[i] = make([]byte, pieceLength)
		copy(sliced[i], piece[pieceOffset:pieceOffset+pieceLength])
	}

	// create PDC manually
	responseChan := make(chan *downloadResponse, 1)
	pdc := &projectDownloadChunk{
		offsetInChunk: offset,
		lengthInChunk: length,

		pieceOffset: pieceOffset,
		pieceLength: pieceLength,

		dataPieces: sliced,

		downloadResponseChan: responseChan,
		workerSet:            pcws,
	}

	pdc.launchedWorkers = append(pdc.launchedWorkers, &launchedWorkerInfo{
		launchTime:           time.Now(),
		expectedCompleteTime: time.Now().Add(time.Minute),
		expectedDuration:     time.Minute,

		pdc:    pdc,
		worker: new(worker),
	})

	// call finalize
	pdc.finalize()

	// verify the download
	downloadResponse := <-responseChan
	if downloadResponse.err != nil {
		t.Fatal("unexpected error", downloadResponse.err)
	}
	if !bytes.Equal(downloadResponse.data, originalData[offset:offset+length]) {
		t.Log("offset", offset)
		t.Log("length", length)
		t.Log("bytes downloaded", len(downloadResponse.data))

		t.Log("actual:\n", downloadResponse.data)
		t.Log("expected:\n", originalData[offset:offset+length])
		t.Fatal("unexpected data")
	}
	if downloadResponse.launchedWorkers == nil || len(downloadResponse.launchedWorkers) != 1 || downloadResponse.launchedWorkers[0].expectedDuration != time.Minute {
		t.Fatal("unexpected")
	}

	// call fail
	pdc.fail(errors.New("failure"))
	downloadResponse = <-responseChan
	if downloadResponse.err == nil {
		t.Fatal("unexpected error")
	}
	if downloadResponse.launchedWorkers == nil {
		t.Fatal("unexpected")
	}
}

// TestProjectDownloadChunk_finished is a unit test for the 'finished' function
// on the pdc. It verifies whether the hopeful and completed pieces are properly
// counted and whether the return values are correct.
func TestProjectDownloadChunk_finished(t *testing.T) {
	// create an EC
	ec, err := modules.NewRSCode(3, 9)
	if err != nil {
		t.Fatal(err)
	}

	// create a passhtrough cipher key
	ck, err := crypto.NewSiaKey(crypto.TypePlain, nil)
	if err != nil {
		t.Fatal(err)
	}

	// create PCWS manually
	pcws := &projectChunkWorkerSet{
		staticChunkIndex:   0,
		staticErasureCoder: ec,
		staticMasterKey:    ck,
		staticPieceRoots:   []crypto.Hash{},

		staticCtx:    context.Background(),
		staticRenter: new(Renter),
	}

	// create PDC manually - only the essentials
	pdc := &projectDownloadChunk{workerSet: pcws}

	// mock unresolved state with hope of successful download
	pdc.availablePieces = make([][]*pieceDownload, 0)
	pdc.unresolvedWorkersRemaining = 4
	finished, err := pdc.finished()
	if err != nil {
		t.Fatal("unexpected error", err)
	}
	if finished {
		t.Fatal("unexpected")
	}

	// mock one completed piece - still unresolved and hopeful
	pdc.unresolvedWorkersRemaining = 3
	pdc.availablePieces = append(pdc.availablePieces, []*pieceDownload{{completed: true}})
	finished, err = pdc.finished()
	if err != nil {
		t.Fatal("unexpected error", err)
	}
	if finished {
		t.Fatal("unexpected")
	}

	// mock resolved state - not hopeful and not finished
	pdc.unresolvedWorkersRemaining = 0
	finished, err = pdc.finished()
	if err != errNotEnoughPieces {
		t.Fatal("unexpected error", err)
	}
	if finished {
		t.Fatal("unexpected")
	}

	// mock resolves state - add 3 pieces in limbo -> hopeful again
	pdc.availablePieces = append(pdc.availablePieces, []*pieceDownload{{}})
	pdc.availablePieces = append(pdc.availablePieces, []*pieceDownload{{}})
	pdc.availablePieces = append(pdc.availablePieces, []*pieceDownload{{}})
	finished, err = pdc.finished()
	if err != nil {
		t.Fatal("unexpected error", err)
	}
	if finished {
		t.Fatal("unexpected")
	}

	// mock two failures -> hope gone again
	pdc.availablePieces[1][0].completed = true
	pdc.availablePieces[1][0].downloadErr = errors.New("failed")
	pdc.availablePieces[2][0].completed = true
	pdc.availablePieces[2][0].downloadErr = errors.New("failed")
	finished, err = pdc.finished()
	if err != errNotEnoughPieces {
		t.Fatal("unexpected error", err)
	}
	if finished {
		t.Fatal("unexpected")
	}

	// undo one failure and add 2 completed -> finished
	pdc.availablePieces[2][0].downloadErr = nil
	pdc.availablePieces[2][0].completed = true
	pdc.availablePieces[3][0].completed = true
	finished, err = pdc.finished()
	if err != nil {
		t.Fatal("unexpected error", err)
	}
	if !finished {
		t.Fatal("unexpected")
	}
}

// TestProjectDownloadChunk_handleJobResponse is a unit test that verifies the
// functionality of the 'handleJobResponse' function on the ProjectDownloadChunk
func TestProjectDownloadChunk_handleJobResponse(t *testing.T) {
	t.Parallel()

	ec := modules.NewRSSubCodeDefault()
	ptck, err := crypto.NewSiaKey(crypto.TypePlain, nil)
	if err != nil {
		t.Fatal(err)
	}

	data := fastrand.Bytes(int(modules.SectorSize))
	pieces, err := ec.Encode(data)
	if err != nil {
		t.Fatal(err)
	}

	w := new(worker)
	w.staticHostPubKeyStr = "w"

	empty := crypto.Hash{}
	pcws := new(projectChunkWorkerSet)
	pcws.staticMasterKey = ptck
	pcws.staticErasureCoder = ec
	pcws.staticPieceRoots = []crypto.Hash{
		empty,
		empty,
		empty,
		crypto.MerkleRoot(pieces[3]),
		empty,
	}

	renter := new(Renter)
	logger, err := persist.NewLogger(ioutil.Discard)
	if err != nil {
		t.Fatal("unexpected")
	}
	renter.log = logger
	pcws.staticRenter = renter

	pdc := new(projectDownloadChunk)
	pdc.workerSet = pcws
	pdc.workerSet.staticChunkIndex = 0
	pdc.dataPieces = make([][]byte, ec.NumPieces())
	pdc.availablePieces = [][]*pieceDownload{
		{{launched: true, worker: w}},
		{{launched: true, worker: w}},
		{{launched: true, worker: w}},
		{{launched: true, worker: w}},
		{{launched: true, worker: w}},
	}

	lwi := launchedWorkerInfo{
		launchTime: time.Now().Add(-time.Minute),
	}
	pdc.launchedWorkers = []*launchedWorkerInfo{&lwi}

	// verify the pdc after a successful read response for piece at index 3
	success := &jobReadResponse{
		staticData:    pieces[3],
		staticErr:     nil,
		staticJobTime: time.Duration(1),
		staticMetadata: jobReadMetadata{
			staticLaunchedWorkerIndex: 0,
			staticPieceRootIndex:      3,
			staticSectorRoot:          crypto.MerkleRoot(pieces[3]),
			staticWorker:              w,
		},
	}
	pdc.handleJobReadResponse(success)
	if !pdc.availablePieces[3][0].completed {
		t.Fatal("unexpected")
	}
	if pdc.availablePieces[3][0].downloadErr != nil {
		t.Fatal("unexpected")
	}
	if !bytes.Equal(pdc.dataPieces[3], pieces[3]) {
		t.Fatal("unexpected")
	}
	if success.staticData != nil {
		t.Fatal("unexpected") // verify we unset the data
	}

	// verify the worker information got updated
	if lwi.completeTime == (time.Time{}) ||
		lwi.jobDuration == 0 ||
		lwi.totalDuration == 0 ||
		lwi.jobErr != nil {
		t.Fatal("unexpected")
	}

	// verify the pdc after a failed read
	pdc.handleJobReadResponse(&jobReadResponse{
		staticData:    nil,
		staticErr:     errors.New("read failed"),
		staticJobTime: time.Duration(1),
		staticMetadata: jobReadMetadata{
			staticPieceRootIndex: 0,
			staticSectorRoot:     empty,
			staticWorker:         w,
		},
	})
	if !pdc.availablePieces[0][0].completed {
		t.Fatal("unexpected")
	}
	if pdc.availablePieces[0][0].downloadErr == nil {
		t.Fatal("unexpected")
	}
	if pdc.dataPieces[0] != nil {
		t.Fatal("unexpected")
	}

	// verify the worker information got updated
	if lwi.completeTime == (time.Time{}) ||
		lwi.jobDuration == 0 ||
		lwi.totalDuration == 0 ||
		lwi.jobErr == nil {
		t.Fatal("unexpected", lwi)
	}

	// rig the availablepieces in a way that it has a duplicate piece, we added
	// a build.Critical to guard against this developer error that we want to
	// test
	pdc.availablePieces[3] = append(
		pdc.availablePieces[3],
		&pieceDownload{launched: true, worker: w},
	)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Expected build.Critical", r)
		}
	}()
	pdc.handleJobReadResponse(success)
}

// TestProjectDownloadChunk_launchWorker is a unit test for the 'launchWorker'
// function on the pdc.
func TestProjectDownloadChunk_launchWorker(t *testing.T) {
	t.Parallel()

	ec := modules.NewRSCodeDefault()
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       fastrand.Bytes(crypto.PublicKeySize),
	}

	// mock a worker, ensure the readqueue returns a non zero time estimate
	worker := mockWorker(100 * time.Millisecond)
	worker.staticHostPubKeyStr = spk.String()

	// mock a pcws
	pcws := new(projectChunkWorkerSet)
	pcws.staticPieceRoots = make([]crypto.Hash, ec.NumPieces())

	// mock a pdc, ensure available pieces is not nil
	pdc := new(projectDownloadChunk)
	pdc.workerSet = pcws
	pdc.pieceLength = 1 << 16 // 64kb
	pdc.availablePieces = make([][]*pieceDownload, ec.NumPieces())
	for pieceIndex := range pdc.availablePieces {
		pdc.availablePieces[pieceIndex] = append(pdc.availablePieces[pieceIndex], &pieceDownload{
			worker: worker,
		})
	}

	// launch a worker and expect it to have enqueued a job and expect the
	// complete time to be somewhere in the future
	expectedCompleteTime, added := pdc.launchWorker(worker, 0, false)
	if !added {
		t.Fatal("unexpected")
	}
	if expectedCompleteTime.Before(time.Now()) {
		t.Fatal("unexpected")
	}

	// mention of the launched worker should be present in the PDC's launched
	// worker map, which holds debug information about all workers that were
	// launched.
	if len(pdc.launchedWorkers) != 1 {
		t.Fatal("unexpected")
	}
	lw := pdc.launchedWorkers[0]

	// assert the launched worker info contains what we expect it to contain
	if lw.launchTime == (time.Time{}) ||
		lw.completeTime != (time.Time{}) ||
		lw.expectedCompleteTime == (time.Time{}) ||
		lw.jobDuration != 0 ||
		lw.totalDuration != 0 ||
		lw.expectedDuration == 0 ||
		!bytes.Equal(lw.pdc.uid[:], pdc.uid[:]) ||
		lw.worker.staticHostPubKeyStr != spk.String() {
		t.Fatal("unexpected")
	}

	// verify one worker was launched without failure
	numLWF := 0 // launchedWithoutFail
	for _, pieces := range pdc.availablePieces {
		launchedWithoutFail := false
		for _, pieceDownload := range pieces {
			if pieceDownload.launched && pieceDownload.downloadErr == nil {
				launchedWithoutFail = true
			}
		}
		if launchedWithoutFail {
			numLWF++
		}
	}
	if numLWF != 1 {
		t.Fatal("unexpected", numLWF)
	}

	// launch the worker again but kill the queue, expect it to have not added
	// the job to the queue and updated the pieceDownload's status to failed
	worker.staticJobReadQueue.killed = true
	_, added = pdc.launchWorker(worker, 0, false)
	if added {
		t.Fatal("unexpected")
	}
	numFailed := 0
	for _, pieces := range pdc.availablePieces {
		for _, pieceDownload := range pieces {
			if pieceDownload.downloadErr != nil {
				numFailed++
			}
		}
	}
	if numFailed != 1 {
		t.Fatal("unexpected", numFailed)
	}
}

// TestGetPieceOffsetAndLen is a unit test that probes the helper function
// getPieceOffsetAndLength
func TestGetPieceOffsetAndLen(t *testing.T) {
	randOff := fastrand.Uint64n(modules.SectorSize)
	randLen := fastrand.Uint64n(modules.SectorSize)

	// verify an EC that does not support partials, defaults to a segemnt size
	// that is equal to the sectorsize
	ec := modules.NewRSCodeDefault()
	pieceOff, pieceLen := getPieceOffsetAndLen(ec, randOff, randLen)
	if pieceOff != 0 || pieceLen%modules.SectorSize != 0 {
		t.Fatal("unexpected", pieceOff, pieceLen)
	}

	// verify an EC that does support partials using the appropriate segment
	// size and the offset are as we expect them to be
	ec = modules.NewRSSubCodeDefault()
	pieceOff, pieceLen = getPieceOffsetAndLen(ec, randOff, randLen)
	if pieceOff%crypto.SegmentSize != 0 || pieceLen%crypto.SegmentSize != 0 {
		t.Fatal("unexpected", pieceOff, pieceLen)
	}

	// verify an EC with minPieces different from 1 that supports partials
	// encoding ensures we are reading enough data
	dataPieces := 2
	segmentSize := crypto.SegmentSize
	chunkSegmentSize := uint64(dataPieces * segmentSize)
	ec, err := modules.NewRSSubCode(2, 5, uint64(segmentSize))
	if err != nil {
		t.Fatal(err)
	}
	pieceOff, pieceLen = getPieceOffsetAndLen(ec, randOff, randLen)
	if ((pieceOff+pieceLen)*uint64(ec.MinPieces()))%chunkSegmentSize != 0 {
		t.Fatal("unexpected", pieceOff, pieceLen)
	}

	// verify an EC that returns a segment size of 0 is considered invalid
	ec = &mockErasureCoder{}
	defer func() {
		if r := recover(); r == nil || !strings.Contains(fmt.Sprintf("%v", r), "pcws has a bad erasure coder") {
			t.Fatal("Expected build.Critical", r)
		}
	}()
	getPieceOffsetAndLen(ec, 0, 0)
}

// TestGetPieceOffsetAndLenWithRecover is a unit test that isolates both
// 'getPieceOffsetAndLen' in combination with the Recover function on the EC and
// asserts we can properly encode and then recover at random offset and length
func TestGetPieceOffsetAndLenWithRecover(t *testing.T) {
	t.Parallel()

	// create data
	cntr := 0
	originalData := make([]byte, modules.SectorSize)
	for i := 0; i < int(modules.SectorSize); i += 2 {
		binary.BigEndian.PutUint16(originalData[i:], uint16(cntr))
		cntr += 1
	}

	// RS encode the data
	data := make([]byte, modules.SectorSize)
	copy(data, originalData)
	ec := modules.NewRSSubCodeDefault()
	pieces, err := ec.Encode(data)
	if err != nil {
		t.Fatal(err)
	}

	// Declare helper for testing.
	run := func(offset, length uint64) {
		pieceOffset, pieceLength := getPieceOffsetAndLen(ec, offset, length)
		skipLength := offset % (crypto.SegmentSize * uint64(ec.MinPieces()))

		sliced := make([][]byte, len(pieces))
		for i, piece := range pieces {
			sliced[i] = make([]byte, pieceLength)
			copy(sliced[i], piece[pieceOffset:pieceOffset+pieceLength])
		}

		buf := bytes.NewBuffer(nil)
		skipWriter := &skipWriter{
			writer: buf,
			skip:   int(skipLength),
		}
		err = ec.Recover(sliced, length+uint64(skipLength), skipWriter)
		if err != nil {
			t.Fatal(err)
		}
		actual := buf.Bytes()

		expected := originalData[offset : offset+length]
		if !bytes.Equal(actual, expected) {
			t.Log("Input       :", offset, length, pieceOffset, pieceLength)
			t.Log("original    :", originalData[:crypto.SegmentSize*8])
			t.Log("expected    :", expected)
			t.Log("expected len:", len(expected))
			t.Log("actual      :", actual)
			t.Log("actual   len:", len(actual))
			t.Fatal("unexpected")
		}
	}

	// Test some cases manually.
	run(0, crypto.SegmentSize)
	run(crypto.SegmentSize, crypto.SegmentSize)
	run(2*crypto.SegmentSize, crypto.SegmentSize)
	run(crypto.SegmentSize, 2*crypto.SegmentSize)
	run(1, crypto.SegmentSize)
	run(0, crypto.SegmentSize-1)
	run(0, crypto.SegmentSize+1)
	run(crypto.SegmentSize-1, crypto.SegmentSize+1)

	// Test random inputs.
	for rounds := 0; rounds < 100; rounds++ {
		// random length and offset
		length := (fastrand.Uint64n(5*crypto.SegmentSize) + 1)
		offset := fastrand.Uint64n(modules.SectorSize - length)
		run(offset, length)
	}
}

// mockWorker is a helper function that returns a worker with a pricetable
// and an initialised read queue that returns a non zero value for read
// estimates depending on the given jobTime value.
func mockWorker(jobTime time.Duration) *worker {
	worker := new(worker)
	worker.newPriceTable()
	worker.staticPriceTable().staticPriceTable = newDefaultPriceTable()
	worker.initJobReadQueue()
	worker.staticJobReadQueue.weightedJobTime64k = float64(jobTime)
	return worker
}

// mockErasureCoder implements the erasure coder interface, but is an invalid
// erasure coder that returns a 0 segmentsize. It is used to test the critical
// that is thrown when an invalid EC is passed to 'getPieceOffsetAndLen'
type mockErasureCoder struct{}

func (mec *mockErasureCoder) NumPieces() int                       { return 10 }
func (mec *mockErasureCoder) MinPieces() int                       { return 1 }
func (mec *mockErasureCoder) Encode(data []byte) ([][]byte, error) { return nil, nil }
func (mec *mockErasureCoder) Identifier() modules.ErasureCoderIdentifier {
	return modules.ErasureCoderIdentifier("mock")
}
func (mec *mockErasureCoder) EncodeShards(data [][]byte) ([][]byte, error)         { return nil, nil }
func (mec *mockErasureCoder) Reconstruct(pieces [][]byte) error                    { return nil }
func (mec *mockErasureCoder) Recover(pieces [][]byte, n uint64, w io.Writer) error { return nil }
func (mec *mockErasureCoder) SupportsPartialEncoding() (uint64, bool)              { return 0, true }
func (mec *mockErasureCoder) Type() modules.ErasureCoderType {
	return modules.ErasureCoderType{9, 9, 9, 9}
}
