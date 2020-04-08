package host

import (
	"fmt"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/siamux"
)

// TestVerifyPaymentRevision is a unit test covering verifyPaymentRevision
func TestVerifyPaymentRevision(t *testing.T) {
	t.Parallel()

	// create a current revision and a payment revision
	height := types.BlockHeight(0)
	amount := types.NewCurrency64(1)
	curr := types.FileContractRevision{
		NewValidProofOutputs: []types.SiacoinOutput{
			{Value: types.NewCurrency64(10)},
			{Value: types.NewCurrency64(1)},
		},
		NewMissedProofOutputs: []types.SiacoinOutput{
			{Value: types.NewCurrency64(10)},
			{Value: types.NewCurrency64(1)},
			{Value: types.ZeroCurrency},
		},
		NewWindowStart: types.BlockHeight(revisionSubmissionBuffer) + 1,
	}
	payment, err := curr.PaymentRevision(amount)
	if err != nil {
		t.Fatal(err)
	}

	// verify a properly created payment revision is accepted
	err = verifyPaymentRevision(curr, payment, height, amount)
	if err != nil {
		t.Fatal("Unexpected error when verifying revision, ", err)
	}

	// deepCopy is a helper function that makes a deep copy of a revision
	deepCopy := func(rev types.FileContractRevision) (revCopy types.FileContractRevision) {
		rBytes := encoding.Marshal(rev)
		err := encoding.Unmarshal(rBytes, &revCopy)
		if err != nil {
			panic(err)
		}
		return
	}

	// expect errBadContractOutputCounts
	badOutputs := []types.SiacoinOutput{payment.NewMissedProofOutputs[0]}
	badPayment := deepCopy(payment)
	badPayment.NewMissedProofOutputs = badOutputs
	err = verifyPaymentRevision(curr, badPayment, height, amount)
	if err != errBadContractOutputCounts {
		t.Fatalf("Expected errBadContractOutputCounts but received '%v'", err)
	}

	// expect errLateRevision
	badCurr := deepCopy(curr)
	badCurr.NewWindowStart = curr.NewWindowStart - 1
	err = verifyPaymentRevision(badCurr, payment, height, amount)
	if err != errLateRevision {
		t.Fatalf("Expected errLateRevision but received '%v'", err)
	}

	// expect host payout address changed
	hash := crypto.HashBytes([]byte("random"))
	badCurr = deepCopy(curr)
	badCurr.NewValidProofOutputs[1].UnlockHash = types.UnlockHash(hash)
	err = verifyPaymentRevision(badCurr, payment, height, amount)
	if err == nil || !strings.Contains(err.Error(), "host payout address changed") {
		t.Fatalf("Expected host payout error but received '%v'", err)
	}

	// expect host payout address changed
	badCurr = deepCopy(curr)
	badCurr.NewMissedProofOutputs[1].UnlockHash = types.UnlockHash(hash)
	err = verifyPaymentRevision(badCurr, payment, height, amount)
	if err == nil || !strings.Contains(err.Error(), "host payout address changed") {
		t.Fatalf("Expected host payout error but received '%v'", err)
	}

	// expect missed void output
	badCurr = deepCopy(curr)
	badCurr.NewMissedProofOutputs = append([]types.SiacoinOutput{}, curr.NewMissedProofOutputs[:2]...)
	err = verifyPaymentRevision(badCurr, payment, height, amount)
	if !errors.Contains(err, types.ErrMissingVoidOutput) {
		t.Fatalf("Expected '%v' but received '%v'", types.ErrMissingVoidOutput, err)
	}

	// expect lost collateral address changed
	badPayment = deepCopy(payment)
	badPayment.NewMissedProofOutputs[2].UnlockHash = types.UnlockHash(hash)
	err = verifyPaymentRevision(curr, badPayment, height, amount)
	if err == nil || !strings.Contains(err.Error(), "lost collateral address was changed") {
		t.Fatalf("Expected lost collaterall error but received '%v'", err)
	}

	// expect renter increased its proof output
	badPayment = deepCopy(payment)
	badPayment.SetValidRenterPayout(curr.ValidRenterPayout().Add64(1))
	err = verifyPaymentRevision(curr, badPayment, height, amount)
	if err == nil || !strings.Contains(err.Error(), string(errHighRenterValidOutput)) {
		t.Fatalf("Expected '%v' but received '%v'", string(errHighRenterValidOutput), err)
	}

	// expect an error saying not enough money was transferred
	err = verifyPaymentRevision(curr, payment, height, amount.Add64(1))
	if err == nil || !strings.Contains(err.Error(), string(errHighRenterValidOutput)) {
		t.Fatalf("Expected '%v' but received '%v'", string(errHighRenterValidOutput), err)
	}
	expectedErrorMsg := fmt.Sprintf("expected at least %v to be exchanged, but %v was exchanged: ", amount.Add64(1), curr.ValidRenterPayout().Sub(payment.ValidRenterPayout()))
	if err == nil || !strings.Contains(err.Error(), expectedErrorMsg) {
		t.Fatalf("Expected '%v' but received '%v'", expectedErrorMsg, err)
	}

	// expect errLowHostValidOutput
	badPayment = deepCopy(payment)
	badPayment.SetValidHostPayout(curr.ValidHostPayout().Sub64(1))
	err = verifyPaymentRevision(curr, badPayment, height, amount)
	if err == nil || !strings.Contains(err.Error(), string(errLowHostValidOutput)) {
		t.Fatalf("Expected '%v' but received '%v'", string(errLowHostValidOutput), err)
	}

	// expect errLowHostValidOutput
	badCurr = deepCopy(curr)
	badCurr.SetValidHostPayout(curr.ValidHostPayout().Sub64(1))
	err = verifyPaymentRevision(badCurr, payment, height, amount)
	if err == nil || !strings.Contains(err.Error(), string(errLowHostValidOutput)) {
		t.Fatalf("Expected '%v' but received '%v'", string(errLowHostValidOutput), err)
	}

	// expect errHighRenterMissedOutput
	badPayment = deepCopy(payment)
	badPayment.SetMissedRenterPayout(payment.MissedRenterOutput().Value.Sub64(1))
	err = verifyPaymentRevision(curr, badPayment, height, amount)
	if err == nil || !strings.Contains(err.Error(), string(errHighRenterMissedOutput)) {
		t.Fatalf("Expected '%v' but received '%v'", string(errHighRenterMissedOutput), err)
	}

	// expect errLowHostMissedOutput
	badCurr = deepCopy(curr)
	currOut := curr.MissedHostOutput()
	currOut.Value = currOut.Value.Add64(1)
	badCurr.NewMissedProofOutputs[1] = currOut
	err = verifyPaymentRevision(badCurr, payment, height, amount)
	if err == nil || !strings.Contains(err.Error(), string(errLowHostMissedOutput)) {
		t.Fatalf("Expected '%v' but received '%v'", string(errLowHostMissedOutput), err)
	}

	// expect errBadRevisionNumber
	badPayment = deepCopy(payment)
	badPayment.NewRevisionNumber -= 1
	err = verifyPaymentRevision(curr, badPayment, height, amount)
	if err != errBadRevisionNumber {
		t.Fatalf("Expected errBadRevisionNumber but received '%v'", err)
	}

	// expect errBadParentID
	badPayment = deepCopy(payment)
	badPayment.ParentID = types.FileContractID(hash)
	err = verifyPaymentRevision(curr, badPayment, height, amount)
	if err != errBadParentID {
		t.Fatalf("Expected errBadParentID but received '%v'", err)
	}

	// expect errBadUnlockConditions
	badPayment = deepCopy(payment)
	badPayment.UnlockConditions.Timelock = payment.UnlockConditions.Timelock + 1
	err = verifyPaymentRevision(curr, badPayment, height, amount)
	if err != errBadUnlockConditions {
		t.Fatalf("Expected errBadUnlockConditions but received '%v'", err)
	}

	// expect errBadFileSize
	badPayment = deepCopy(payment)
	badPayment.NewFileSize = payment.NewFileSize + 1
	err = verifyPaymentRevision(curr, badPayment, height, amount)
	if err != errBadFileSize {
		t.Fatalf("Expected errBadFileSize but received '%v'", err)
	}

	// expect errBadFileMerkleRoot
	badPayment = deepCopy(payment)
	badPayment.NewFileMerkleRoot = hash
	err = verifyPaymentRevision(curr, badPayment, height, amount)
	if err != errBadFileMerkleRoot {
		t.Fatalf("Expected errBadFileMerkleRoot but received '%v'", err)
	}

	// expect errBadWindowStart
	badPayment = deepCopy(payment)
	badPayment.NewWindowStart = curr.NewWindowStart + 1
	err = verifyPaymentRevision(curr, badPayment, height, amount)
	if err != errBadWindowStart {
		t.Fatalf("Expected errBadWindowStart but received '%v'", err)
	}

	// expect errBadWindowEnd
	badPayment = deepCopy(payment)
	badPayment.NewWindowEnd = curr.NewWindowEnd - 1
	err = verifyPaymentRevision(curr, badPayment, height, amount)
	if err != errBadWindowEnd {
		t.Fatalf("Expected errBadWindowEnd but received '%v'", err)
	}

	// expect errBadUnlockHash
	badPayment = deepCopy(payment)
	badPayment.NewUnlockHash = types.UnlockHash(hash)
	err = verifyPaymentRevision(curr, badPayment, height, amount)
	if err != errBadUnlockHash {
		t.Fatalf("Expected errBadUnlockHash but received '%v'", err)
	}

	// expect errLowHostMissedOutput
	badCurr = deepCopy(curr)
	badCurr.SetMissedHostPayout(payment.MissedHostOutput().Value.Sub64(1))
	err = verifyPaymentRevision(badCurr, payment, height, amount)
	if err != errLowHostMissedOutput {
		t.Fatalf("Expected errLowHostMissedOutput but received '%v'", err)
	}
}

