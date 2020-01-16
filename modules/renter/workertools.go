package renter

// workertools.go provides a set of tools for the workers to use. An example of
// a tool would be a downloader, or an MDM instance. The goal of the toolkit is
// to wrap everything so that the worker code can montior things like failure
// rates on its tools, latencies to the host, throughputs, etc. This monitoring
// code is all centralized in the tools file so that it can be updated and
// expanded without having to rewrite any of the actual project code.
//
// This file can also perform optimizations that combine jobs. For example, if
// the worker runs multiple jobs in parallel to request data from the host,
// these tools can combine the requests together, invisible to the project code.
//
// This should make for generally more scalable engineering as the set of things
// the worker is capable of continues to expand.

// Downloader returns an interface that can be used by the worker to fetch data.
func (w *worker) Downloader() (contractor.Downloader, error) {
	return w.hostContractor.Downloader(w.staticHostPubKey, w.renter.tg.StopChan())
}
