package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"

	"github.com/vbauerster/mpb/v5"

	"github.com/spf13/cobra"
	"github.com/vbauerster/mpb/v5/decor"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter"
	"gitlab.com/NebulousLabs/Sia/modules/renter/filesystem"
	"gitlab.com/NebulousLabs/errors"
)

var (
	skynetCmd = &cobra.Command{
		Use:   "skynet",
		Short: "Perform actions related to Skynet",
		Long: `Perform actions related to Skynet, a file sharing and data publication platform
on top of Sia.`,
		Run: skynetcmd,
	}

	skynetBackupSkylinkCmd = &cobra.Command{
		Use:   "backup [flags] [skylink] ...",
		Short: "Backs up a skylink to the skynetSkylinkBackupDir",
		Long: `Backs up a skylink, or multiple space separated skylinks, by downloading 
the skyfile(s) to the skynetSkylinkBackupDir`,
		Run: skynetbackupskylinkcmd,
	}

	skynetBlocklistCmd = &cobra.Command{
		Use:   "blocklist",
		Short: "Add, remove, or list skylinks from the blocklist.",
		Long:  "Add, remove, or list skylinks from the blocklist.",
		Run:   skynetblocklistgetcmd,
	}

	skynetBlocklistAddCmd = &cobra.Command{
		Use:   "add [skylink] ...",
		Short: "Add skylinks to the blocklist",
		Long:  "Add space separated skylinks to the blocklist.",
		Run:   skynetblocklistaddcmd,
	}

	skynetBlocklistRemoveCmd = &cobra.Command{
		Use:   "remove [skylink] ...",
		Short: "Remove skylinks from the blocklist",
		Long:  "Remove space separated skylinks from the blocklist.",
		Run:   skynetblocklistremovecmd,
	}

	skynetConvertCmd = &cobra.Command{
		Use:   "convert [source siaPath] [destination siaPath]",
		Short: "Convert a siafile to a skyfile with a skylink.",
		Long: `Convert a siafile to a skyfile and then generate its skylink. A new skylink
	will be created in the user's skyfile directory. The skyfile and the original
	siafile are both necessary to pin the file and keep the skylink active. The
	skyfile will consume an additional 40 MiB of storage.`,
		Run: wrap(skynetconvertcmd),
	}

	skynetDownloadCmd = &cobra.Command{
		Use:   "download [skylink] [destination]",
		Short: "Download a skylink from skynet.",
		Long: `Download a file from skynet using a skylink. The download may fail unless this
node is configured as a skynet portal. Use the --portal flag to fetch a skylink
file from a chosen skynet portal.`,
		Run: skynetdownloadcmd,
	}

	skynetLsCmd = &cobra.Command{
		Use:   "ls",
		Short: "List all skyfiles that the user has pinned.",
		Long: `List all skyfiles that the user has pinned along with the corresponding
skylinks. By default, only files in var/skynet/ will be displayed. The --root
flag can be used to view skyfiles pinned in other folders.`,
		Run: skynetlscmd,
	}

	skynetPinCmd = &cobra.Command{
		Use:   "pin [skylink] [destination siapath]",
		Short: "Pin a skylink from skynet by re-uploading it yourself.",
		Long: `Pin the file associated with this skylink by re-uploading an exact copy. This
ensures that the file will still be available on skynet as long as you continue
maintaining the file in your renter.`,
		Run: wrap(skynetpincmd),
	}

	skynetPortalsCmd = &cobra.Command{
		Use:   "portals",
		Short: "Add, remove, or list registered Skynet portals.",
		Long:  "Add, remove, or list registered Skynet portals.",
		Run:   wrap(skynetportalsgetcmd),
	}

	skynetPortalsAddCmd = &cobra.Command{
		Use:   "add [url]",
		Short: "Add a Skynet portal as public or private to the persisted portals list.",
		Long: `Add a Skynet portal as public or private. Specify the url of the Skynet portal followed
by --public if you want it to be publicly available.`,
		Run: wrap(skynetportalsaddcmd),
	}

	skynetPortalsRemoveCmd = &cobra.Command{
		Use:   "remove [url]",
		Short: "Remove a Skynet portal from the persisted portals list.",
		Long:  "Remove a Skynet portal from the persisted portals list.",
		Run:   wrap(skynetportalsremovecmd),
	}

	skynetRestoreSkylinkCmd = &cobra.Command{
		Use:   "restore [backupPath] ...",
		Short: "Restores a skyfile from the backupPath",
		Long: `Restores a skyfile from the backupPath, or multiple skyfiles from multiple space 
separated backupPaths, by re-uploading the data.`,
		Run: skynetrestoreskylinkcmd,
	}

	skynetUnpinCmd = &cobra.Command{
		Use:   "unpin [siapath]",
		Short: "Unpin pinned skyfiles or directories.",
		Long: `Unpin one or more pinned skyfiles or directories at the given siapaths. The
files and directories will continue to be available on Skynet if other nodes have pinned them.`,
		Run: skynetunpincmd,
	}

	skynetUploadCmd = &cobra.Command{
		Use:   "upload [source path] [destination siapath]",
		Short: "Upload a file or a directory to Skynet.",
		Long: `Upload a file or a directory to Skynet. A skylink will be 
produced which can be shared and used to retrieve the file. If the given path is
a directory it will be uploaded as a single skylink unless the --separately flag
is passed, in which case all files under that directory will be uploaded 
individually and an individual skylink will be produced for each. All files that
get uploaded will be pinned to this Sia node, meaning that this node will pay
for storage and repairs until the files are manually deleted. Use the --dry-run 
flag to fetch the skylink without actually uploading the file.`,
		Run: skynetuploadcmd,
	}
)