// balanceTracker is a helper struct to track ephemeral account balances and
// return when they need to be refilled.
type balanceTracker struct {
	balances  map[modules.AccountID]int64
	threshold int64
	mu        sync.Mutex
}

// TrackDeposit deposits the given amount into the specified account
func (bt *balanceTracker) TrackDeposit(id modules.AccountID, deposit int64) {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	bt.balances[id] += deposit
}

// TrackWithdrawal withdraws the given amount from the account with specified
// id. Returns whether the account should be refilled or not depending on the
// balance tracker's threshold.
func (bt *balanceTracker) TrackWithdrawal(id modules.AccountID, withdrawal int64) (refill bool) {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	bt.balances[id] -= withdrawal
	if bt.balances[id] < bt.threshold {
		return true
	}
	return
}

// TestProcessParallelPayments tests the behaviour of the ProcessPayment method
// when multiple threads use multiple contracts and ephemeral accounts at the
// same time to perform payments.
func TestProcessParallelPayments(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// determine a reasonable timeout
	var timeout time.Duration
	if build.VLONG {
		timeout = time.Minute
	} else {
		timeout = 10 * time.Second
	}

	// setup the host
	ht, err := newHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	am := ht.host.staticAccountManager

	var refillAmount uint64 = 100
	var maxWithdrawalAmount uint64 = 10

	// setup a balance tracker
	bt := &balanceTracker{
		balances:  make(map[modules.AccountID]int64),
		threshold: int64(maxWithdrawalAmount),
	}

	// setup multiple renters and SOs
	pairs := make([]*renterHostPair, runtime.NumCPU())
	for i := range pairs {
		_, pair, err := newRenterHostPairCustomHostTester(ht)
		if err != nil {
			t.Fatal(err)
		}
		pairs[i] = pair

		if err := callDeposit(am, pair.eaid, types.NewCurrency64(refillAmount)); err != nil {
			t.Log("failed deposit", err)
			t.Fatal(err)
		}
		bt.TrackDeposit(pair.eaid, int64(refillAmount))
	}

	// setup a lock guarding the filecontracts seeing as we are concurrently
	// accessing them and generating revisions for them
	fcLocks := make(map[types.FileContractID]*sync.Mutex)
	for _, pair := range pairs {
		fcLocks[pair.fcid] = new(sync.Mutex)
	}

	var fcPayments uint64
	var eaPayments uint64
	var fcFailures uint64
	var eaFailures uint64

	// start the timer
	finished := make(chan struct{})
	time.AfterFunc(timeout, func() {
		close(finished)
	})

	for range pairs {
		go func() {
			// create two streams
			rs, hs := NewTestStreams()
			defer rs.Close()
			defer hs.Close()

		LOOP:
			for {
				select {
				case <-finished:
					break LOOP
				default:
				}

				// generate random pair and amount
				rp := pairs[fastrand.Intn(len(pairs))]
				rw := fastrand.Uint64n(maxWithdrawalAmount) + 1
				ra := types.NewCurrency64(rw)

				// pay by contract 5% of time
				var payByFC bool
				if fastrand.Intn(100) < 5 {
					payByFC = true
				}

				// randomly pick a flow and run it
				var failed bool
				var pd modules.PaymentDetails
				var err error
				if payByFC {
					fcLocks[rp.fcid].Lock()
					if pd, failed, err = runPayByContractFlow(rp, rs, hs, ra); failed {
						atomic.AddUint64(&fcFailures, 1)
					}
					atomic.AddUint64(&fcPayments, 1)
					fcLocks[rp.fcid].Unlock()
				} else {
					refill := bt.TrackWithdrawal(rp.eaid, int64(rw))
					if refill {
						go func(id modules.AccountID) {
							time.Sleep(100 * time.Millisecond) // make it slow
							if err := callDeposit(am, id, types.NewCurrency64(refillAmount)); err != nil {
								t.Error(err)
							}
							bt.TrackDeposit(id, int64(refillAmount))
						}(rp.eaid)
					}
					if pd, failed, err = runPayByEphemeralAccountFlow(rp, rs, hs, ra); failed {
						atomic.AddUint64(&eaFailures, 1)
					}
					atomic.AddUint64(&eaPayments, 1)
				}

				// compare amount paid to what we expect
				if !failed && err == nil && !pd.Amount().Equals(ra) {
					err = fmt.Errorf("Unexpected amount paid, expected %v actual %v", ra, pd.Amount())
				}

				if err != nil {
					t.Error(err)
					break LOOP
				}
			}
		}()
	}
	<-finished

	t.Logf("\n\nIn %.f seconds, on %d cores, the following payments completed successfully\nPayByContract: %d (%v expected failures)\nPayByEphemeralAccount: %d (%v expected failures)\n\n", timeout.Seconds(), runtime.NumCPU(), atomic.LoadUint64(&fcPayments), atomic.LoadUint64(&fcFailures), atomic.LoadUint64(&eaPayments), atomic.LoadUint64(&eaFailures))
}

