package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
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

	gatewayBlacklistCmd = &cobra.Command{
		Use:   "blacklist",
		Short: "View and manage the gateway's blacklisted peers",
		Long:  "Display and manage the peers currently on the gateway blacklist.",
		Run:   wrap(gatewayblacklistcmd),
	}

	gatewayBlacklistAppendCmd = &cobra.Command{
		Use:   "append [ip] [ip] [ip] [ip] ...",
		Short: "Append a new address to the blacklisted peers list",
		Long: `Add a new address to the list of blacklisted peers.
Accepts a list of ipaddresses or domain names as individual inputs.

For example: siac gateway blacklist append 123.123.123.123 111.222.111.222 mysiahost.duckdns.org`,
		Run: wrap(gatewayblacklistappendcmd),
	}

	gatewayBlacklistRemoveCmd = &cobra.Command{
		Use:   "remove [ip] [ip] [ip] [ip] ...",
		Short: "Remove a peer from the list of blacklisted peers",
		Long: `Remove one or more peers from the list of blacklisted peers.
Accepts a list of ipaddresses or domain names as individual inputs.

For example: siac gateway blacklist remove 123.123.123.123 111.222.111.222 mysiahost.duckdns.org`,
		Run: wrap(gatewayblacklistremovecmd),
	}

	gatewayBlacklistSetCmd = &cobra.Command{
		Use:   "set [ip] [ip] [ip] [ip] ...",
		Short: "Set the blacklisted peers list",
		Long: `Set the blacklisted peers list.
Accepts a list of ipaddresses or domain names as individual inputs.

For example: siac gateway blacklist set 123.123.123.123 111.222.111.222 mysiahost.duckdns.org`,
		Run: wrap(gatewayblacklistsetcmd),
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
		Long: `Set the maxdownloadspeed and maxuploadspeed in 
Bytes per second: B/s, KB/s, MB/s, GB/s, TB/s
or
Bits per second: Bps, Kbps, Mbps, Gbps, Tbps
Set them to 0 for no limit.`,
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

// gatewayblacklistcmd is the handler for the command `siac gateway blacklist`
// Prints the hosts on the gateway blacklist
func gatewayblacklistcmd() {
	gbg, err := httpClient.GatewayBlacklistGet()
	if err != nil {
		die("Could not get gateway blacklist", err)
	}
	fmt.Println(len(gbg.Blacklist), "peers currently on the gateway blacklist")
	for _, ip := range gbg.Blacklist {
		fmt.Println(ip)
	}

}

// gatewayblacklistappendcmd is the handler for the command
// `siac gateway blacklist append`
// Adds one or more new hosts to the gateway's blacklist
func gatewayblacklistappendcmd(addresses []string) {
	err := httpClient.GatewayAppendBlacklistPost(addresses)
	if err != nil {
		die("Could not append the peer to the gateway blacklist", err)
	}
	fmt.Println(addresses, "successfully added to the gateway blacklist")
}

// gatewayblacklistremovecmd is the handler for the command
// `siac gateway blacklist remove`
// Removes one or more hosts from the gateway's blacklist
func gatewayblacklistremovecmd(addresses []string) {
	err := httpClient.GatewayRemoveBlacklistPost(addresses)
	if err != nil {
		die("Could not remove the peer from the gateway blacklist", err)
	}
	fmt.Println(addresses, "was successfully removed from the gateway blacklist")
}

// gatewayblacklistsetcmd is the handler for the command
// `siac gateway blacklist set`
// Sets the gateway blacklist to the hosts passed in via a comma-separated list
func gatewayblacklistsetcmd(addresses []string) {
	err := httpClient.GatewaySetBlacklistPost(addresses)
	if err != nil {
		die("Could not set the gateway blacklist", err)
	}
	fmt.Println(addresses, "was successfully set as the gateway blacklist")
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
	downloadSpeedInt, err := parseRatelimit(downloadSpeedStr)
	if err != nil {
		die(errors.AddContext(err, "unable to parse download speed"))
	}
	uploadSpeedInt, err := parseRatelimit(uploadSpeedStr)
	if err != nil {
		die(errors.AddContext(err, "unable to parse upload speed"))
	}

	err = httpClient.GatewayRateLimitPost(downloadSpeedInt, uploadSpeedInt)
	if err != nil {
		die("Could not set gateway ratelimit speed")
	}
	fmt.Println("Set gateway maxdownloadspeed to ", downloadSpeedInt, " and maxuploadspeed to ", uploadSpeedInt)
}