// skynetcmd displays the usage info for the command.
//
// TODO: Could put some stats or summaries or something here.
func skynetcmd(cmd *cobra.Command, _ []string) {
	_ = cmd.UsageFunc()(cmd)
	os.Exit(exitCodeUsage)
}

// skynetbackupskylinkcmd backs up a skylink, or multiple space separated
// skylinks, by downloading the skyfile(s) to the skynetSkylinkBackupDir.
//
// TODO: This should be updated to accept a backup type? Or just default return
// a tar-gz
func skynetbackupskylinkcmd(cmd *cobra.Command, skylinks []string) {
	if len(skylinks) == 0 {
		_ = cmd.UsageFunc()(cmd)
		os.Exit(exitCodeUsage)
	}
	if skynetSkylinkBackupDir == "" {
		die("backup dir needs to be defined")
	}

	// NOTE: Errors will be printed out so as many skylinks as possible can be
	// backed up
	for _, skylink := range skylinks {
		// Backup the Skylink
		var backup bytes.Buffer
		err := httpClient.SkynetSkylinkBackup(skylink, &backup)
		if err != nil {
			fmt.Println("unable to backup skylink:", skylink, ",error:", err)
			continue
		}
		fmt.Printf("Backup Successful!\nSkylink: %v\nBackup Path: %v\n", skylink, skynetSkylinkBackupDir)
	}
}

// skynetblocklistaddcmd adds skylinks to the blocklist
func skynetblocklistaddcmd(cmd *cobra.Command, args []string) {
	skynetBlocklistUpdate(args, nil)
}

// skynetblocklistremovecmd removes skylinks from the blocklist
func skynetblocklistremovecmd(cmd *cobra.Command, args []string) {
	skynetBlocklistUpdate(nil, args)
}

// skynetBlocklistUpdate adds/removes trimmed skylinks to the blocklist
func skynetBlocklistUpdate(additions, removals []string) {
	additions = sanitizeSkylinks(additions)
	removals = sanitizeSkylinks(removals)

	err := httpClient.SkynetBlocklistHashPost(additions, removals, skynetBlocklistHash)
	if err != nil {
		die("Unable to update skynet blocklist:", err)
	}

	fmt.Println("Skynet Blocklist updated")
}

// skynetblocklistgetcmd will return the list of hashed merkleroots that are blocked
// from Skynet.
func skynetblocklistgetcmd(_ *cobra.Command, _ []string) {
	response, err := httpClient.SkynetBlocklistGet()
	if err != nil {
		die("Unable to get skynet blocklist:", err)
	}

	fmt.Printf("Listing %d blocked skylink(s) merkleroots:\n", len(response.Blocklist))
	for _, hash := range response.Blocklist {
		fmt.Printf("\t%s\n", hash)
	}
}