// runPayByContractFlow is a helper function that runs the 'PayByContract' flow
// and returns the result of running it.
func runPayByContractFlow(pair *renterHostPair, rStream, hStream siamux.Stream, amount types.Currency) (payment modules.PaymentDetails, fail bool, err error) {
	if fastrand.Intn(100) < 5 { // fail 5% of time
		fail = true
	}

	defer func() {
		if fail && err == nil {
			err = errors.AddContext(err, "Expected failure but error was nil")
			return
		}
		if fail && err != nil && strings.Contains(err.Error(), "Invalid payment revision") {
			err = nil
		}
	}()

	err = run(
		func() error {
			// prepare an updated revision that pays the host
			rev, sig, err := pair.paymentRevision(amount)
			if err != nil {
				return err
			}
			// corrupt the revision if we're expected to fail
			if fail {
				rev.SetValidRenterPayout(rev.ValidRenterPayout().Add64(1))
			}
			// send PaymentRequest & PayByContractRequest
			pRequest := modules.PaymentRequest{Type: modules.PayByContract}
			pbcRequest := newPayByContractRequest(rev, sig)
			err = modules.RPCWriteAll(rStream, pRequest, pbcRequest)
			if err != nil {
				return err
			}
			// receive PayByContractResponse
			var payByResponse modules.PayByContractResponse
			err = modules.RPCRead(rStream, &payByResponse)
			if err != nil {
				return err
			}
			return nil
		},
		func() error {
			// process payment request
			var pErr error
			payment, pErr = pair.host.ProcessPayment(hStream)
			if pErr != nil {
				modules.RPCWriteError(hStream, pErr)
			}
			return nil
		},
	)
	return
}

