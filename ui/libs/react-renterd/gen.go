package reactrenterd

import (
	"os"

	"github.com/OneOfOne/struct2ts"
	"go.sia.tech/siad/v2/api/renterd"
	"go.sia.tech/siad/v2/wallet"
)

var missing = `
type Hash256 = string;
type Signature = string;
`

func check(e error) {
	if e != nil {
		panic(e)
	}
}

// GenerateTypes transforms Go structs to Typescript structs and writes them to a file.
func GenerateTypes() {
	converter := struct2ts.New(&struct2ts.Options{
		InterfaceOnly: true,
	})

	converter.Add(renterd.WalletBalanceResponse{})
	converter.Add(wallet.Transaction{})
	converter.Add(renterd.SyncerPeerResponse{})
	converter.Add(renterd.SyncerConnectRequest{})
	converter.Add(renterd.RHPScanRequest{})
	converter.Add(renterd.RHPFormRequest{})
	converter.Add(renterd.RHPFormResponse{})
	converter.Add(renterd.RHPRenewRequest{})
	converter.Add(renterd.RHPRenewResponse{})
	converter.Add(renterd.RHPReadRequest{})
	converter.Add(renterd.RHPAppendRequest{})
	converter.Add(renterd.RHPAppendResponse{})

	f, err := os.Create("libs/react-renterd/src/types.ts")
	check(err)
	defer f.Close()
	err = converter.RenderTo(f)
	if err != nil {
		panic(err)
	}
	f.WriteString(missing)
	f.Sync()
}
