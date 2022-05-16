package reactsiad

import (
	"os"

	"github.com/OneOfOne/struct2ts"
	"go.sia.tech/siad/v2/api/siad"
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

	converter.Add(siad.WalletBalanceResponse{})
	converter.Add(wallet.AddressInfo{})
	converter.Add(siad.WalletUTXOsResponse{})
	converter.Add(siad.TxpoolBroadcastRequest{})
	converter.Add(wallet.Transaction{})
	converter.Add(siad.SyncerPeerResponse{})
	converter.Add(siad.SyncerConnectRequest{})
	// converter.Add(siad.ConsensusTipResponse{})

	f, err := os.Create("libs/react-siad/src/types.ts")
	check(err)
	defer f.Close()
	err = converter.RenderTo(f)
	if err != nil {
		panic(err)
	}
	f.WriteString(missing)
	f.Sync()
}