// runPayByContractFlow is a helper function that runs the
// 'PayByEphemeralAccount' flow and returns the result of running it.
func runPayByEphemeralAccountFlow(pair *renterHostPair, rStream, hStream siamux.Stream, amount types.Currency) (payment modules.PaymentDetails, fail bool, err error) {
	if fastrand.Intn(100) < 5 { // fail 5% of time
		fail = true
	}

	defer func() {
		if fail && err == nil {
			err = errors.AddContext(err, "Expected failure but error was nil")
			return
		}
		if fail && err != nil && strings.Contains(err.Error(), modules.ErrWithdrawalInvalidSignature.Error()) {
			err = nil
		}
	}()

	err = run(
		func() error {
			// create the request
			pbeaRequest := newPayByEphemeralAccountRequest(pair.eaid, pair.host.blockHeight+6, amount, pair.renter)

			if fail {
				// this induces failure because the nonce will be different and
				// this the signature will be invalid
				pbeaRequest.Signature = newPayByEphemeralAccountRequest(pair.eaid, pair.host.blockHeight+6, amount, pair.renter).Signature
			}

			// send PaymentRequest & PayByEphemeralAccountRequest
			pRequest := modules.PaymentRequest{Type: modules.PayByEphemeralAccount}
			err := modules.RPCWriteAll(rStream, pRequest, pbeaRequest)
			if err != nil {
				return err
			}

			// receive PayByEphemeralAccountResponse
			var payByResponse modules.PayByEphemeralAccountResponse
			err = modules.RPCRead(rStream, &payByResponse)
			if err != nil {
				return err
			}
			return nil
		},
		func() error {
			// process payment request
			payment, err = pair.host.ProcessPayment(hStream)
			if err != nil {
				modules.RPCWriteError(hStream, err)
			}
			return nil
		},
	)
	return
}

