package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"gitlab.com/NebulousLabs/Sia/crypto"
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
		Use:   "get-id",
		Short: "Get the skykey id by its name",
		Long:  `Get the base64-encoded skykey id`,
		Run:   wrap(skykeygetidcmd),
	}
)

// skykeycmd displays the usage info for the command.
func skykeycmd(cmd *cobra.Command, args []string) {
	cmd.UsageFunc()(cmd)
	os.Exit(exitCodeUsage)
}

// skykeycreatecmd creates a new Skykey with the given name and cipher type
// as set by flag.
func skykeycreatecmd(name string) {
	skykeyStr, err := skykeyCreate(name)
	if err != nil {
		die(err)
	}
	fmt.Printf("Created new skykey: %v\n", skykeyStr)
}

func skykeyCreate(name string) (string, error) {
	var cipherType crypto.CipherType
	err := cipherType.FromString(skykeyCipherType)
	if err != nil {
		return "", errors.AddContext(err, "Could not decode cipher-type")
	}

	sk, err := httpClient.SkykeyCreateKeyPost(name, cipherType)
	if err != nil {
		return "", errors.AddContext(err, "Could not create skykey")
	}
	return sk.ToString()
}

// skykeyaddcmd adds the given skykey to the renter's skykey manager.
func skykeyaddcmd(skykeyString string) {
	err := skykeyAdd(skykeyString)
	if strings.Contains(err.Error(), skykey.ErrSkykeyNameAlreadyUsed.Error()) {
		die("Skykey name already used. Try using the --rename-as parameter with a different name.")
	}
	if err != nil {
		die(err)
	}

	fmt.Printf("Successfully added new skykey: %v\n", skykeyString)
}

func skykeyAdd(skykeyString string) error {
	var sk skykey.Skykey
	err := sk.FromString(skykeyString)
	if err != nil {
		return errors.AddContext(err, "Could not decode skykey string")
	}

	// Rename the skykey if the --rename-as flag was provided.
	if skykeyRenameAs != "" {
		sk.Name = skykeyRenameAs
	}

	err = httpClient.SkykeyAddKeyPost(sk)
	if err != nil {
		return errors.AddContext(err, "Could not add skykey")
	}

	return nil
}

// skykeygetcmd retrieves the skykey using a name or id flag.
func skykeygetcmd() {
	skykeyStr, err := skykeyGet(skykeyName, skykeyID)
	if err != nil {
		die(err)
	}

	fmt.Printf("Found skykey: %v\n", skykeyStr)
}

func skykeyGet(name, id string) (string, error) {
	if name == "" && id == "" {
		return "", errors.New("Cannot get skykey without using --name or --id flag")
	}
	if name != "" && id != "" {
		return "", errors.New("Use only one flag to get the skykey: --name or --id flag")
	}

	var sk skykey.Skykey
	var err error
	if name != "" {
		sk, err = httpClient.SkykeyGetByName(name)
	} else {
		var skykeyID skykey.SkykeyID
		err = skykeyID.FromString(id)
		if err != nil {
			return "", errors.New("Could not decode skykey ID")
		}

		sk, err = httpClient.SkykeyGetByID(skykeyID)
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
