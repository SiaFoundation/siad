// Package server provides a server that can wrap a node and serve an http api
// for interacting with the node.
package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	mnemonics "gitlab.com/NebulousLabs/entropy-mnemonics"
	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/node"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/Sia/types"
)

// A Server is a collection of siad modules that can be communicated with over
// an http api.
type Server struct {
	api               *api.API
	apiServer         *http.Server
	done              chan struct{}
	listener          net.Listener
	node              *node.Node
	requiredUserAgent string
	serveErr          error
	Dir               string

	closeMu sync.Mutex
}

// serve listens for and handles API calls. It is a blocking function.
func (srv *Server) serve() error {
	// The server will run until an error is encountered or the listener is
	// closed, via either the Close method or by signal handling.  Closing the
	// listener will result in the benign error handled below.
	err := srv.apiServer.Serve(srv.listener)
	if err != nil && !strings.HasSuffix(err.Error(), "use of closed network connection") {
		return err
	}
	return nil
}

// Close closes the Server's listener, causing the HTTP server to shut down.
func (srv *Server) Close() error {
	srv.closeMu.Lock()
	defer srv.closeMu.Unlock()
	// Stop accepting API requests.
	err := srv.apiServer.Shutdown(context.Background())
	// Wait for serve() to return and capture its error.
	<-srv.done
	if srv.serveErr != http.ErrServerClosed {
		err = errors.Compose(err, srv.serveErr)
	}
	// Shutdown modules.
	if srv.node != nil {
		err = errors.Compose(err, srv.node.Close())
	}
	return errors.AddContext(err, "error while closing server")
}

// APIAddress returns the underlying node's api address
func (srv *Server) APIAddress() string {
	return srv.listener.Addr().String()
}

// GatewayAddress returns the underlying node's gateway address
func (srv *Server) GatewayAddress() modules.NetAddress {
	return srv.node.Gateway.Address()
}

// HostPublicKey returns the host's public key or an error if the node is no
// host.
func (srv *Server) HostPublicKey() (types.SiaPublicKey, error) {
	if srv.node.Host == nil {
		return types.SiaPublicKey{}, errors.New("can't get public host key of a non-host node")
	}
	return srv.node.Host.PublicKey(), nil
}

// ServeErr is a blocking call that will return the result of srv.serve after
// the server stopped.
func (srv *Server) ServeErr() <-chan error {
	c := make(chan error)
	go func() {
		<-srv.done
		close(c)
	}()
	return c
}

// Unlock unlocks the server's wallet using the provided password.
func (srv *Server) Unlock(password string) error {
	if srv.node.Wallet == nil {
		return errors.New("server doesn't have a wallet")
	}
	var validKeys []crypto.CipherKey
	dicts := []mnemonics.DictionaryID{"english", "german", "japanese"}
	for _, dict := range dicts {
		seed, err := modules.StringToSeed(password, dict)
		if err != nil {
			continue
		}
		validKeys = append(validKeys, crypto.NewWalletKey(crypto.HashObject(seed)))
	}
	validKeys = append(validKeys, crypto.NewWalletKey(crypto.HashObject(password)))
	for _, key := range validKeys {
		if err := srv.node.Wallet.Unlock(key); err == nil {
			return nil
		}
	}
	return modules.ErrBadEncryptionKey
}

// New creates a new API server from the provided modules. The API will
// require authentication using HTTP basic auth if the supplied password is not
// the empty string. Usernames are ignored for authentication. This type of
// authentication sends passwords in plaintext and should therefore only be
// used if the APIaddr is localhost.
func New(APIaddr string, requiredUserAgent string, requiredPassword string, nodeParams node.NodeParams) (*Server, error) {
	// Create the server listener.
	listener, err := net.Listen("tcp", APIaddr)
	if err != nil {
		return nil, err
	}

	// Load the config file.
	cfg, err := modules.NewConfig(filepath.Join(nodeParams.Dir, configName))
	if err != nil {
		return nil, errors.AddContext(err, "failed to load siad config")
	}

	// Create the api for the server.
	api := api.New(cfg, requiredUserAgent, requiredPassword, nil, nil, nil, nil, nil, nil, nil, nil)
	srv := &Server{
		api: api,
		apiServer: &http.Server{
			Handler: api,

			// set reasonable timeout windows for requests, to prevent the Sia API
			// server from leaking file descriptors due to slow, disappearing, or
			// unreliable API clients.

			// ReadTimeout defines the maximum amount of time allowed to fully read
			// the request body. This timeout is applied to every handler in the
			// server.
			ReadTimeout: time.Minute * 60,

			// ReadHeaderTimeout defines the amount of time allowed to fully read the
			// request headers.
			ReadHeaderTimeout: time.Minute * 2,

			// IdleTimeout defines the maximum duration a HTTP Keep-Alive connection
			// the API is kept open with no activity before closing.
			IdleTimeout: time.Minute * 5,
		},
		done:              make(chan struct{}),
		listener:          listener,
		requiredUserAgent: requiredUserAgent,
		Dir:               nodeParams.Dir,
	}

	// Set the shutdown method to allow the api to shutdown the server.
	api.Shutdown = srv.Close

	// Spin up a goroutine that serves the API and closes srv.done when
	// finished.
	go func() {
		srv.serveErr = srv.serve()
		close(srv.done)
	}()

	// Create the Sia node for the server after the server was started.
	node, errChan := node.New(nodeParams)
	select {
	case err := <-errChan:
		if err != nil {
			if isAddrInUseErr(err) {
				return nil, fmt.Errorf("%v; are you running another instance of siad?", err.Error())
			}
			return nil, errors.AddContext(err, "server is unable to create the Sia node")
		}
	default:
	}

	// Make sure that the server wasn't shut down while loading the modules.
	srv.closeMu.Lock()
	defer srv.closeMu.Unlock()
	select {
	case <-srv.done:
		// Server was shut down. Close node and exit.
		return srv, node.Close()
	default:
	}
	// Server wasn't shut down. Add node and replace modules.
	srv.node = node
	api.SetModules(node.ConsensusSet, node.Explorer, node.Gateway, node.Host, node.Miner, node.Renter, node.TransactionPool, node.Wallet)

	// Wait for the node to be done loading.
	select {
	case err := <-errChan:
		if err != nil {
			// Error occured during async load. Close all modules.
			return nil, node.Close()
		}
	}
	return srv, nil
}

// isAddrInUseErr checks if the error corresponds to syscall.EADDRINUSE
func isAddrInUseErr(err error) bool {
	if opErr, ok := err.(*net.OpError); ok {
		if syscallErr, ok := opErr.Err.(*os.SyscallError); ok {
			return syscallErr.Err == syscall.EADDRINUSE
		}
	}
	return false
}