// TestProcessPayment verifies the host's ProcessPayment method. It covers both
// the PayByContract and PayByEphemeralAccount payment methods.
func TestProcessPayment(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// setup a host and renter pair with an emulated file contract between them
	ht, pair, err := newRenterHostPair(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer ht.Close()

	// test both payment methods
	testPayByContract(t, pair)
	testPayByEphemeralAccount(t, pair)
}

// testPayByContract verifies payment is processed correctly in the case of the
// PayByContract payment method.
func testPayByContract(t *testing.T, pair *renterHostPair) {
	host, renterSK := pair.host, pair.renter
	amount := types.SiacoinPrecision
	amountStr := amount.HumanString()

	// prepare an updated revision that pays the host
	rev, sig, err := pair.paymentRevision(amount)
	if err != nil {
		t.Fatal(err)
	}

	// create two streams
	rStream, hStream := NewTestStreams()
	defer rStream.Close()
	defer hStream.Close()

	var payment modules.PaymentDetails
	var payByResponse modules.PayByContractResponse

	renterFunc := func() error {
		// send PaymentRequest & PayByContractRequest
		pRequest := modules.PaymentRequest{Type: modules.PayByContract}
		pbcRequest := newPayByContractRequest(rev, sig)
		err := modules.RPCWriteAll(rStream, pRequest, pbcRequest)
		if err != nil {
			return err
		}

		// receive PayByContractResponse
		err = modules.RPCRead(rStream, &payByResponse)
		if err != nil {
			return err
		}
		return nil
	}
	hostFunc := func() error {
		// process payment request
		payment, err = host.ProcessPayment(hStream)
		if err != nil {
			modules.RPCWriteError(hStream, err)
		}
		return nil
	}

	// run the payment code
	err = run(renterFunc, hostFunc)
	if err != nil {
		t.Fatal("Unexpected error occurred", err.Error())
	}

	// verify the host's signature
	hash := crypto.HashAll(rev)
	var hpk crypto.PublicKey
	copy(hpk[:], host.PublicKey().Key)
	err = crypto.VerifyHash(hash, hpk, payByResponse.Signature)
	if err != nil {
		t.Fatal("could not verify host's signature")
	}

	// verify the host updated the storage obligation
	updated, err := host.managedGetStorageObligation(pair.fcid)
	if err != nil {
		t.Fatal(err)
	}
	recent, err := updated.recentRevision()
	if err != nil {
		t.Fatal(err)
	}
	if rev.NewRevisionNumber != recent.NewRevisionNumber {
		t.Log("expected", rev.NewRevisionNumber)
		t.Log("actual", recent.NewRevisionNumber)
		t.Fatal("Unexpected revision number")
	}

	// verify the payment details
	if !payment.Amount().Equals(amount) {
		t.Fatalf("Unexpected amount paid, expected %v actual %v", amountStr, payment.Amount().HumanString())
	}
	if !payment.AddedCollateral().IsZero() {
		t.Fatalf("Unexpected collateral added, expected 0H actual %v", payment.AddedCollateral())
	}

	// prepare a set of payouts that do not deduct payment from the renter
	validPayouts, missedPayouts := updated.payouts()
	validPayouts[1].Value = validPayouts[1].Value.Add(amount)
	missedPayouts[0].Value = missedPayouts[0].Value.Sub(amount)
	missedPayouts[1].Value = missedPayouts[1].Value.Add(amount)

	// overwrite the correct payouts with the faulty payouts
	rev, err = recent.PaymentRevision(amount)
	if err != nil {
		t.Fatal(err)
	}
	rev.NewValidProofOutputs = validPayouts
	rev.NewMissedProofOutputs = missedPayouts
	sig = revisionSignature(rev, host.blockHeight, renterSK)

	// verify err is not nil
	err = run(renterFunc, hostFunc)
	if err == nil || !strings.Contains(err.Error(), "Invalid payment revision") {
		t.Fatalf("Expected error indicating the invalid revision, instead error was: '%v'", err)
	}
}

// testPayByEphemeralAccount verifies payment is processed correctly in the case
// of the PayByEphemeralAccount payment method.
func testPayByEphemeralAccount(t *testing.T, pair *renterHostPair) {
	host := pair.host
	amount := types.NewCurrency64(5)
	deposit := types.NewCurrency64(8) // enough to perform 1 payment, but not 2

	// prepare an ephmeral account and fund it
	sk, accountID := prepareAccount()
	err := callDeposit(host.staticAccountManager, accountID, deposit)
	if err != nil {
		t.Fatal(err)
	}
	// create two streams
	rStream, hStream := NewTestStreams()
	defer rStream.Close()
	defer hStream.Close()

	var payment modules.PaymentDetails
	var payByResponse modules.PayByEphemeralAccountResponse

	renterFunc := func() error {
		// send PaymentRequest & PayByEphemeralAccountRequest
		pRequest := modules.PaymentRequest{Type: modules.PayByEphemeralAccount}
		pbcRequest := newPayByEphemeralAccountRequest(accountID, host.blockHeight+6, amount, sk)
		err := modules.RPCWriteAll(rStream, pRequest, pbcRequest)
		if err != nil {
			return err
		}

		// receive PayByEphemeralAccountResponse
		err = modules.RPCRead(rStream, &payByResponse)
		if err != nil {
			return err
		}
		return nil
	}
	hostFunc := func() error {
		// process payment request
		payment, err = host.ProcessPayment(hStream)
		if err != nil {
			modules.RPCWriteError(hStream, err)
		}
		return nil
	}

	// verify err is nil
	err = run(renterFunc, hostFunc)
	if err != nil {
		t.Fatal("Unexpected error occurred", err.Error())
	}

	// verify the account id that's returned equals the account
	if payment.AccountID() != accountID {
		t.Fatalf("Unexpected account id, expected %s but received %s", accountID, payment.AccountID())
	}

	// verify the response contains the amount that got withdrawn
	if !payByResponse.Amount.Equals(amount) {
		t.Fatalf("Unexpected payment amount, expected %s, but received %s", amount.HumanString(), payByResponse.Amount.HumanString())
	}

	// verify the payment got withdrawn from the ephemeral account
	balance := getAccountBalance(host.staticAccountManager, accountID)
	if !balance.Equals(deposit.Sub(amount)) {
		t.Fatalf("Unexpected account balance, expected %v but received %s", deposit.Sub(amount), balance.HumanString())
	}

	// try and perform the same request again, which should fail because the
	// account balance is insufficient verify err is not nil and contains a
	// mention of insufficient balance
	err = run(renterFunc, hostFunc)
	if err == nil || !strings.Contains(err.Error(), "balance was insufficient") {
		t.Fatalf("Expected error to mention account balance was insuficient, instead error was: '%v'", err)
	}
}

// newPayByContractRequest uses a revision and signature to build the
// PayBycontractRequest
func newPayByContractRequest(rev types.FileContractRevision, sig crypto.Signature) modules.PayByContractRequest {
	var req modules.PayByContractRequest

	req.ContractID = rev.ID()
	req.NewRevisionNumber = rev.NewRevisionNumber
	req.NewValidProofValues = make([]types.Currency, len(rev.NewValidProofOutputs))
	for i, o := range rev.NewValidProofOutputs {
		req.NewValidProofValues[i] = o.Value
	}
	req.NewMissedProofValues = make([]types.Currency, len(rev.NewMissedProofOutputs))
	for i, o := range rev.NewMissedProofOutputs {
		req.NewMissedProofValues[i] = o.Value
	}
	req.Signature = sig[:]

	return req
}

// newPayByEphemeralAccountRequest uses the given parameters to create a
// PayByEphemeralAccountRequest
func newPayByEphemeralAccountRequest(account modules.AccountID, expiry types.BlockHeight, amount types.Currency, sk crypto.SecretKey) modules.PayByEphemeralAccountRequest {
	// generate a nonce
	var nonce [modules.WithdrawalNonceSize]byte
	copy(nonce[:], fastrand.Bytes(len(nonce)))

	// create a new WithdrawalMessage
	wm := modules.WithdrawalMessage{
		Account: account,
		Expiry:  expiry,
		Amount:  amount,
		Nonce:   nonce,
	}

	// sign it
	sig := crypto.SignHash(crypto.HashObject(wm), sk)
	return modules.PayByEphemeralAccountRequest{
		Message:   wm,
		Signature: sig,
	}
}

// addNoOpRevision is a helper method that adds a revision to the given
// obligation. In production this 'noOpRevision' is always added, however the
// obligation returned by `newTesterStorageObligation` does not add it.
func (ht *hostTester) addNoOpRevision(so storageObligation, renterPK types.SiaPublicKey) (storageObligation, error) {
	builder, err := ht.wallet.StartTransaction()
	if err != nil {
		return storageObligation{}, err
	}

	txnSet := so.OriginTransactionSet
	contractTxn := txnSet[len(txnSet)-1]
	fc := contractTxn.FileContracts[0]

	noOpRevision := types.FileContractRevision{
		ParentID: contractTxn.FileContractID(0),
		UnlockConditions: types.UnlockConditions{
			PublicKeys: []types.SiaPublicKey{
				renterPK,
				ht.host.publicKey,
			},
			SignaturesRequired: 2,
		},
		NewRevisionNumber:     fc.RevisionNumber + 1,
		NewFileSize:           fc.FileSize,
		NewFileMerkleRoot:     fc.FileMerkleRoot,
		NewWindowStart:        fc.WindowStart,
		NewWindowEnd:          fc.WindowEnd,
		NewValidProofOutputs:  fc.ValidProofOutputs,
		NewMissedProofOutputs: fc.MissedProofOutputs,
		NewUnlockHash:         fc.UnlockHash,
	}

	builder.AddFileContractRevision(noOpRevision)
	tSet, err := builder.Sign(true)
	if err != nil {
		return so, err
	}
	so.RevisionTransactionSet = tSet
	return so, nil
}

// revisionSignature is a helper function that signs the given revision with the
// given key
func revisionSignature(rev types.FileContractRevision, blockHeight types.BlockHeight, secretKey crypto.SecretKey) crypto.Signature {
	signedTxn := types.Transaction{
		FileContractRevisions: []types.FileContractRevision{rev},
		TransactionSignatures: []types.TransactionSignature{{
			ParentID:       crypto.Hash(rev.ParentID),
			CoveredFields:  types.CoveredFields{FileContractRevisions: []uint64{0}},
			PublicKeyIndex: 0,
		}},
	}
	hash := signedTxn.SigHash(0, blockHeight)
	return crypto.SignHash(hash, secretKey)
}

// run is a helper function that runs the given functions in separate goroutines
// and awaits them
func run(f1, f2 func() error) error {
	var errF1, errF2 error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		errF1 = f1()
		wg.Done()
	}()
	wg.Add(1)
	go func() {
		errF2 = f2()
		wg.Done()
	}()
	wg.Wait()
	return errors.Compose(errF1, errF2)
}

