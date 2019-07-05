# Wallet

### Wallet Encryption

This section gives an overview of how encryption is handled within the Sia wallet and refers to the source code within `encrypt.go`. All of the encryption within the wallet is done using the Twofish cipher in Galois Counter Mode and hashing is done using the Blake2B algorithm. 

#### Masterkey

The masterkey can either be a custom password provided by the user or the wallet seed in the default case. Either way, the custom password or seed are hashed before being used to encrypt/decrypt the wallet to produce 32 bytes of entropy for the Twofish cipher. The masterkey itself is also never used directly to encrypt any data. Instead it is used with the wallet's UID to derive a key to encrypt the seed.

To allow the user to recover the wallet after forgetting a custom password, the masterkey is also encrypted and stored within the wallet's BoltDB bucket. In this case the encryption key used is derived from the primary seed and the wallet's UID. 

#### Seed Encryption

To protect the user's primary seed, the so-called `seedFile` is encrypted before being stored on disk within the wallet's BoltDB bucket. This file contains the primary seed, a random UID and the encrypted verification plaintext. The UID is used to derive a key to encrypt the verification plaintext from the masterkey and the verification plaintext can be used to verify that decrypting the seed was successful. This is done by decrypting the seedFile and then decrypting the verification plaintext and comparing it to the expected plaintext in the `verificationPlaintext`constant. Without the verification plaintext this wouldn't be possible since both the UID and the seed are random entropy.

#### Locking / Unlocking the Wallet

"Locking" and "Unlocking" the wallet refers to the process of wiping sensitive data like the primary seed from memory and loading the encrypted data from disk into memory and decrypting it respectively. 

#### Changing the password

There are 2 ways to change the wallet's password. Either by providing the current masterkey which allows the wallet to decrypt the data on disk and reencrypt it using a new key or by using the primary seed. The latter will use the seed to retrieve the masterkey from disk and then use it to reencrypt the wallet.
