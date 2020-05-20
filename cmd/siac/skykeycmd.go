package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/node/api/client"
	"gitlab.com/NebulousLabs/Sia/skykey"
	"gitlab.com/NebulousLabs/errors"
)

var (
	skykeyCmd = &cobra.Command{
		Use:   "skykey",
		Short: "Perform actions related to Skykeys",
		Long:  `Perform actions related to Skykeys, the encryption keys used for Skyfiles.`,
		Run:   skykeycmd,
	}

	skykeyCreateCmd = &cobra.Command{
		Use:   "create [name]",
		Short: "Create a skykey with the given name.",
		Long: `Create a skykey  with the given name. The --cipher-type flag can be
		used to specify the cipher type. Its default is XChaCha20.`,
		Run: wrap(skykeycreatecmd),
	}

	skykeyAddCmd = &cobra.Command{
		Use:   "add [skykey base64-encoded skykey]",
		Short: "Add a base64-encoded skykey to the key manager.",
		Long:  `Add a base64-encoded skykey to the key manager.`,
		Run:   wrap(skykeyaddcmd),
	}

	skykeyGetCmd = &cobra.Command{
		Use:   "get",
		Short: "Get the skykey by its name or id",
		Long:  `Get the base64-encoded skykey using either its name with --name or id with --id`,
		Run:   wrap(skykeygetcmd),
	}

	skykeyGetIDCmd = &cobra.Command{
		Use:   "get-id [name]",
		Short: "Get the skykey id by its name",
		Long:  `Get the base64-encoded skykey id by its name`,
		Run:   wrap(skykeygetidcmd),
	}

	skykeyListCmd = &cobra.Command{
		Use:   "ls",
		Short: "List all skykeys",
		Long:  "List all skykeys. Use with --show-priv-keys to show full encoding with private key also.",
		Run:   wrap(skykeylistcmd),
	}
)

// skykeycmd displays the usage info for the command.
func skykeycmd(cmd *cobra.Command, args []string) {
	cmd.UsageFunc()(cmd)
	os.Exit(exitCodeUsage)
}

// skykeycreatecmd is a wrapper for skykeyCreate used to handle skykey creation.
func skykeycreatecmd(name string) {
	skykeyStr, err := skykeyCreate(httpClient, name)
	if err != nil {
		die(errors.AddContext(err, "Failed to create new skykey"))
	}
	fmt.Printf("Created new skykey: %v\n", skykeyStr)
}

// skykeyCreate creates a new Skykey with the given name and cipher type
// as set by flag.
func skykeyCreate(c client.Client, name string) (string, error) {
	var cipherType crypto.CipherType
	err := cipherType.FromString(skykeyCipherType)
	if err != nil {
		return "", errors.AddContext(err, "Could not decode cipher-type")
	}

	sk, err := c.SkykeyCreateKeyPost(name, cipherType)
	if err != nil {
		return "", errors.AddContext(err, "Could not create skykey")
	}
	return sk.ToString()
}

// skykeyaddcmd is a wrapper for skykeyAdd used to handle the addition of new skykeys.
func skykeyaddcmd(skykeyString string) {
	err := skykeyAdd(httpClient, skykeyString)
	if err != nil && strings.Contains(err.Error(), skykey.ErrSkykeyWithNameAlreadyExists.Error()) {
		die("Skykey name already used. Try using the --rename-as parameter with a different name.")
	}
	if err != nil {
		die(errors.AddContext(err, "Failed to add skykey"))
	}

	fmt.Printf("Successfully added new skykey: %v\n", skykeyString)
}

// skykeyAdd adds the given skykey to the renter's skykey manager.
func skykeyAdd(c client.Client, skykeyString string) error {
	var sk skykey.Skykey
	err := sk.FromString(skykeyString)
	if err != nil {
		return errors.AddContext(err, "Could not decode skykey string")
	}

	// Rename the skykey if the --rename-as flag was provided.
	if skykeyRenameAs != "" {
		sk.Name = skykeyRenameAs
	}

	err = c.SkykeyAddKeyPost(sk)
	if err != nil {
		return errors.AddContext(err, "Could not add skykey")
	}

	return nil
}