// skynetconvertcmd will convert an existing siafile to a skyfile and skylink on
// the Sia network.
func skynetconvertcmd(sourceSiaPathStr, destSiaPathStr string) {
	// Create the siapaths.
	sourceSiaPath, err := modules.NewSiaPath(sourceSiaPathStr)
	if err != nil {
		die("Could not parse source siapath:", err)
	}
	destSiaPath, err := modules.NewSiaPath(destSiaPathStr)
	if err != nil {
		die("Could not parse destination siapath:", err)
	}

	// Perform the conversion and print the result.
	sup := modules.SkyfileUploadParameters{
		SiaPath: destSiaPath,
	}
	sup = parseAndAddSkykey(sup)
	sshp, err := httpClient.SkynetConvertSiafileToSkyfilePost(sup, sourceSiaPath)
	skylink := sshp.Skylink
	if err != nil {
		die("could not convert siafile to skyfile:", err)
	}

	// Calculate the siapath that was used for the upload.
	var skypath modules.SiaPath
	if skynetUploadRoot {
		skypath = destSiaPath
	} else {
		skypath, err = modules.SkynetFolder.Join(destSiaPath.String())
		if err != nil {
			die("could not fetch skypath:", err)
		}
	}
	fmt.Printf("Skyfile uploaded successfully to %v\nSkylink: sia://%v\n", skypath, skylink)
}

// skynetdownloadcmd will perform the download of a skylink.
func skynetdownloadcmd(cmd *cobra.Command, args []string) {
	if len(args) != 2 {
		_ = cmd.UsageFunc()(cmd)
		os.Exit(exitCodeUsage)
	}

	// Open the file.
	skylink := args[0]
	skylink = strings.TrimPrefix(skylink, "sia://")
	filename := args[1]
	err := downloadSkylink(skylink, filename)
	if err != nil {
		die("unable to download skylink", err)
	}
}

// downloadSkylink downloads the skylink to the destination filename
func downloadSkylink(skylink, filename string) (err error) {
	file, err := os.Create(filename)
	if err != nil {
		return errors.AddContext(err, "unable to create destination file")
	}
	defer func() {
		err = errors.Compose(err, file.Close())
	}()

	// Check whether the portal flag is set, if so use the portal download
	// method.
	var reader io.ReadCloser
	defer func() {
		err = errors.Compose(err, reader.Close())
	}()
	if skynetDownloadPortal != "" {
		url := skynetDownloadPortal + "/" + skylink
		resp, err := http.Get(url)
		if err != nil {
			return errors.AddContext(err, "unable to download from portal")
		}
		reader = resp.Body
	} else {
		// Try to perform a download using the client package.
		reader, err = httpClient.SkynetSkylinkReaderGet(skylink)
		if err != nil {
			return errors.AddContext(err, "unable to fetch skylink")
		}
	}

	// Write data to disk
	_, err = io.Copy(file, reader)
	if err != nil {
		return errors.AddContext(err, "unable to write full data")
	}
	return nil
}

