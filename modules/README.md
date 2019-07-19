# Modules

The modules package is the top-level package for all modules. It contains the interface for each module, sub-packages which implement said modules and other shared constants and code which needs to be accessible within all sub-packages.

## Top-Level Modules
- [Consensus](#consensus)
- [Explorer](#explorer)
- [Gateway](#gateway)
- [Host](#host)
- [Miner](#miner)
- [Renter](#renter)
- [Transaction Pool](#transaction-pool)
- [Wallet](#wallet)

## Subsystems
- [Alert System](#alert-system)
- [Dependencies](#dependencies)
- [Negotiate](#negotiate)
- [Network Addresses](#network-addresses)
- [Siad Configuration](#siad-configuration)
- [SiaPath](#siapath)
- [Storage Manager](#storage-manager)

### Consensus
**Key Files**
- [consensus.go](./consensus.go)
- [README.md](./consensus/README.md)

*TODO* 
  - fill out module explanation

### Explorer
**Key Files**
- [explorer.go](./explorer.go)
- [README.md](./explorer/README.md)

*TODO* 
  - fill out module explanation

### Gateway
**Key Files**
- [gateway.go](./gateway.go)
- [README.md](./gateway/README.md)

*TODO* 
  - fill out module explanation

### Host
**Key Files**
- [host.go](./host.go)
- [README.md](./host/README.md)

*TODO* 
  - fill out module explanation

### Miner
**Key Files**
- [miner.go](./miner.go)
- [README.md](./miner/README.md)

*TODO* 
  - fill out module explanation

### Renter
**Key Files**
- [renter.go](./renter.go)
- [README.md](./renter/README.md)

*TODO* 
  - fill out module explanation

### Transaction Pool
**Key Files**
- [transactionpool.go](./transactionpool.go)
- [README.md](./transactionpool/README.md)

*TODO* 
  - fill out module explanation

### Wallet
**Key Files**
- [wallet.go](./wallet.go)
- [README.md](./wallet/README.md)

*TODO* 
  - fill out subsystem explanation

### Alert System
**Key Files**
- [alert.go](./alert.go)

The Alert System provides the `Alerter` interface and an implementation of the interface which can be used by modules which need to be able to register alerts in case of irregularities during runtime. An `Alert` provides the following information:

- **Message**: Some information about the issue
- **Cause**: The cause for the issue if it is known
- **Module**: The name of the module that registered the alert
- **Severity**: The severity level associated with the alert

The following levels of severity are currently available:

- **Unknown**: This should never be used and is a safeguard agains developer errors	
- **Warning**: Warns the user about potential issues which might require preventive actions
- **Error**: Alerts the user of an issue that requires immediate action to prevent further issues like loss of data
- **Critical**: Indicates that a critical error is imminent. e.g. lack of funds causing contracts to get lost

### Dependencies
**Key Files**
- [dependencies.go](./dependencies.go)

*TODO* 
  - fill out subsystem explanation

### Negotiate
**Key Files**
- [negotiate.go](./negotiate.go)

*TODO* 
  - fill out subsystem explanation

### Network Addresses
**Key Files**
- [netaddress.go](./netaddress.go)

*TODO* 
  - fill out subsystem explanation

### Siad Configuration
**Key Files**
- [siadconfig.go](./siadconfig.go)

*TODO* 
  - fill out subsystem explanation

### SiaPath
**Key Files**
- [siapath.go](./siapath.go)

*TODO* 
  - fill out subsystem explanation

### Storage Manager
**Key Files**
- [storagemanager.go](./storagemanager.go)

*TODO* 
  - fill out subsystem explanation
