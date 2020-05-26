# Persist
The persist package contains functionality for storing persisted data.

## Subsystems
- [appendonly](#appendonly)
- [boltdb](#boltdb)
- [json](#json)
- [log](#log)
- [persist](#persist)

### AppendOnly
**Key Files**
- [appendonly.go](./appendonly.go)

The AppendOnly Persistence System is responsible for persistence files
maintained in an append-only fashion and ensures safe and performant ACID
operations. An append-only structure is used with the length in bytes encoded in
the metadata.

**Inbound Complexities**
 - `NewAppendOnlyPersist` initializes the persistence file, either creating or
   loading it
 - `Write` appends the information to the persistence file

### BoltDB
**Key Files**
- [boltdb.go](./boltdb.go)

*TODO* 
  - fill out module explanation

### JSON
**Key Files**
- [json.go](./json.go)

*TODO* 
  - fill out module explanation

### Log
**Key Files**
- [log.go](./log.go)

*TODO* 
  - fill out module explanation

### Persist
**Key Files**
- [persist.go](./persist.go)

*TODO* 
  - fill out module explanation
