package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/vbauerster/mpb/v5"
	"github.com/vbauerster/mpb/v5/decor"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/filesystem"
)

var (
	skynetCmd = &cobra.Command{
		Use:   "skynet",
		Short: "Perform actions related to Skynet",
		Long: `Perform actions related to Skynet, a file sharing and data publication platform
on top of Sia.`,
		Run: skynetcmd,
	}

	skynetBlacklistCmd = &cobra.Command{
		Use:   "blacklist [skylink]",
		Short: "Blacklist a skylink from skynet.",
		Long: `Blacklist a skylink from skynet. Use the --remove flag to
remove a skylink from the blacklist.`,
		Run: skynetblacklistcmd,
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
		Long: `Upload a file or a directory to Skynet. A skylink will be produced which can be
shared and used to retrieve the file. If the given path is a directory all files under that directory will
be uploaded individually and an individual skylink will be produced for each. All files that get uploaded
will be pinned to this Sia node, meaning that this node will pay for storage and repairs until the files 
are manually deleted. Use the --dry-run flag to fetch the skylink without actually uploading the file.`,
		Run: wrap(skynetuploadcmd),
	}
)

// skynetcmd displays the usage info for the command.
//
// TODO: Could put some stats or summaries or something here.
func skynetcmd(cmd *cobra.Command, args []string) {
	cmd.UsageFunc()(cmd)
	os.Exit(exitCodeUsage)
}

// skynetblacklistcmd handles adding and removing a skylink from the Skynet
// Blacklist
func skynetblacklistcmd(cmd *cobra.Command, args []string) {
	if len(args) != 1 {
		cmd.UsageFunc()(cmd)
		os.Exit(exitCodeUsage)
	}

	// Get the skylink
	skylink := args[0]
	skylink = strings.TrimPrefix(skylink, "sia://")

	// Check if this is an addition or removal
	var add, remove []string
	if skynetBlacklistRemove {
		remove = append(remove, skylink)
	} else {
		add = append(add, skylink)
	}

	// Try to update the Skynet Blacklist.
	err := httpClient.SkynetBlacklistPost(add, remove)
	if err != nil {
		die("Unable to update skynet blacklist:", err)
	}
	fmt.Println("Skynet Blacklist updated")
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
	skylink, err := httpClient.SkynetConvertSiafileToSkyfilePost(sup, sourceSiaPath)
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
		cmd.UsageFunc()(cmd)
		os.Exit(exitCodeUsage)
	}

	// Open the file.
	skylink := args[0]
	skylink = strings.TrimPrefix(skylink, "sia://")
	filename := args[1]
	file, err := os.Create(filename)
	if err != nil {
		die("Unable to create destination file:", err)
	}
	defer file.Close()

	// Check whether the portal flag is set, if so use the portal download
	// method.
	var reader io.ReadCloser
	if skynetDownloadPortal != "" {
		url := skynetDownloadPortal + "/" + skylink
		resp, err := http.Get(url)
		if err != nil {
			die("Unable to download from portal:", err)
		}
		reader = resp.Body
		defer reader.Close()
	} else {
		// Try to perform a download using the client package.
		reader, err = httpClient.SkynetSkylinkReaderGet(skylink)
		if err != nil {
			die("Unable to fetch skylink:", err)
		}
		defer reader.Close()
	}

	_, err = io.Copy(file, reader)
	if err != nil {
		die("Unable to write full data:", err)
	}
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
		cmd.UsageFunc()(cmd)
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
	dirs := getDir(sp, true, skynetLsRecursive)
	// Drop any files that are not tracking skylinks.
	for j := 0; j < len(dirs); j++ {
		for i := 0; i < len(dirs[j].files); i++ {
			if len(dirs[j].files[i].Skylinks) == 0 {
				dirs[j].files = append(dirs[j].files[:i], dirs[j].files[i+1:]...)
				i--
			}
		}
	}

	// Produce the full information for these files.
	numFiles := 0
	var totalStored uint64
	for _, dir := range dirs {
		for _, file := range dir.files {
			totalStored += file.Filesize
		}
		numFiles += len(dir.files)
	}
	if numFiles+len(dirs) < 1 {
		fmt.Println("No files/dirs have been uploaded.")
		return
	}
	fmt.Printf("\nListing %v files/dirs:", numFiles+len(dirs)-1)
	fmt.Printf(" %9s\n", modules.FilesizeUnits(totalStored))
	sort.Sort(byDirectoryInfo(dirs))
	// Print dirs.
	for _, dir := range dirs {
		fmt.Printf("%v/\n", dir.dir.SiaPath)
		// Print subdirs.
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		sort.Sort(bySiaPathDir(dir.subDirs))
		for _, subDir := range dir.subDirs {
			subDirName := subDir.SiaPath.Name() + "/"
			size := modules.FilesizeUnits(subDir.AggregateSize)
			fmt.Fprintf(w, "  %v\t\t%9v\n", subDirName, size)
		}

		// Print files.
		sort.Sort(bySiaPathFile(dir.files))
		for _, file := range dir.files {
			name := file.SiaPath.Name()
			firstSkylink := file.Skylinks[0]
			size := modules.FilesizeUnits(file.Filesize)
			fmt.Fprintf(w, "  %v\t%v\t%9v\n", name, firstSkylink, size)
			for _, skylink := range file.Skylinks[1:] {
				fmt.Fprintf(w, "\t%v\t\n", skylink)
			}
		}
		w.Flush()
		fmt.Println()
	}
}

