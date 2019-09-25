package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

var (
	alertsCmd = &cobra.Command{
		Use:   "alerts",
		Short: "view daemon alerts",
		Long:  "view daemon alerts",
		Run:   wrap(alertscmd),
	}

	stopCmd = &cobra.Command{
		Use:   "stop",
		Short: "Stop the Sia daemon",
		Long:  "Stop the Sia daemon.",
		Run:   wrap(stopcmd),
	}

	updateCheckCmd = &cobra.Command{
		Use:   "check",
		Short: "Check for available updates",
		Long:  "Check for available updates.",
		Run:   wrap(updatecheckcmd),
	}

	globalRatelimitCmd = &cobra.Command{
		Use:   "ratelimit [maxdownloadspeed] [maxuploadspeed]",
		Short: "set the global maxdownloadspeed and maxuploadspeed",
		Long: `Set the global maxdownloadspeed and maxuploadspeed in
Bytes per second: B/s, KB/s, MB/s, GB/s, TB/s
or
Bits per second: Bps, Kbps, Mbps, Gbps, Tbps
Set them to 0 for no limit.`,
		Run: wrap(globalratelimitcmd),
	}

	updateCmd = &cobra.Command{
		Use:   "update",
		Short: "Update Sia",
		Long:  "Check for (and/or download) available updates for Sia.",
		Run:   wrap(updatecmd),
	}

	versionCmd = &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Long:  "Print version information.",
		Run:   wrap(versioncmd),
	}
)

// alertscmd prints the alerts from the daemon.
func alertscmd() {
	al, err := httpClient.DaemonAlertsGet()
	if err != nil {
		fmt.Println("Could not get daemon alerts:", err)
		return
	}
	fmt.Println("There are", len(al.Alerts), "alerts")
	alertCount := 0
	for sev := 3; sev > 0; sev-- { // print the alerts in order of critical, warning, error
		for _, a := range al.Alerts {
			if a.Severity == modules.AlertSeverity(sev) {
				if alertCount > 1000 {
					fmt.Println("Only the first 1000 alerts are displayed in siac")
					return
				}
				alertCount += 1
				fmt.Printf(`------------------
Module:   %s
Severity: %s
Message:  %s
Cause:    %s
`, a.Module, a.Severity.String(), a.Msg, a.Cause)
			}
		}
	}
}

// version prints the version of siac and siad.
func versioncmd() {
	fmt.Println("Sia Client")
	if build.ReleaseTag == "" {
		fmt.Println("\tVersion " + build.Version)
	} else {
		fmt.Println("\tVersion " + build.Version + "-" + build.ReleaseTag)
	}
	if build.GitRevision != "" {
		fmt.Println("\tGit Revision " + build.GitRevision)
		fmt.Println("\tBuild Time   " + build.BuildTime)
	}
	dvg, err := httpClient.DaemonVersionGet()
	if err != nil {
		fmt.Println("Could not get daemon version:", err)
		return
	}
	fmt.Println("Sia Daemon")
	fmt.Println("\tVersion " + dvg.Version)
	if build.GitRevision != "" {
		fmt.Println("\tGit Revision " + dvg.GitRevision)
		fmt.Println("\tBuild Time   " + dvg.BuildTime)
	}
}

// stopcmd is the handler for the command `siac stop`.
// Stops the daemon.
func stopcmd() {
	err := httpClient.DaemonStopGet()
	if err != nil {
		die("Could not stop daemon:", err)
	}
	fmt.Println("Sia daemon stopped.")
}

// updatecmd is the handler for the command `siac update`.
// Updates the daemon version to latest general release.
func updatecmd() {
	update, err := httpClient.DaemonUpdateGet()
	if err != nil {
		fmt.Println("Could not check for update:", err)
		return
	}
	if !update.Available {
		fmt.Println("Already up to date.")
		return
	}

	err = httpClient.DaemonUpdatePost()
	if err != nil {
		fmt.Println("Could not apply update:", err)
		return
	}
	fmt.Printf("Updated to version %s! Restart siad now.\n", update.Version)
}

// updatecheckcmd is the handler for the command `siac check`.
// Checks is there is an newer daemon version available.
func updatecheckcmd() {
	update, err := httpClient.DaemonUpdateGet()
	if err != nil {
		fmt.Println("Could not check for update:", err)
		return
	}
	if update.Available {
		fmt.Printf("A new release (v%s) is available! Run 'siac update' to install it.\n", update.Version)
	} else {
		fmt.Println("Up to date.")
	}
}

// globalratelimitcmd is the handler for the command `siac ratelimit`.
// Sets the global maxuploadspeed and maxdownloadspeed the daemon can use.
func globalratelimitcmd(downloadSpeedStr, uploadSpeedStr string) {
	downloadSpeedInt, err := parseRatelimit(downloadSpeedStr)
	if err != nil {
		die(errors.AddContext(err, "unable to parse download speed"))
	}
	uploadSpeedInt, err := parseRatelimit(uploadSpeedStr)
	if err != nil {
		die(errors.AddContext(err, "unable to parse upload speed"))
	}
	err = httpClient.DaemonGlobalRateLimitPost(downloadSpeedInt, uploadSpeedInt)
	if err != nil {
		die("Could not set global ratelimit speed:", err)
	}
	fmt.Println("Set global maxdownloadspeed to ", downloadSpeedInt, " and maxuploadspeed to ", uploadSpeedInt)
}
