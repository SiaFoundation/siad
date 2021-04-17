package api

import (
	"encoding/json"
	"errors"
	"io"
	"testing"

	"gitlab.com/NebulousLabs/encoding"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// TestConsensusGet probes the GET call to /consensus.
func TestIntegrationConsensusGET(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	st, err := createServerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer st.server.panicClose()

	var cg ConsensusGET
	err = st.getAPI("/consensus", &cg)
	if err != nil {
		t.Fatal(err)
	}
	if cg.Height != 4+types.TaxHardforkHeight {
		t.Error("wrong height returned in consensus GET call")
	}
	if cg.CurrentBlock != st.server.api.cs.CurrentBlock().ID() {
		t.Error("wrong block returned in consensus GET call")
	}
	expectedTarget := types.Target{128}
	if cg.Target != expectedTarget {
		t.Error("wrong target returned in consensus GET call")
	}

	if cg.BlockFrequency != types.BlockFrequency {
		t.Error("constant mismatch")
	}
	if cg.SiafundCount.Cmp(types.SiafundCount) != 0 {
		t.Error("constant mismatch")
	}
	if cg.InitialCoinbase != types.InitialCoinbase {
		t.Error("constant mismatch")
	}
}

// TestConsensusValidateTransactionSet probes the POST call to
// /consensus/validate/transactionset.
func TestConsensusValidateTransactionSet(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	st, err := createServerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer st.server.panicClose()

	// Get a transaction to validate.
	txnSet, err := st.wallet.SendSiacoins(types.SiacoinPrecision, types.UnlockHash{})
	if err != nil {
		t.Fatal(err)
	}

	jsonTxns, err := json.Marshal(txnSet)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := HttpPOST("http://"+st.server.listener.Addr().String()+"/consensus/validate/transactionset", string(jsonTxns))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	if non2xx(resp.StatusCode) {
		t.Fatal(decodeError(resp))
	}

	// Try again with an invalid transaction set
	txnSet = []types.Transaction{{TransactionSignatures: []types.TransactionSignature{{}}}}
	jsonTxns, err = json.Marshal(txnSet)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = HttpPOST("http://"+st.server.listener.Addr().String()+"/consensus/validate/transactionset", string(jsonTxns))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()
	if !non2xx(resp.StatusCode) {
		t.Fatal("expected validation error")
	}
}

// TestIntegrationConsensusSubscribe probes the /consensus/subscribe endpoint.
func TestIntegrationConsensusSubscribe(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	st, err := createServerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer st.server.panicClose()

	ccid := modules.ConsensusChangeBeginning
	resp, err := HttpGET("http://" + st.server.listener.Addr().String() + "/consensus/subscribe/" + ccid.String())
	if err != nil {
		t.Fatal("unable to make an http request", err)
	}
	defer func() {
		err := resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()
	if non2xx(resp.StatusCode) {
		t.Fatal(decodeError(resp))
	}
	dec := encoding.NewDecoder(resp.Body, 1e6)
	var cc modules.ConsensusChange
	var ids []modules.ConsensusChangeID
	for {
		if err := dec.Decode(&cc); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, cc.ID)
	}

	// try subscribing from height 3
	resp, err = HttpGET("http://" + st.server.listener.Addr().String() + "/consensus/subscribe/" + ids[2].String())
	if err != nil {
		t.Fatal("unable to make an http request", err)
	}
	defer func() {
		err := resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()
	if non2xx(resp.StatusCode) {
		t.Fatal(decodeError(resp))
	}
	dec = encoding.NewDecoder(resp.Body, 1e6)
	for {
		if err := dec.Decode(&cc); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
	}
}
