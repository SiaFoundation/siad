package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	mnemonics "gitlab.com/NebulousLabs/entropy-mnemonics"
)

var (
	utilsCmd = &cobra.Command{
		Use:   "utils",
		Short: "various utilities for working with Sia's types",
		Long: `Various utilities for working with Sia's types.
These commands do not require siad.`,
		// Run field not provided; utils requires a subcommand.
	}

	bashcomplCmd = &cobra.Command{
		Use:   "bash-completion [path]",
		Short: "Creates bash completion file.",
		Long: `Creates a bash completion file at the specified location.

Note: Bash completions will only work with the prefix with which the script
is created (e.g. ./siac or siac).

Once created, the file has to be moved to the bash completion script folder,
usually /etc/bash_completion.d/`,
		Run: wrap(bashcomplcmd),
	}

	mangenCmd = &cobra.Command{
		Use:   "man-generation [path]",
		Short: "Creates unix style manpages.",
		Long:  "Creates unix style man pages at the specified directory.",
		Run:   wrap(mangencmd),
	}

	utilsHastingsCmd = &cobra.Command{
		Use:   "hastings [amount]",
		Short: "convert a currency amount to Hastings",
		Long: `Convert a currency amount to Hastings.
See wallet --help for a list of units.`,
		Run: wrap(utilshastingscmd),
	}

	utilsEncodeRawTxnCmd = &cobra.Command{
		Use:   "encoderawtxn [json txn]",
		Short: "convert a JSON-encoded transaction to base64",
		Long: `Convert a JSON-encoded transaction to base64.
The argument may be either a JSON literal or a file containing JSON.`,
		Run: wrap(utilsencoderawtxncmd),
	}

	utilsDecodeRawTxnCmd = &cobra.Command{
		Use:   "decoderawtxn [base64 txn]",
		Short: "convert a base64-encoded transaction to JSON",
		Long:  `Convert a base64-encoded transaction to JSON.`,
		Run:   wrap(utilsdecoderawtxncmd),
	}

	utilsSigHashCmd = &cobra.Command{
		Use:   "sighash [sig index] [txn]",
		Short: "calculate the SigHash of a transaction",
		Long: `Calculate the SigHash of a transaction.
The SigHash is the hash of the fields of the transaction specified
in the CoveredFields of the specified signature.
The transaction may be JSON, base64, or a file containing either.`,
		Run: wrap(utilssighashcmd),
	}

	utilsCheckSigCmd = &cobra.Command{
		Use:   "checksig [sig] [hash] [pubkey]",
		Short: "verify a signature of the specified hash",
		Long: `Verify that a hash was signed by the specified key.

The signature should be base64-encoded, and the hash should be hex-encoded.
The pubkey should be either a JSON-encoded SiaPublicKey, or of the form:
    algorithm:hexkey
e.g. ed25519:d0e1a2d3b4e5e6f7...

Use sighash to calculate the hash of a transaction.
`,
		Run: wrap(utilschecksigcmd),
	}

	utilsVerifySeedCmd = &cobra.Command{
		Use:   "verify-seed",
		Short: "verify seed is formatted correctly",
		Long: `Verify that a seed has 29 words, no extra whitespace,
and all words appear in the Sia dictionary`,
		Run: wrap(utilsverifyseed),
	}
)

func bashcomplcmd(path string) {
	rootCmd.GenBashCompletionFile(path)
}

func mangencmd(path string) {
	doc.GenManTree(rootCmd, &doc.GenManHeader{
		Section: "1",
		Manual:  "siac Manual",
		Source:  "",
	}, path)
}

func utilshastingscmd(amount string) {
	hastings, err := parseCurrency(amount)
	if err != nil {
		die(err)
	}
	fmt.Println(hastings)
}

func utilsdecoderawtxncmd(b64 string) {
	bin, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		die("Invalid base64:", err)
	}
	var txn types.Transaction
	if err := encoding.Unmarshal(bin, &txn); err != nil {
		die("Invalid transaction:", err)
	}
	js, _ := json.MarshalIndent(txn, "", "\t")
	fmt.Println(string(js))
}

func utilsencoderawtxncmd(jstxn string) {
	var jsBytes []byte
	if strings.HasPrefix(strings.TrimSpace(jstxn), "{") {
		// assume JSON if arg starts with {
		jsBytes = []byte(jstxn)
	} else {
		// otherwise, assume it's a file containing JSON
		var err error
		jsBytes, err = ioutil.ReadFile(jstxn)
		if err != nil {
			die("Could not read JSON file:", err)
		}
	}
	var txn types.Transaction
	if err := json.Unmarshal(jsBytes, &txn); err != nil {
		die("Invalid transaction:", err)
	}
	fmt.Println(base64.StdEncoding.EncodeToString(encoding.Marshal(txn)))
}

func utilssighashcmd(indexStr, txnStr string) {
	index, err := strconv.Atoi(indexStr)
	if err != nil {
		die("Sig index must be an integer")
	}

	// assume txn is a file
	txnBytes, err := ioutil.ReadFile(txnStr)
	if os.IsNotExist(err) {
		// assume txn is a literal encoding
		txnBytes = []byte(txnStr)
	} else if err != nil {
		die("Could not read JSON file:", err)
	}
	// txnBytes is either JSON or base64
	var txn types.Transaction
	if json.Valid(txnBytes) {
		if err := json.Unmarshal(txnBytes, &txn); err != nil {
			die("Could not decode JSON:", err)
		}
	} else {
		bin, err := base64.StdEncoding.DecodeString(string(txnBytes))
		if err != nil {
			die("Could not decode txn as JSON, base64, or file")
		}
		if err := encoding.Unmarshal(bin, &txn); err != nil {
			die("Could not decode binary transaction:", err)
		}
	}

	fmt.Println(txn.SigHash(index, 180e3))
}

func utilschecksigcmd(base64Sig, hexHash, pkStr string) {
	var sig crypto.Signature
	sigBytes, err := base64.StdEncoding.DecodeString(base64Sig)
	if err != nil || copy(sig[:], sigBytes) != len(sig) {
		die("Couldn't parse signature")
	}
	var hash crypto.Hash
	if err := hash.LoadString(hexHash); err != nil {
		die("Couldn't parse hash")
	}
	var spk types.SiaPublicKey
	if spk.LoadString(pkStr); len(spk.Key) == 0 {
		if err := json.Unmarshal([]byte(pkStr), &spk); err != nil {
			die("Couldn't parse pubkey")
		}
	}
	if spk.Algorithm != types.SignatureEd25519 {
		die("Only ed25519 signatures are supported")
	}
	var pk crypto.PublicKey
	copy(pk[:], spk.Key)

	if crypto.VerifyHash(hash, pk, sig) == nil {
		fmt.Println("Verified OK")
	} else {
		log.Fatalln("Bad signature")
	}
}

func utilsverifyseed() {
	seed, err := passwordPrompt("Please enter your seed: ")
	if err != nil {
		die("Could not read seed")
	}

	_, err = modules.StringToSeed(seed, mnemonics.English)
	if err != nil {
		die(err)
	}
	fmt.Println("No issues detected with your seed")

}
