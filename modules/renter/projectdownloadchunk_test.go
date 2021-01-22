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

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestProjectDownloadChunk_finalize is a unit test for the 'finalize' function
// on the pdc. It verifies whether the returned data is properly offset to
// include only the pieces requested by the user.
func TestProjectDownloadChunk_finalize(t *testing.T) {
	t.Parallel()

	size := 512
	// create a random sector
	// sectorData := fastrand.Bytes(int(modules.SectorSize))
	sectorData := make([]byte, size)
	cntr := 0
	for i := 0; i < int(size); i += 2 {
		fmt.Println("adding ", uint64(cntr))
		binary.LittleEndian.PutUint64(sectorData[i:], uint64(cntr))
		cntr += 1
	}
	sectorRoot := crypto.MerkleRoot(sectorData)

	// create an EC and a passhtrough cipher key
	ec := modules.NewRSSubCodeDefault()
	ck, err := crypto.NewSiaKey(crypto.TypePlain, nil)
	if err != nil {
		t.Fatal(err)
	}

	// RS encode the data
	pieces, err := ec.Encode(sectorData)
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
	offset = 128
	length = 192
	pieceOffset, pieceLength := getPieceOffsetAndLen(ec, offset, length)

	fmt.Println("piece offset", pieceOffset)
	fmt.Println("piece length", pieceLength)

	// create PDC manually
	responseChan := make(chan *downloadResponse, 1)
	pdc := &projectDownloadChunk{
		offsetInChunk: offset,
		lengthInChunk: length,

		pieceOffset: pieceOffset,
		pieceLength: pieceLength,

		dataPieces: pieces,

		downloadResponseChan: responseChan,
		workerSet:            pcws,
	}

	// call finalize
	pdc.finalize()

	// verify the download
	downloadResponse := <-responseChan
	if downloadResponse.err != nil {
		t.Fatal("unexpected error", downloadResponse.err)
	}
	if !bytes.Equal(downloadResponse.data, sectorData[offset:offset+length]) {
		t.Log("offset", offset)
		t.Log("length", length)
		t.Log("bytes downloaded", len(downloadResponse.data))

		t.Log("expected:\n", downloadResponse.data)
		t.Log("actual:\n", sectorData[offset:offset+length])
		t.Fatal("unexpected data")
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

	// verify the pdc after a successful read response for piece at index 3
	success := &jobReadResponse{
		staticData:       pieces[3],
		staticErr:        nil,
		staticSectorRoot: crypto.MerkleRoot(pieces[3]),
		staticWorker:     w,
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

	// verify the pdc after a failed read
	pdc.handleJobReadResponse(&jobReadResponse{
		staticData:       nil,
		staticErr:        errors.New("read failed"),
		staticSectorRoot: empty, // it'll see this as piece index 0
		staticWorker:     w,
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
	worker := mockWorker(10)
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
	expectedCompleteTime, added := pdc.launchWorker(worker, 0)
	if !added {
		t.Fatal("unexpected")
	}
	if expectedCompleteTime.Before(time.Now()) {
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
	_, added = pdc.launchWorker(worker, 0)
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

// mockWorker is a helper function that returns a worker with a pricetable
// and an initialised read queue that returns a non zero value for read
// estimates depending on the given jobsCompleted value.
//
// for example, passing in 10 yields 100ms expected read time for 64kb jobs only
// as the weightedJobTime64k is set to 1s, passing in 5 yields 200ms.
func mockWorker(jobsCompleted float64) *worker {
	worker := new(worker)
	worker.newPriceTable()
	worker.staticPriceTable().staticPriceTable = newDefaultPriceTable()
	worker.initJobReadQueue()
	worker.staticJobReadQueue.weightedJobTime64k = float64(time.Second)
	worker.staticJobReadQueue.weightedJobsCompleted64k = jobsCompleted
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