// skynetlscmd is the handler for the command `siac skynet ls`. Works very
// similar to 'siac renter ls' but defaults to the SkynetFolder and only
// displays files that are pinning skylinks.
func skynetlscmd(cmd *cobra.Command, args []string) {
	var path string
	switch len(args) {
	case 0:
		path = "."
	case 1:
		path = args[0]
	default:
		_ = cmd.UsageFunc()(cmd)
		os.Exit(exitCodeUsage)
	}
	// Parse the input siapath.
	var sp modules.SiaPath
	var err error
	if path == "." || path == "" || path == "/" {
		sp = modules.RootSiaPath()
	} else {
		sp, err = modules.NewSiaPath(path)
		if err != nil {
			die("could not parse siapath:", err)
		}
	}

	// Check whether the command is based in root or based in the skynet folder.
	if !skynetLsRoot {
		if sp.IsRoot() {
			sp = modules.SkynetFolder
		} else {
			sp, err = modules.SkynetFolder.Join(sp.String())
			if err != nil {
				die("could not build siapath:", err)
			}
		}
	}

	// Check if the command is hitting a single file.
	if !sp.IsRoot() {
		rf, err := httpClient.RenterFileRootGet(sp)
		if err == nil {
			if len(rf.File.Skylinks) == 0 {
				fmt.Println("File is not pinning any skylinks")
				return
			}
			json, err := json.MarshalIndent(rf.File, "", "  ")
			if err != nil {
				log.Fatal(err)
			}

			fmt.Println()
			fmt.Println(string(json))
			fmt.Println()
			return
		} else if !strings.Contains(err.Error(), filesystem.ErrNotExist.Error()) {
			die(fmt.Sprintf("Error getting file %v: %v", path, err))
		}
	}

	// Get the full set of files and directories.
	//
	// NOTE: Always query recursively so that we can filter out files that are
	// not tracked by Skynet and get accurate, consistent sizes for dirs when
	// displaying. If the --recursive flag was not passed, we limit the
	// directory output later.
	dirs := getDir(sp, true, true)

	// Sort the directories and the files.
	sort.Sort(byDirectoryInfo(dirs))
	for i := 0; i < len(dirs); i++ {
		sort.Sort(bySiaPathDir(dirs[i].subDirs))
		sort.Sort(bySiaPathFile(dirs[i].files))
	}

	// Keep track of the aggregate sizes for dirs as we may be adjusting them.
	sizePerDir := make(map[modules.SiaPath]uint64)
	for _, dir := range dirs {
		sizePerDir[dir.dir.SiaPath] = dir.dir.AggregateSize
	}

	// Drop any files that are not tracking skylinks.
	var numDropped uint64
	numOmittedPerDir := make(map[modules.SiaPath]int)
	for j := 0; j < len(dirs); j++ {
		for i := 0; i < len(dirs[j].files); i++ {
			file := dirs[j].files[i]
			if len(file.Skylinks) != 0 {
				continue
			}

			siaPath := dirs[j].dir.SiaPath
			numDropped++
			numOmittedPerDir[siaPath]++
			// Subtract the size from the aggregate size for the dir and all
			// parent dirs.
			for {
				sizePerDir[siaPath] -= file.Filesize
				if siaPath.IsRoot() {
					break
				}
				siaPath, err = siaPath.Dir()
				if err != nil {
					die("could not parse parent dir:", err)
				}
				if _, exists := sizePerDir[siaPath]; !exists {
					break
				}
			}
			// Remove the file.
			copy(dirs[j].files[i:], dirs[j].files[i+1:])
			dirs[j].files = dirs[j].files[len(dirs[j].files)-1:]
			i--
		}
	}

	// Get the total number of listings (subdirs and files).
	root := dirs[0] // Root directory we are querying.
	var numFilesDirs uint64
	if skynetLsRecursive {
		numFilesDirs = root.dir.AggregateNumFiles + root.dir.AggregateNumSubDirs
		numFilesDirs -= numDropped
	} else {
		numFilesDirs = root.dir.NumFiles + root.dir.NumSubDirs
		numFilesDirs -= uint64(numOmittedPerDir[root.dir.SiaPath])
	}

	// Print totals.
	totalStoredStr := modules.FilesizeUnits(sizePerDir[root.dir.SiaPath])
	fmt.Printf("\nListing %v files/dirs:\t%9s\n\n", numFilesDirs, totalStoredStr)

	// Print dirs.
	for _, dir := range dirs {
		fmt.Printf("%v/", dir.dir.SiaPath)
		if numOmitted := numOmittedPerDir[dir.dir.SiaPath]; numOmitted > 0 {
			fmt.Printf("\t(%v omitted)", numOmitted)
		}
		fmt.Println()

		// Print subdirs.
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		for _, subDir := range dir.subDirs {
			subDirName := subDir.SiaPath.Name() + "/"
			sizeUnits := modules.FilesizeUnits(sizePerDir[subDir.SiaPath])
			fmt.Fprintf(w, "  %v\t\t%9v\n", subDirName, sizeUnits)
		}

		// Print files.
		for _, file := range dir.files {
			name := file.SiaPath.Name()
			firstSkylink := file.Skylinks[0]
			size := modules.FilesizeUnits(file.Filesize)
			fmt.Fprintf(w, "  %v\t%v\t%9v\n", name, firstSkylink, size)
			for _, skylink := range file.Skylinks[1:] {
				fmt.Fprintf(w, "\t%v\t\n", skylink)
			}
		}
		if err := w.Flush(); err != nil {
			die("failed to flush writer")
		}
		fmt.Println()

		if !skynetLsRecursive {
			// If not --recursive, finish early after the first dir.
			break
		}
	}
}

