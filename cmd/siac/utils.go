package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
	mnemonics "gitlab.com/NebulousLabs/entropy-mnemonics"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/Sia/types"
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
		Long: `Verify that a seed has correct number of words, no extra whitespace,
and all words appear in the Sia dictionary. The language may be english (default), japanese, or german`,
		Run: wrap(utilsverifyseed),
	}

	utilsDisplayAPIPasswordCmd = &cobra.Command{
		Use:   "display-api-password",
		Short: "display the API password",
		Long: `Display the API password.  The API password is required for some 3rd 
party integrations such as Duplicati`,
		Run: wrap(utilsdisplayapipassword),
	}

	utilsBruteForceSeedCmd = &cobra.Command{
		Use:   "bruteforce-seed",
		Short: "attempt to brute force seed",
		Long: `Attempts to brute force a partial Sia seed.  Accepts a 27 or 28 word
seed and returns a valid 28 or 29 word seed`,
		Run: wrap(utilsbruteforceseed),
	}

	utilsUploadedsizeCmd = &cobra.Command{
		Use:   "uploadedsize [path]",
		Short: "calculate a folder's size on Sia",
		Long: `Calculates a given folder size on Sia and the lost space caused by 
files are rounded up to the minimum chunks size.`,
		Run: wrap(utilsuploadedsizecmd),
	}
)

// bashcmlcmd is the handler for the command `siac utils bash-completion`.
func bashcomplcmd(path string) {
	rootCmd.GenBashCompletionFile(path)
}

// mangencmd is the handler for the command `siac utils man-generation`.
// generates siac man pages
func mangencmd(path string) {
	doc.GenManTree(rootCmd, &doc.GenManHeader{
		Section: "1",
		Manual:  "siac Manual",
		Source:  "",
	}, path)
}

// utilshastingscmd is the handler for the command `siac utils hastings`.
// converts a Siacoin amount into hastings.
func utilshastingscmd(amount string) {
	hastings, err := parseCurrency(amount)
	if err != nil {
		die(err)
	}
	fmt.Println(hastings)
}

// utilsdecoderawtxncmd is the handler for command `siac utils decoderawtxn`.
// converts a base64-encoded transaction to JSON encoding
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

// utilsencoderawtxncmd is the handler for command `siac utils encoderawtxn`.
// converts a JSON encoded transaction to base64-encoding
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

// utilssighashcmd is the handler for the command `siac utils sighash`.
// calculates the SigHash of a transaction
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

// utilschecksigcmd is the handler for the command `siac utils checksig`.
// verifies the signature of a hash
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

// utilsverifyseed is the handler for the command `siac utils verify-seed`.
// verifies a seed matches the required formatting.  This can be used to help
// troubleshot seeds that are not being accepted by siad.
func utilsverifyseed() {
	seed, err := passwordPrompt("Please enter your seed: ")
	if err != nil {
		die("Could not read seed")
	}

	_, err = modules.StringToSeed(seed, mnemonics.DictionaryID(strings.ToLower(dictionaryLanguage)))
	if err != nil {
		die(err)
	}
	fmt.Println("No issues detected with your seed")

}

// utilsdisplayapipassword is the handler for the command `siac utils
// display-api-password`.
// displays the API Password to the user.
func utilsdisplayapipassword() {
	fmt.Println(httpClient.Password)
}

