# Typesutils

The typesutils package provides helper methods for working with transactions,
especially during testing and debugging.

## Subsystems

Typesutils has the following subsystems:
 - [Minimum Transaction Set](#minimum-transaction-set)
 - [Transaction Graph](#transaction-graph)

### Minimum Transaction Set
**Key Files**
 - [minimumtransactionsetexports.go](./minimumtransactionsetexports.go)

The minimum transaction set function takes two transaction sets as input. The
first is a set of required transactions (often just one transaction), and the
second is a set of transactions that may be required dependencies. The function
will return a single transaction set which contains all of the required
transactions and all of their direct/required dependencies, and no other
transactions.

It is fine for the two input sets to contain overlapping transactions, however
each set much be independently ordered correctly and valid.

##### Exports

 - `MinimizeTransactionSet` is an independent function which returns a
   transaction set that contains all transactions of its first input plus any
   required dependencies from its second input.

### Transaction Graph
**Key Files**
 - [transactiongraphexports.go](./transactiongraphexports.go)

The Transaction Graph is a tool for building sets of transactions that have
specific properties. This can be useful for testing modules such as the
transaction pool to see how the transaction pool responds to certain dependency
graphs or fee structures. The goal of the transaction graph is to be a much
simpler method for constructing elaborate transaction setups vs. constructing
these setups by hand.

Note: With the exception of one struct, all of the code in the transaction graph
subsystem is exported.

##### Exports

 - `SimpleTransaction` is an outline of a transaction that should be added to
   the transaction graph. It has the same field names as a types.Transaction,
   however they have been greatly simplified to make building transactions
   easier.
 - `TransactionGraph` is the stateful object that can be used to incrementally
   build an elaborate transaction graph.
   - `TransactionGraph.AddSiacoinSource` is a method that allows a source input
	 to be added to the transaction graph. The transactions in the graph will
	 only be valid if they have some base input consisting of pre-existing
	 siacoins, and this method allows the caller to supply such an input. This
	 method can be used as many times as necessary. This method will return an
	 index that tells you how to spend the output within the transaction graph.
   - `TransactionGraph.AddTransaction` will take a simple transaction as input
	 and compose it into a full, valid transaction within the graph. Basic
	 checking is also performed to ensure that all inputs are valid, and that
	 the input totals match the output totals for the final transaction.
   - `TransactionGraph.Transactions` will return all of the transactions that
	 have been built to be a part of the transaction graph.
- `NewTransactionGraph` will initialize and return a `TransactionGraph`.