// skynetPin will pin the Skyfile associated with the provided Skylink at the
// provided SiaPath
func skynetPin(skylink string, siaPath modules.SiaPath) (string, error) {
	// Check if --portal was set
	if skynetPinPortal == "" {
		spp := modules.SkyfilePinParameters{
			SiaPath: siaPath,
			Root:    skynetUploadRoot,
		}
		fmt.Println("Pinning Skyfile ...")
		return skylink, httpClient.SkynetSkylinkPinPost(skylink, spp)
	}

	// Download skyfile from the Portal
	fmt.Printf("Downloading Skyfile from %v ...", skynetPinPortal)
	url := skynetPinPortal + "/" + skylink
	resp, err := http.Get(url)
	if err != nil {
		return "", errors.AddContext(err, "unable to download from portal")
	}
	reader := resp.Body
	defer func() {
		err = reader.Close()
		if err != nil {
			die("unable to close reader:", err)
		}
	}()

	// Get the SkyfileMetadata from the Header
	var sm modules.SkyfileMetadata
	strMetadata := resp.Header.Get("Skynet-File-Metadata")
	if strMetadata != "" {
		err = json.Unmarshal([]byte(strMetadata), &sm)
		if err != nil {
			return "", errors.AddContext(err, "unable to unmarshal skyfile metadata")
		}
	}

	// Upload the skyfile to pin it to the renter node
	sup := modules.SkyfileUploadParameters{
		SiaPath:  siaPath,
		Reader:   reader,
		Filename: sm.Filename,
		Mode:     sm.Mode,
	}

	// NOTE: Since the user can define a new siapath for the Skyfile the skylink
	// returned from the upload may be different than the original skylink which
	// is why we are overwriting the skylink here.
	fmt.Println("Pinning Skyfile ...")
	skylink, _, err = httpClient.SkynetSkyfilePost(sup)
	if err != nil {
		return "", errors.AddContext(err, "unable to upload skyfile")
	}
	return skylink, nil
}

// skynetpincmd will pin the file from this skylink.
func skynetpincmd(sourceSkylink, destSiaPath string) {
	skylink := strings.TrimPrefix(sourceSkylink, "sia://")
	// Create the siapath.
	siaPath, err := modules.NewSiaPath(destSiaPath)
	if err != nil {
		die("Could not parse destination siapath:", err)
	}

	// Pin the Skyfile
	skylink, err = skynetPin(skylink, siaPath)
	if err != nil {
		die("Unable to Pin Skyfile:", err)
	}
	fmt.Printf("Skyfile pinned successfully\nSkylink: sia://%v\n", skylink)
}

// skynetrestoreskylinkcmd restores a skyfile from the backupPath, or multiple
// skyfiles from multiple space separated backupPaths, by re-uploading the data.
//
// TODO: Update the have a backup as input. Read the backup from disk
func skynetrestoreskylinkcmd(cmd *cobra.Command, backupPaths []string) {
	if len(backupPaths) == 0 {
		_ = cmd.UsageFunc()(cmd)
		os.Exit(exitCodeUsage)
	}

	// NOTE: Errors will be printed out so as many skylinks as possible can be
	// restored
	//
	// TODO: Should probably update the backup and restore commands to have
	// a RestoreAll option. The backupAll option would be to continue to call
	// backup with the same backup writer.
	for _, backupPath := range backupPaths {
		// Backup the Skylink
		var backupDst bytes.Buffer
		skylink, err := httpClient.SkynetSkylinkRestorePost(&backupDst)
		if err != nil {
			fmt.Println("unable to restore file:", backupPath, ",error:", err)
			continue
		}
		fmt.Printf("Restore Successful!\nSkylink: %v\nBackup Path: %v\n", skylink, backupPath)
	}
}