// utilsbruteforceseed is the handler for the command `siac utils
// bruteforce-seed`
// attempts to find the one word missing from a seed.
func utilsbruteforceseed() {
	fmt.Println("Enter partial seed: ")
	s := bufio.NewScanner(os.Stdin)
	s.Scan()
	if s.Err() != nil {
		log.Fatal("Couldn't read seed:", s.Err())
	}
	knownWords := strings.Fields(s.Text())
	if len(knownWords) != 27 && len(knownWords) != 28 {
		log.Fatalln("Expected 27 or 28 words in partial seed, got", len(knownWords))
	}
	allWords := make([]string, len(knownWords)+1)
	var did mnemonics.DictionaryID = "english"
	var checked int
	total := len(allWords) * len(mnemonics.EnglishDictionary)
	for i := range allWords {
		copy(allWords[:i], knownWords[:i])
		copy(allWords[i+1:], knownWords[i:])
		for _, word := range mnemonics.EnglishDictionary {
			allWords[i] = word
			s := strings.Join(allWords, " ")
			checksumSeedBytes, _ := mnemonics.FromString(s, did)
			var seed modules.Seed
			copy(seed[:], checksumSeedBytes)
			fullChecksum := crypto.HashObject(seed)
			if len(checksumSeedBytes) == crypto.EntropySize+modules.SeedChecksumSize && bytes.Equal(fullChecksum[:modules.SeedChecksumSize], checksumSeedBytes[crypto.EntropySize:]) {

				if _, err := modules.StringToSeed(s, mnemonics.English); err == nil {
					fmt.Printf("\nFound valid seed! The missing word was %q\n", word)
					fmt.Println(s)
					return
				}
			}
			checked++
			fmt.Printf("\rChecked %v/%v...", checked, total)
		}
	}
	fmt.Printf("\nNo valid seed found :(\n")
}

// utilsuploadedsizecmd is the handler for the command `utils uploadedsize [path] [flags]`
// It estimates the 'on Sia' size of the given directory
func utilsuploadedsizecmd(path string) {
	var fileSizes []uint64
	if fileExists(path) {
		fi, err := os.Stat(path)
		if err != nil {
			fmt.Println("Error: could not determine the file size")
			return
		}
		fileSizes = append(fileSizes, uint64(fi.Size()))
	} else {
		err := filepath.Walk( // export all file sizes to fileSizes slice (recursive)
			path,
			func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if !info.IsDir() {
					fileSizes = append(fileSizes, uint64(info.Size()))
				}
				return nil
			})
		if err != nil {
			fmt.Println("Error walking directory:", err)
			return
		}
	}

	var diskSize, siaSize, lostPercent uint64
	minFileSize := siatest.ChunkSize(uint64(renter.DefaultDataPieces), crypto.TypeDefaultRenter)

	for _, size := range fileSizes { // Calc variables here
		diskSize += size

		// Round file size to 40MiB chunks
		numChunks := uint64(size / minFileSize)
		if size%minFileSize != 0 {
			numChunks++
		}
		siaSize += numChunks * minFileSize
	}

	if diskSize != 0 {
		lostPercent = uint64(float64(siaSize)/float64(diskSize)*100) - 100
	}
	fmt.Printf(`Size on
    Disk: %v
    Sia:  %v

Lost space: %v
    +%v%% empty space used for scaling every file up to %v
`,
		filesizeUnits(diskSize),
		filesizeUnits(siaSize),
		filesizeUnits(siaSize-diskSize),
		lostPercent,
		filesizeUnits(minFileSize))

	if uploadedsizeUtilVerbose { // print only if -v or --verbose used
		fmt.Printf(`
Files: %v
    Average: %v
    Median: %v
`,
			len(fileSizes),
			filesizeUnits(calculateAverageUint64(fileSizes)),
			filesizeUnits(calculateMedianUint64(fileSizes)))
	}
}

// calculateAverageUint64 calculates the average of a uint64 slice and returns the average as a uint64
func calculateAverageUint64(input []uint64) uint64 {
	total := uint64(0)
	if len(input) == 0 {
		return 0
	}
	for _, v := range input {
		total += v
	}
	return total / uint64(len(input))
}

// calculateMedianUint64 calculates the median of a uint64 slice and returns the median as a uint64
func calculateMedianUint64(mm []uint64) uint64 {
	sort.Slice(mm, func(i, j int) bool { return mm[i] < mm[j] }) // sort the numbers

	mNumber := len(mm) / 2

	if len(mm)%2 == 0 {
		return mm[mNumber]
	}

	return (mm[mNumber-1] + mm[mNumber]) / 2
}

func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}
