package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

// benchmarkFn is a helper type
type benchmarkFn func()

// captureOutput is a small helper function that takes a function as an argument
// and runs that function, meanwhile capturing the output of stdout. The output
// is then returned as a string.
func captureOutput(f benchmarkFn) string {
	stdout := os.Stdout
	stderr := os.Stderr
	defer func() {
		os.Stdout = stdout
		os.Stderr = stderr
		log.SetOutput(os.Stderr)
	}()

	// create multiwriter to capture output
	var buf bytes.Buffer
	mw := io.MultiWriter(stdout, &buf)

	// create a pipe and replace stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	os.Stderr = w
	log.SetOutput(mw)

	// create channel to control when we can return (after copying is finished)
	exit := make(chan bool)
	go func() {
		_, _ = io.Copy(mw, r)
		exit <- true
	}()

	f()
	_ = w.Close()
	<-exit
	return buf.String()
}

// uploadBenchmarkOutput is a small helper function that uploads the given
// output string to Skynet
func uploadBenchmarkOutput(output string) (string, error) {
	name := fmt.Sprintf("skynet-benchmark-%v", time.Now().Format("2021-Feb-02"))
	siaPath, err := modules.NewSiaPath(name)
	if err != nil {
		return "", err
	}

	// Fill out the upload parameters.
	sup := modules.SkyfileUploadParameters{
		SiaPath:  siaPath,
		Filename: name + ".log",
		Mode:     modules.DefaultFilePerm,
		Root:     true,
		Force:    true, // This will overwrite other files in the dir.
		Reader:   bytes.NewBufferString(output),
	}

	// Upload the file.
	skylink, _, err := c.SkynetSkyfilePost(sup)
	if err != nil {
		return "", errors.AddContext(err, "error uploading file")
	}
	return skylink, err
}