// testStream is a helper struct that wraps a net.Conn and implements the
// siamux.Stream interface.
type testStream struct {
	c net.Conn
}

// NewTestStreams returns two siamux.Stream mock objects.
func NewTestStreams() (client siamux.Stream, server siamux.Stream) {
	var clientConn net.Conn
	var serverConn net.Conn
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		serverConn, _ = ln.Accept()
		wg.Done()
	}()
	clientConn, _ = net.Dial("tcp", ln.Addr().String())
	wg.Wait()

	client = testStream{c: clientConn}
	server = testStream{c: serverConn}
	return
}

func (s testStream) Read(b []byte) (n int, err error)  { return s.c.Read(b) }
func (s testStream) Write(b []byte) (n int, err error) { return s.c.Write(b) }
func (s testStream) Close() error                      { return s.c.Close() }

func (s testStream) LocalAddr() net.Addr            { panic("not implemented") }
func (s testStream) RemoteAddr() net.Addr           { panic("not implemented") }
func (s testStream) SetDeadline(t time.Time) error  { panic("not implemented") }
func (s testStream) SetPriority(priority int) error { panic("not implemented") }

func (s testStream) SetReadDeadline(t time.Time) error {
	panic("not implemented")
}
func (s testStream) SetWriteDeadline(t time.Time) error {
	panic("not implemented")
}

// TestStreams is a small test that verifies the working of the test stream. It
// will test that an object can be written to and read from the stream over the
// underlying connection.
func TestStreams(t *testing.T) {
	renter, host := NewTestStreams()

	var pr modules.PaymentRequest
	var wg sync.WaitGroup
	wg.Add(1)
	func() {
		defer wg.Done()
		req := modules.PaymentRequest{Type: modules.PayByContract}
		err := modules.RPCWrite(renter, req)
		if err != nil {
			t.Fatal(err)
		}
	}()

	wg.Add(1)
	func() {
		defer wg.Done()
		err := modules.RPCRead(host, &pr)
		if err != nil {
			t.Fatal(err)
		}
	}()
	wg.Wait()

	if pr.Type != modules.PayByContract {
		t.Fatal("Unexpected request received")
	}
}