// skykeygetcmd is a wrapper for skykeyGet that handles skykey get commands.
func skykeygetcmd() {
	skykeyStr, err := skykeyGet(httpClient, skykeyName, skykeyID)
	if err != nil {
		die(err)
	}

	fmt.Printf("Found skykey: %v\n", skykeyStr)
}

// skykeyGet retrieves the skykey using a name or id flag.
func skykeyGet(c client.Client, name, id string) (string, error) {
	if name == "" && id == "" {
		return "", errors.New("Cannot get skykey without using --name or --id flag")
	}
	if name != "" && id != "" {
		return "", errors.New("Use only one flag to get the skykey: --name or --id flag")
	}

	var sk skykey.Skykey
	var err error
	if name != "" {
		sk, err = c.SkykeyGetByName(name)
	} else {
		var skykeyID skykey.SkykeyID
		err = skykeyID.FromString(id)
		if err != nil {
			return "", errors.AddContext(err, "Could not decode skykey ID")
		}

		sk, err = c.SkykeyGetByID(skykeyID)
	}

	if err != nil {
		return "", errors.AddContext(err, "Failed to retrieve skykey")
	}

	return sk.ToString()
}

// skykeygetidcmd retrieves the skykey id using its name.
func skykeygetidcmd(skykeyName string) {
	sk, err := httpClient.SkykeyGetByName(skykeyName)
	if err != nil {
		die("Failed to retrieve skykey:", err)
	}
	fmt.Printf("Found skykey ID: %v\n", sk.ID().ToString())
}

// skykeylistcmd is a wrapper for skykeyListKeys that prints a list of all
// skykeys.
func skykeylistcmd() {
	skykeysString, err := skykeyListKeys(httpClient, skykeyShowPrivateKeys)
	if err != nil {
		die("Failed to get all skykeys:", err)
	}
	fmt.Print(skykeysString)
}

// skykeyListKeys returns a formatted string containing a list of all skykeys
// being stored by the renter. It includes IDs, Names, and if showPrivateKeys is
// set to true it will include the full encoded skykey.
func skykeyListKeys(c client.Client, showPrivateKeys bool) (string, error) {
	skykeys, err := c.SkykeyGetSkykeys()
	if err != nil {
		return "", err
	}

	var b strings.Builder

	// Define a function that adds a string to the builder and pads it with a
	// certain number of spaces. This gets us nice columns for printing.
	addAndPadRight := func(s string, maxLen int) {
		b.WriteString(s)
		for i := len(s); i < maxLen; i++ {
			b.WriteString(" ")
		}
	}

	// Get the max name length for padding purposes.
	maxNameLength := 0
	for _, sk := range skykeys {
		if len(sk.Name) > maxNameLength {
			maxNameLength = len(sk.Name)
		}
	}

	// Print a title row.
	maxIDStringLen := 24
	addAndPadRight("ID", maxIDStringLen+2)
	addAndPadRight("Name", maxNameLength+2)
	if showPrivateKeys {
		b.WriteString("Full Skykey")
	}
	b.WriteString("\n")

	titleLen := b.Len() - 1
	for i := 0; i < titleLen; i++ {
		b.WriteString("-")
	}
	b.WriteString("\n")

	for _, sk := range skykeys {
		idStr := sk.ID().ToString()
		addAndPadRight(idStr, maxIDStringLen+2)
		addAndPadRight(sk.Name, maxNameLength+2)

		if showPrivateKeys {
			skStr, err := sk.ToString()
			if err != nil {
				return "", err
			}
			b.WriteString(skStr)
		}
		b.WriteString("\n")
	}

	return b.String(), nil
}