// skynetunpincmd will unpin and delete either a single or multiple files or
// directories from the Renter.
func skynetunpincmd(cmd *cobra.Command, skyPathStrs []string) {
	if len(skyPathStrs) == 0 {
		_ = cmd.UsageFunc()(cmd)
		os.Exit(exitCodeUsage)
	}

	for _, skyPathStr := range skyPathStrs {
		// Create the skypath.
		skyPath, err := modules.NewSiaPath(skyPathStr)
		if err != nil {
			die("Could not parse skypath:", err)
		}

		// Parse out the intended siapath.
		if !skynetUnpinRoot {
			skyPath, err = modules.SkynetFolder.Join(skyPath.String())
			if err != nil {
				die("could not build siapath:", err)
			}
		}

		// Try to delete file.
		//
		// In the case where the path points to a dir, this will fail and we
		// silently move on to deleting it as a dir. This is more efficient than
		// querying the renter first to see if it is a file or a dir, as that is
		// guaranteed to always be two renter calls.
		errFile := httpClient.RenterFileDeleteRootPost(skyPath)
		if errFile == nil {
			fmt.Printf("Unpinned skyfile '%v'\n", skyPath)
			continue
		} else if !(strings.Contains(errFile.Error(), filesystem.ErrNotExist.Error()) || strings.Contains(errFile.Error(), filesystem.ErrDeleteFileIsDir.Error())) {
			die(fmt.Sprintf("Failed to unpin skyfile %v: %v", skyPath, errFile))
		}
		// Try to delete dir.
		errDir := httpClient.RenterDirDeleteRootPost(skyPath)
		if errDir == nil {
			fmt.Printf("Unpinned Skynet directory '%v'\n", skyPath)
			continue
		} else if !strings.Contains(errDir.Error(), filesystem.ErrNotExist.Error()) {
			die(fmt.Sprintf("Failed to unpin Skynet directory %v: %v", skyPath, errDir))
		}

		// Unknown file/dir.
		die(fmt.Sprintf("Unknown path '%v'", skyPath))
	}
}

// skynetuploadcmd will upload a file or directory to Skynet. If --dry-run is
// passed, it will fetch the skylinks without uploading.
func skynetuploadcmd(_ *cobra.Command, args []string) {
	if len(args) == 1 {
		skynetuploadpipecmd(args[0])
		return
	}
	if len(args) != 2 {
		die("wrong number of arguments")
	}
	sourcePath, destSiaPath := args[0], args[1]
	fi, err := os.Stat(sourcePath)
	if err != nil {
		die("Unable to fetch source fileinfo:", err)
	}

	// create a new progress bar set:
	pbs := mpb.New(mpb.WithWidth(40))

	if !fi.IsDir() {
		skynetUploadFile(sourcePath, sourcePath, destSiaPath, pbs)
		if skynetUploadDryRun {
			fmt.Print("[dry run] ")
		}
		pbs.Wait()
		fmt.Printf("Successfully uploaded skyfile!\n")
		return
	}

	if skynetUploadSeparately {
		skynetUploadFilesSeparately(sourcePath, destSiaPath, pbs)
		return
	}
	skynetUploadDirectory(sourcePath, destSiaPath)
}

// skynetuploadpipecmd will upload a file or directory to Skynet. If --dry-run is
// passed, it will fetch the skylinks without uploading.
func skynetuploadpipecmd(destSiaPath string) {
	fi, err := os.Stdin.Stat()
	if err != nil {
		die(err)
	}
	if fi.Mode()&os.ModeNamedPipe == 0 {
		die("Command is meant to be used with either a pipe or src file")
	}
	// Create the siapath.
	siaPath, err := modules.NewSiaPath(destSiaPath)
	if err != nil {
		die("Could not parse destination siapath:", err)
	}
	filename := siaPath.Name()

	// create a new progress bar set:
	pbs := mpb.New(mpb.WithWidth(40))
	// Create the single bar.
	bar := pbs.AddSpinner(
		-1, // size is unknown
		mpb.SpinnerOnLeft,
		mpb.SpinnerStyle([]string{"∙∙∙", "●∙∙", "∙●∙", "∙∙●", "∙∙∙"}),
		mpb.BarFillerClearOnComplete(),
		mpb.PrependDecorators(
			decor.AverageSpeed(decor.UnitKiB, "% .1f", decor.WC{W: 4}),
			decor.Counters(decor.UnitKiB, " - %.1f / %.1f", decor.WC{W: 4}),
		),
	)
	// Create the proxy reader from stdin.
	r := bar.ProxyReader(os.Stdin)
	// Set a spinner to start after the upload is finished
	pSpinner := newProgressSpinner(pbs, bar, filename)
	// Perform the upload
	skylink := skynetUploadFileFromReader(r, filename, siaPath, modules.DefaultFilePerm)
	// Replace the spinner with the skylink and stop it
	newProgressSkylink(pbs, pSpinner, filename, skylink)
	return
}

