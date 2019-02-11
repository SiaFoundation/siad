# File Contract Recovery

This document lays out the design for file contract recovery using file
contract identifiers. These identifiers are used to identify ones contracts on
the blockchain and to figure out what host the contract was formed with. So renters
can recognize a contract as their own simply by knowing their own wallet seed.

## Deriving the contract identifier from the wallet seed
This section explains the multi-step process of deriving the identifier from
the wallet seed.

#### Renter Seed and Ephemeral Renter Seed

From the wallet sed we derive a renter seed by combining it with a specifier.
In the following pseudo-code `H` refers to hashing the input using the Blake2B
hashing algorithm.
```
renterSeed := H(walletSeed || "renter")
```
For additional security we don't use the renterSeed directly but we hash it again
with the `windowStart` field of the contract divided by 1000. That way we get an
ephemeral seed which changes every 1000 blocks and even if we hand it out to a 
third party explorer, we don't reveal too much information.
```
ephemeralRenterSeed := H(renterSeed || windowStart / 1000)
```

#### Identifier Seed (32 bytes)
The identifier seed is the seed we use to create the identifiers we put into the
arbitrary data of file contract transactions. It can be handed out to third party
to give them read access to the contracts being created by a specific renter.
```
fileContractIdentifierSeed := H(ephemeralRenterSeed || "identifier_seed")
```

#### Secret Key Seed (32 bytes)
The secret key seed is the seed we use to create secret keys for file contracts
which are used to sign revisions. Giving away this seed allows someone to actually
use the contracts derived from it.
```
fileContractSecretKeySeed := H(ephemeralRenterSeed || "secret_key_seed")
```

#### Signing Key Seed (32 bytes)
The signing key seed is used to derive signing keys which are used to sign the
identifiers themselves. This allows us to give away the identifier seed without
allowing a third party to trick us by creating contracts and using our identifier
seed to make us believe we created that contract ourselves.
```
fileContractSigningKeySeed := H(ephemeralRenterSeed || "signing_key_seed")
```

#### Generating the identifier and keys
The identifier, secret key and signing key can be generated simply by using the
corresponding seed and the `SiacoinOutputID` that is spent by the first input of
the transaction that contains the file contract.
```
fileContractIdentifier := H(fileContractIdentifierSeed || firstInputToFileContract)

fileContractSecretKey := H(fileContractSecretKeySeed || firstInputToFileContract)

fileContractSigningKeyPart1 := H(fileContractSigningKeySeed || firstInputToFileContract || 0)
fileContractSigningKeyPart2 := H(fileContractSigningKeySeed || firstInputToFileContract || 1)
fileContractSigningKey := fileContractSigningKeyPart1 || fileContractSigningKeyPart2
```
The fileContractSigningKey is generated a bit differently since we need 64 bytes
instead of 32 bytes of entropy for Threefish.

#### Creating the final, signed identifier
The final, signed identifier has a length of 80 bytes and consists of three parts.

[0:16]  - the file contract identifier prefix for the arbitrary data

[16:48] - the fileContractIdentifier

[48:80] - a signature created by encrypting the identifier using Threefish. 
The 32 byte identifier is first padded with zeros to match the Threefish blocksize
of 64 bytes. Then it is encrypted and the first 32 bytes of the ciphertext are
used as the signature.

To make sure that we know to which host we can go to recover a specific contract,
we also append the Threefish-encrypted public key of the host to the arbitrary
data of the transaction.

## Contract Recovery

Recovering a contract is relatively straightforward.

1. Check that a contract has the correct arbitrary data prefix
2. Generate the identifier and see if it matches
3. Verify that the signature of the identifier also matches
4. Get the latest revision of the contract from the host
5. Use that revision to pay the host for providing the merkle roots of the uploaded sectors

