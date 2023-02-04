package main

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/modules"
)

var (
	gatewayAddressCmd = &cobra.Command{
		Use:   "address",
		Short: "Print the gateway address",
		Long:  "Print the network address of the gateway.",
		Run:   wrap(gatewayaddresscmd),
	}

	gatewayBandwidthCmd = &cobra.Command{
		Use:   "bandwidth",
		Short: "returns the total upload and download bandwidth usage for the gateway",
		Long: `returns the total upload and download bandwidth usage for the gateway
and the duration of the bandwidth tracking.`,
		Run: wrap(gatewaybandwidthcmd),
	}

	gatewayCmd = &cobra.Command{
		Use:   "gateway",
		Short: "Perform gateway actions",
		Long:  "View and manage the gateway's connected peers.",
		Run:   wrap(gatewaycmd),
	}

	gatewayBlocklistCmd = &cobra.Command{
		Use:   "blocklist",
		Short: "View and manage the gateway's blocklisted peers",
		Long:  "Display and manage the peers currently on the gateway blocklist.",
		Run:   wrap(gatewayblocklistcmd),
	}

	gatewayBlocklistAppendCmd = &cobra.Command{
		Use:   "append [ip] [ip] [ip] [ip]...",
		Short: "Adds new ip address(es) to the gateway blocklist.",
		Long: `Adds new ip address(es) to the gateway blocklist.
Accepts a list of ip addresses or domain names as individual inputs.

For example: siac gateway blocklist append 123.123.123.123 111.222.111.222 mysiahost.duckdns.org`,
		Run: gatewayblocklistappendcmd,
	}

	gatewayBlocklistClearCmd = &cobra.Command{
		Use:   "clear",
		Short: "Clear the blocklisted peers list",
		Long: `Clear the blocklisted peers list.

	For example: siac gateway blocklist clear`,
		Run: gatewayblocklistclearcmd,
	}

	gatewayBlocklistRemoveCmd = &cobra.Command{
		Use:   "remove [ip] [ip] [ip] [ip]...",
		Short: "Remove ip address(es) from the gateway blocklist.",
		Long: `Remove ip address(es) from the gateway blocklist.
Accepts a list of ip addresses or domain names as individual inputs.

For example: siac gateway blocklist remove 123.123.123.123 111.222.111.222 mysiahost.duckdns.org`,
		Run: gatewayblocklistremovecmd,
	}

	gatewayBlocklistSetCmd = &cobra.Command{
		Use:   "set [ip] [ip] [ip] [ip]...",
		Short: "Set the gateway's blocklist",
		Long: `Set the gateway's blocklist.
Accepts a list of ip addresses or domain names as individual inputs.

For example: siac gateway blocklist set 123.123.123.123 111.222.111.222 mysiahost.duckdns.org`,
		Run: gatewayblocklistsetcmd,
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

// gatewaybandwidthcmd is the handler for the command `siac gateway bandwidth`.
// returns the total upload and download bandwidth usage for the gateway
func gatewaybandwidthcmd() {
	bandwidth, err := httpClient.GatewayBandwidthGet()
	if err != nil {
		die("Could not get bandwidth monitor", err)
	}

	fmt.Printf(`Download: %v 
Upload:   %v 
Duration: %v 
`, modules.FilesizeUnits(bandwidth.Download), modules.FilesizeUnits(bandwidth.Upload), fmtDuration(time.Since(bandwidth.StartTime)))
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

// gatewayblocklistcmd is the handler for the command `siac gateway blocklist`
// Prints the ip addresses on the gateway blocklist
func gatewayblocklistcmd() {
	gbg, err := httpClient.GatewayBlocklistGet()
	if err != nil {
		die("Could not get gateway blocklist", err)
	}
	fmt.Println(len(gbg.Blocklist), "ip addresses currently on the gateway blocklist")
	for _, ip := range gbg.Blocklist {
		fmt.Println(ip)
	}
}

// gatewayblocklistappendcmd is the handler for the command
// `siac gateway blocklist append`
// Adds one or more new ip addresses to the gateway's blocklist
func gatewayblocklistappendcmd(cmd *cobra.Command, addresses []string) {
	if len(addresses) == 0 {
		fmt.Println("No IP addresses submitted to append")
		_ = cmd.UsageFunc()(cmd)
		os.Exit(exitCodeUsage)
	}
	err := httpClient.GatewayAppendBlocklistPost(addresses)
	if err != nil {
		die("Could not append the ip addresses(es) to the gateway blocklist", err)
	}
	fmt.Println(addresses, "successfully added to the gateway blocklist")
}

// gatewayblocklistclearcmd is the handler for the command
// `siac gateway blocklist clear`
// Clears the gateway blocklist
func gatewayblocklistclearcmd(cmd *cobra.Command, addresses []string) {
	err := httpClient.GatewaySetBlocklistPost(addresses)
	if err != nil {
		die("Could not clear the gateway blocklist", err)
	}
	fmt.Println("successfully cleared the gateway blocklist")
}

// gatewayblocklistremovecmd is the handler for the command
// `siac gateway blocklist remove`
// Removes one or more ip addresses from the gateway's blocklist
func gatewayblocklistremovecmd(cmd *cobra.Command, addresses []string) {
	if len(addresses) == 0 {
		fmt.Println("No IP addresses submitted to remove")
		_ = cmd.UsageFunc()(cmd)
		os.Exit(exitCodeUsage)
	}
	err := httpClient.GatewayRemoveBlocklistPost(addresses)
	if err != nil {
		die("Could not remove the ip address(es) from the gateway blocklist", err)
	}
	fmt.Println(addresses, "was successfully removed from the gateway blocklist")
}

// gatewayblocklistsetcmd is the handler for the command
// `siac gateway blocklist set`
// Sets the gateway blocklist to the ip addresses passed in
func gatewayblocklistsetcmd(cmd *cobra.Command, addresses []string) {
	if len(addresses) == 0 {
		fmt.Println("No IP addresses submitted")
		_ = cmd.UsageFunc()(cmd)
		os.Exit(exitCodeUsage)
	}
	err := httpClient.GatewaySetBlocklistPost(addresses)
	if err != nil {
		die("Could not set the gateway blocklist", err)
	}
	fmt.Println(addresses, "was successfully set as the gateway blocklist")
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
	if err := w.Flush(); err != nil {
		die("failed to flush writer")
	}
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
