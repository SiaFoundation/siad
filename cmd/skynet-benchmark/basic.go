package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"strconv"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

const (
	// The SiaPath that will be used by the program to upload files for the
	// basic check.
	testSiaDirBasic = "var/skynet-benchmark-basic"
)

func basicULDL(fileSize uint64) error {
	// Create the filename.
	name := strconv.Itoa(int(fileSize) / 1e3)

	baseSiaPath, err := modules.NewSiaPath(testSiaDirBasic)
	if err != nil {
		return errors.AddContext(err, "error creating base sia path")
	}
	sp, err := baseSiaPath.Join(name + "kb")
	if err != nil {
		return errors.AddContext(err, "error creating full sia path")
	}
	// Create a file for uploading.
	buf := bytes.NewReader(fastrand.Bytes(int(fileSize)))
	// Fill out the upload parameters.
	sup := modules.SkyfileUploadParameters{
		SiaPath: sp,
		Root:    true,
		Force:   true, // This will overwrite other files in the dir.

		FileMetadata: modules.SkyfileMetadata{
			Filename: name + "kb.rand",
			Mode:     modules.DefaultFilePerm,
		},

		Reader: buf,
	}

	// Upload the file.
	fmt.Println("\t" + name + "kb Upload:")
	start := time.Now()
	skylink, _, err := c.SkynetSkyfilePost(sup)
	if err != nil {
		return errors.AddContext(err, "error uploading file")
	}
	fmt.Println("\t\t"+name+"kb Upload Time:", time.Since(start))

	// Sleep for a multiple of the total amount of time the initial upload took
	// to allow the upload to process further.
	time.Sleep(time.Since(start) * 3)
	// Sleep for 10 additional seconds to allow the upload to process further.
	time.Sleep(time.Second * 30)

	// Download the file.
	fmt.Println("\t" + name + "kb Download:")
	start = time.Now()
	reader, err := c.SkynetSkylinkReaderGet(skylink)
	if err != nil {
		return errors.AddContext(err, "error fetching reader for download")
	}
	fmt.Println("\t\t"+name+"kb Reader Time:", time.Since(start))
	data, err := ioutil.ReadAll(reader)
	if err != nil {
		return errors.AddContext(err, "error reading all data from reader")
	}
	fmt.Println("\t\t"+name+"kb Download Time:", time.Since(start))
	if len(data) != int(fileSize) {
		return errors.AddContext(err, "data is not the correct size")
	}
	return nil
}

// basicCheck will upload a small number of files and download a small number of
// files to check that Skynet is working well. basicCheck will provide
// performance numbers.
func basicCheck() {
	fmt.Println("Performing basic check.")

	// 64kb.
	err := basicULDL(64e3)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	fmt.Println()

	// 1 mb.
	//
	// Sleep for 10 seconds to clear out the preceding download.
	time.Sleep(time.Second * 10)
	err = basicULDL(1e6)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	fmt.Println()

	// 4 mb.
	//
	// Sleep for 10 seconds to clear out the preceding download.
	time.Sleep(time.Second * 10)
	err = basicULDL(4e6)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
}