// skynetpincmd will pin the file from this skylink.
func skynetpincmd(sourceSkylink, destSiaPath string) {
	skylink := strings.TrimPrefix(sourceSkylink, "sia://")
	// Create the siapath.
	siaPath, err := modules.NewSiaPath(destSiaPath)
	if err != nil {
		die("Could not parse destination siapath:", err)
	}

	spp := modules.SkyfilePinParameters{
		SiaPath: siaPath,
		Root:    skynetUploadRoot,
	}

	err = httpClient.SkynetSkylinkPinPost(skylink, spp)
	if err != nil {
		die("could not pin file to Skynet:", err)
	}

	fmt.Printf("Skyfile pinned successfully \nSkylink: sia://%v\n", skylink)
}

// skynetunpincmd will unpin and delete either a single or multiple files or
// directories from the Renter.
func skynetunpincmd(cmd *cobra.Command, skyPathStrs []string) {
	if len(skyPathStrs) == 0 {
		cmd.UsageFunc()(cmd)
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
func skynetuploadcmd(sourcePath, destSiaPath string) {
	// Open the source file.
	file, err := os.Open(sourcePath)
	if err != nil {
		die("Unable to open source path:", err)
	}
	defer file.Close()
	fi, err := file.Stat()
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
		fmt.Printf("Successfully uploaded skyfile!\n")
		return
	}

	// Walk the target directory and collect all files that are going to be
	// uploaded.
	filesToUpload := make([]string, 0)
	err = filepath.Walk(sourcePath, func(path string, info os.FileInfo, err error) error {
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

// skynetUploadFile uploads a file to Skynet
func skynetUploadFile(basePath, sourcePath string, destSiaPath string, pbs *mpb.Progress) (skylink string) {
	// Create the siapath.
	siaPath, err := modules.NewSiaPath(destSiaPath)
	if err != nil {
		die("Could not parse destination siapath:", err)
	}
	filename := filepath.Base(sourcePath)

	// Open the source file.
	file, err := os.Open(sourcePath)
	if err != nil {
		die("Unable to open source path:", err)
	}
	defer file.Close()
	fi, err := file.Stat()
	if err != nil {
		die("Unable to fetch source fileinfo:", err)
	}

	if skynetUploadSilent {
		// Silently upload the file and print a simple source -> skylink
		// matching after it's done.
		skylink = skynetUploadFileFromReader(file, filename, siaPath, fi.Mode())
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
	rc := io.ReadCloser(file)
	// Wrap the file reader in a progress bar reader
	pUpload, rc := newProgressReader(pbs, fi.Size(), relPath, rc)
	// Set a spinner to start after the upload is finished
	pSpinner := newProgressSpinner(pbs, pUpload, relPath)
	// Perform the upload
	skylink = skynetUploadFileFromReader(rc, filename, siaPath, fi.Mode())
	// Replace the spinner with the skylink and stop it
	newProgressSkylink(pbs, pSpinner, relPath, skylink)
	return
}

// skynetUploadFileFromReader is a helper method that uploads a file to Skynet
func skynetUploadFileFromReader(source io.Reader, filename string, siaPath modules.SiaPath, mode os.FileMode) (skylink string) {
	// Upload the file and return a skylink
	sup := modules.SkyfileUploadParameters{
		SiaPath: siaPath,
		Root:    skynetUploadRoot,

		DryRun: skynetUploadDryRun,

		FileMetadata: modules.SkyfileMetadata{
			Filename: filename,
			Mode:     mode,
		},

		Reader: source,
	}
	skylink, _, err := httpClient.SkynetSkyfilePost(sup)
	if err != nil {
		die("could not upload file to Skynet:", err)
	}
	return skylink
}

// newProgressReader is a helper method for adding a new progress bar to an
// existing *mpb.Progress object.
func newProgressReader(pbs *mpb.Progress, size int64, filename string, file io.Reader) (*mpb.Bar, io.ReadCloser) {
	bar := pbs.AddBar(
		size,
		mpb.PrependDecorators(
			decor.Name(pBarJobUpload, decor.WC{W: 10}),
			decor.Percentage(decor.WC{W: 6}),
		),
		mpb.AppendDecorators(
			decor.Name(filename, decor.WC{W: len(filename) + 1, C: decor.DidentRight}),
		),
	)
	return bar, bar.ProxyReader(file)
}

// newProgressSpinner creates a spinner that is queued after `afterBar` is
// complete.
func newProgressSpinner(pbs *mpb.Progress, afterBar *mpb.Bar, filename string) *mpb.Bar {
	return pbs.AddSpinner(
		1,
		mpb.SpinnerOnMiddle,
		mpb.SpinnerStyle([]string{"∙∙∙", "●∙∙", "∙●∙", "∙∙●", "∙∙∙"}),
		mpb.BarQueueAfter(afterBar),
		mpb.BarFillerClearOnComplete(),
		mpb.PrependDecorators(
			decor.OnComplete(decor.Name(pBarJobProcess, decor.WC{W: 10}), pBarJobDone),
			decor.Name("", decor.WC{W: 6, C: decor.DidentRight}),
		),
		mpb.AppendDecorators(
			decor.Name(filename, decor.WC{W: len(filename) + 1, C: decor.DidentRight}),
		),
	)
}

// newProgressSkylink creates a static progress bar that starts after `afterBar`
// and displays the skylink. The bar is stopped immediately.
func newProgressSkylink(pbs *mpb.Progress, afterBar *mpb.Bar, filename, skylink string) *mpb.Bar {
	bar := pbs.AddBar(
		1, // we'll increment it once to stop it
		mpb.BarQueueAfter(afterBar),
		mpb.BarFillerClearOnComplete(),
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
	return bar
}