// skynetportalsgetcmd displays the list of persisted Skynet portals
func skynetportalsgetcmd() {
	portals, err := httpClient.SkynetPortalsGet()
	if err != nil {
		die("Could not get portal list:", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	fmt.Fprintf(w, "Address\tPublic\n")
	fmt.Fprintf(w, "-------\t------\n")

	for _, portal := range portals.Portals {
		fmt.Fprintf(w, "%s\t%t\n", portal.Address, portal.Public)
	}

	if err = w.Flush(); err != nil {
		die(err)
	}
}

// skynetportalsaddcmd adds a Skynet portal as either public or private
func skynetportalsaddcmd(portalURL string) {
	addition := modules.SkynetPortal{
		Address: modules.NetAddress(portalURL),
		Public:  skynetPortalPublic,
	}

	err := httpClient.SkynetPortalsPost([]modules.SkynetPortal{addition}, nil)
	if err != nil {
		die("Could not add portal:", err)
	}
}

// skynetportalsremovecmd removes a Skynet portal
func skynetportalsremovecmd(portalUrl string) {
	removal := modules.NetAddress(portalUrl)

	err := httpClient.SkynetPortalsPost(nil, []modules.NetAddress{removal})
	if err != nil {
		die("Could not remove portal:", err)
	}
}

// skynetUploadFile uploads a file to Skynet
func skynetUploadFile(basePath, sourcePath string, destSiaPath string, pbs *mpb.Progress) {
	// Create the siapath.
	siaPath, err := modules.NewSiaPath(destSiaPath)
	if err != nil {
		die("Could not parse destination siapath:", err)
	}
	filename := filepath.Base(sourcePath)

	// Open the source.
	file, err := os.Open(sourcePath)
	if err != nil {
		die("Unable to open source path:", err)
	}
	defer func() { _ = file.Close() }()

	fi, err := file.Stat()
	if err != nil {
		die("Unable to fetch source fileinfo:", err)
	}

	if skynetUploadSilent {
		// Silently upload the file and print a simple source -> skylink
		// matching after it's done.
		skylink := skynetUploadFileFromReader(file, filename, siaPath, fi.Mode())
		fmt.Printf("%s -> %s\n", sourcePath, skylink)
		return
	}

	// Display progress bars while uploading and processing the file.
	var relPath string
	if sourcePath == basePath {
		// when uploading a single file we only display the filename
		relPath = filename
	} else {
		// when uploading multiple files we strip the common basePath
		relPath, err = filepath.Rel(basePath, sourcePath)
		if err != nil {
			die("Could not get relative path:", err)
		}
	}
	// Wrap the file reader in a progress bar reader
	pUpload, rc := newProgressReader(pbs, fi.Size(), relPath, file)
	// Set a spinner to start after the upload is finished
	pSpinner := newProgressSpinner(pbs, pUpload, relPath)
	// Perform the upload
	skylink := skynetUploadFileFromReader(rc, filename, siaPath, fi.Mode())
	// Replace the spinner with the skylink and stop it
	newProgressSkylink(pbs, pSpinner, relPath, skylink)
	return
}

// skynetUploadFilesSeparately uploads a number of files to Skynet, printing out
// separate skylink for each
func skynetUploadFilesSeparately(sourcePath, destSiaPath string, pbs *mpb.Progress) {
	// Walk the target directory and collect all files that are going to be
	// uploaded.
	filesToUpload := make([]string, 0)
	err := filepath.Walk(sourcePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			fmt.Println("Warning: skipping file:", err)
			return nil
		}
		if !info.IsDir() {
			filesToUpload = append(filesToUpload, path)
		}
		return nil
	})
	if err != nil {
		die(err)
	}

	// Confirm with the user that they want to upload all of them.
	if skynetUploadDryRun {
		fmt.Print("[dry run] ")
	}
	ok := askForConfirmation(fmt.Sprintf("Are you sure that you want to upload %d files to Skynet?", len(filesToUpload)))
	if !ok {
		os.Exit(0)
	}

	// Start the workers.
	filesChan := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < SimultaneousSkynetUploads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for filename := range filesChan {
				// get only the filename and path, relative to the original destSiaPath
				// in order to figure out where to put the file
				newDestSiaPath := filepath.Join(destSiaPath, strings.TrimPrefix(filename, sourcePath))
				skynetUploadFile(sourcePath, filename, newDestSiaPath, pbs)
			}
		}()
	}
	// Send all files for upload.
	for _, path := range filesToUpload {
		filesChan <- path
	}
	// Signal the workers that there is no more work.
	close(filesChan)
	wg.Wait()
	pbs.Wait()
	if skynetUploadDryRun {
		fmt.Print("[dry run] ")
	}
	fmt.Printf("Successfully uploaded %d skyfiles!\n", len(filesToUpload))
}

