package main

import (
	"strings"

	"go.sia.tech/siad/node"
)

// createNodeParams parses the provided config and creates the corresponding
// node params for the server.
func parseModules(config Config) node.NodeParams {
	params := node.NodeParams{}
	// Parse the modules.
	if strings.Contains(config.Siad.Modules, "a") {
		params.CreateAccounting = true
	}
	if strings.Contains(config.Siad.Modules, "c") {
		params.CreateConsensusSet = true
	}
	if strings.Contains(config.Siad.Modules, "e") {
		params.CreateExplorer = true
	}
	if strings.Contains(config.Siad.Modules, "g") {
		params.CreateGateway = true
	}
	if strings.Contains(config.Siad.Modules, "h") {
		params.CreateHost = true
	}
	if strings.Contains(config.Siad.Modules, "m") {
		params.CreateMiner = true
	}
	if strings.Contains(config.Siad.Modules, "r") {
		params.CreateRenter = true
	}
	if strings.Contains(config.Siad.Modules, "t") {
		params.CreateTransactionPool = true
	}
	if strings.Contains(config.Siad.Modules, "w") {
		params.CreateWallet = true
		// Add Accounting and FeeManager modules to all nodes that have at least
		// a wallet
		params.CreateAccounting = true
	}
	// Parse remaining fields.
	params.Bootstrap = !config.Siad.NoBootstrap
	params.UseUPNP = config.Siad.UseUPNP
	params.HostAddress = config.Siad.HostAddr
	params.RPCAddress = config.Siad.RPCaddr
	params.SiaMuxTCPAddress = config.Siad.SiaMuxTCPAddr
	params.SiaMuxWSAddress = config.Siad.SiaMuxWSAddr
	params.Dir = config.Siad.SiaDir
	return params
}
