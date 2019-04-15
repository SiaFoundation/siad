package main

import (
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"gitlab.com/NebulousLabs/Sia/modules"
)

var (
	gatewayAddressCmd = &cobra.Command{
		Use:   "address",
		Short: "Print the gateway address",
		Long:  "Print the network address of the gateway.",
		Run:   wrap(gatewayaddresscmd),
	}

	gatewayCmd = &cobra.Command{
		Use:   "gateway",
		Short: "Perform gateway actions",
		Long:  "View and manage the gateway's connected peers.",
		Run:   wrap(gatewaycmd),
	}

	gatewayConnectCmd = &cobra.Command{
		Use:   "connect [address]",
		Short: "Connect to a peer",
		Long:  "Connect to a peer and add it to the node list.",
		Run:   wrap(gatewayconnectcmd),
	}

	gatewayDisconnectCmd = &cobra.Command{
		Use:   "disconnect [address]",
		Short: "Disconnect from a peer",
		Long:  "Disconnect from a peer. Does not remove the peer from the node list.",
		Run:   wrap(gatewaydisconnectcmd),
	}

	gatewayListCmd = &cobra.Command{
		Use:   "list",
		Short: "View a list of peers",
		Long:  "View the current peer list.",
		Run:   wrap(gatewaylistcmd),
	}

	gatewayRatelimitCmd = &cobra.Command{
		Use:   "ratelimit [maxdownloadspeed] [maxuploadspeed]",
		Short: "set maxdownloadspeed and maxuploadspeed",
		Long: `Set the maxdownloadspeed and maxuploadspeed in bytes-per-second
(B/s).  Set them to 0 for no limit.`,
		Run: wrap(gatewayratelimitcmd),
	}
)

// gatewayconnectcmd is the handler for the command `siac gateway add [address]`.
// Adds a new peer to the peer list.
func gatewayconnectcmd(addr string) {
	err := httpClient.GatewayConnectPost(modules.NetAddress(addr))
	if err != nil {
		die("Could not add peer:", err)
	}
	fmt.Println("Added", addr, "to peer list.")
}

// gatewaydisconnectcmd is the handler for the command `siac gateway remove [address]`.
// Removes a peer from the peer list.
func gatewaydisconnectcmd(addr string) {
	err := httpClient.GatewayDisconnectPost(modules.NetAddress(addr))
	if err != nil {
		die("Could not remove peer:", err)
	}
	fmt.Println("Removed", addr, "from peer list.")
}

// gatewayaddresscmd is the handler for the command `siac gateway address`.
// Prints the gateway's network address.
func gatewayaddresscmd() {
	info, err := httpClient.GatewayGet()
	if err != nil {
		die("Could not get gateway address:", err)
	}
	fmt.Println("Address:", info.NetAddress)
}

// gatewaycmd is the handler for the command `siac gateway`.
// Prints the gateway's network address and number of peers.
func gatewaycmd() {
	info, err := httpClient.GatewayGet()
	if err != nil {
		die("Could not get gateway address:", err)
	}
	fmt.Println("Address:", info.NetAddress)
	fmt.Println("Active peers:", len(info.Peers))
	fmt.Println("Max download speed:", info.MaxDownloadSpeed)
	fmt.Println("Max upload speed:", info.MaxUploadSpeed)
}

// gatewaylistcmd is the handler for the command `siac gateway list`.
// Prints a list of all peers.
func gatewaylistcmd() {
	info, err := httpClient.GatewayGet()
	if err != nil {
		die("Could not get peer list:", err)
	}
	if len(info.Peers) == 0 {
		fmt.Println("No peers to show.")
		return
	}
	fmt.Println(len(info.Peers), "active peers:")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "Version\tOutbound\tAddress")
	for _, peer := range info.Peers {
		fmt.Fprintf(w, "%v\t%v\t%v\n", peer.Version, yesNo(!peer.Inbound), peer.NetAddress)
	}
	w.Flush()
}

// gatewayratelimitcmd is the handler for the command `siac gateway ratelimit`.
// sets the maximum upload & download bandwidth the gateway module is permitted
// to use.
func gatewayratelimitcmd(downloadSpeedStr, uploadSpeedStr string) {
	downloadSpeedInt, err := strconv.ParseInt(downloadSpeedStr, 10, 64)
	if err != nil {
		die("Could not parse downloadspeed")
	}
	uploadSpeedInt, err := strconv.ParseInt(uploadSpeedStr, 10, 64)
	if err != nil {
		die("Could not parse uploadspeed")
	}

	err = httpClient.GatewayRateLimitPost(downloadSpeedInt, uploadSpeedInt)
	if err != nil {
		die("Could not set gateway ratelimit speed")
	}
	fmt.Println("Set gateway maxdownloadspeed to ", downloadSpeedInt, " and maxuploadspeed to ", uploadSpeedInt)
}
