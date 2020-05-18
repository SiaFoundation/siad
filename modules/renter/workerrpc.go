package renter

import (
	"fmt"
	"io"
	"sync/atomic"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/siamux"
)

// staticNewStream returns a new stream to the worker's host
func (w *worker) staticNewStream() (siamux.Stream, error) {
	// TODO: Change to '<'
	if build.VersionCmp(w.staticCache().staticHostVersion, minAsyncVersion) != 0 {
		w.renter.log.Critical("calling staticNewStream on a host that doesn't support the new protocol")
		println("bad staticNewStream call")
		return nil, errors.New("host doesn't support this")
	}
	stream, err := w.renter.staticMux.NewStream(modules.HostSiaMuxSubscriberName, w.staticHostMuxAddress, modules.SiaPKToMuxPK(w.staticHostPubKey))
	if err != nil {
		fmt.Printf("%v: failed to get new stream on host: %v\n", w.staticHostPubKeyStr, err)
		return nil, err
	}
	// TODO: Consider marking this host as !GFU and !GFR because they can't be
	// reached on Skynet.
	atomic.StoreUint64(&w.atomicStreamHasBeenValid, 1)
	return stream, nil
}