// skynetUploadDirectory uploads a directory as a single skyfile
func skynetUploadDirectory(sourcePath, destSiaPath string) {
	skyfilePath, err := modules.NewSiaPath(destSiaPath)
	if err != nil {
		fmt.Println("Failed to create siapath", destSiaPath)
		die(err)
	}
	if skynetUploadDisableDefaultPath && skynetUploadDefaultPath != "" {
		fmt.Println("Illegal combination of parameters: --defaultpath and --disabledefaultpath are mutually exclusive.")
		die()
	}
	pr, pw := io.Pipe()
	defer pr.Close()
	writer := multipart.NewWriter(pw)
	go func() {
		defer pw.Close()
		// Walk the target directory and collect all files that are going to be
		// uploaded.
		var offset uint64
		err = filepath.Walk(sourcePath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				fmt.Printf("Failed to read file %s.\n", path)
				die(err)
			}
			if info.IsDir() {
				return nil
			}
			data, err := ioutil.ReadFile(path)
			if err != nil {
				fmt.Printf("Failed to read file %s.\n", path)
				die(err)
			}
			_, err = modules.AddMultipartFile(writer, data, "files[]", info.Name(), modules.DefaultFilePerm, &offset)
			if err != nil {
				fmt.Printf("Failed to add file %s to multipart upload.\n", path)
				die(err)
			}
			return nil
		})
		if err != nil {
			die(err)
		}
		if err = writer.Close(); err != nil {
			die(err)
		}
	}()

	sup := modules.SkyfileMultipartUploadParameters{
		SiaPath:             skyfilePath,
		Force:               false,
		Root:                false,
		BaseChunkRedundancy: renter.SkyfileDefaultBaseChunkRedundancy,
		Reader:              pr,
		Filename:            skyfilePath.Name(),
		DefaultPath:         skynetUploadDefaultPath,
		DisableDefaultPath:  skynetUploadDisableDefaultPath,
		ContentType:         writer.FormDataContentType(),
	}
	skylink, _, err := httpClient.SkynetSkyfileMultiPartPost(sup)
	if err != nil {
		fmt.Println("Failed to upload directory.")
		die(err)
	}
	fmt.Println("Successfully uploaded directory:", skylink)
}

// skynetUploadFileFromReader is a helper method that uploads a file to Skynet
func skynetUploadFileFromReader(source io.Reader, filename string, siaPath modules.SiaPath, mode os.FileMode) (skylink string) {
	// Upload the file and return a skylink
	sup := modules.SkyfileUploadParameters{
		SiaPath: siaPath,
		Root:    skynetUploadRoot,

		Filename: filename,
		Mode:     mode,

		DryRun: skynetUploadDryRun,
		Reader: source,
	}
	sup = parseAndAddSkykey(sup)
	skylink, _, err := httpClient.SkynetSkyfilePost(sup)
	if err != nil {
		die("could not upload file to Skynet:", err)
	}
	return skylink
}

// newProgressSkylink creates a static progress bar that starts after `afterBar`
// and displays the skylink. The bar is stopped immediately.
func newProgressSkylink(pbs *mpb.Progress, afterBar *mpb.Bar, filename, skylink string) *mpb.Bar {
	bar := pbs.AddBar(
		1, // we'll increment it once to stop it
		mpb.BarQueueAfter(afterBar),
		mpb.PrependDecorators(
			decor.Name(pBarJobDone, decor.WC{W: 10}),
			decor.Name(skylink),
		),
		mpb.AppendDecorators(
			decor.Name(filename, decor.WC{W: len(filename) + 1, C: decor.DidentRight}),
		),
	)
	afterBar.Increment()
	bar.Increment()
	// Wait for finished bars to be rendered.
	pbs.Wait()
	return bar
}
