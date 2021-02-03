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

func captureOutput(f func()) string {
	stdout := os.Stdout
	stderr := os.Stderr
	defer func() {
		os.Stdout = stdout
		os.Stderr = stderr
		log.SetOutput(os.Stderr)
	}()

	var buf bytes.Buffer
	mw := io.MultiWriter(stdout, &buf)

	// get pipe reader and writer | writes to pipe writer come out pipe reader
	r, w, _ := os.Pipe()

	// replace stdout,stderr with pipe writer | all writes to stdout, stderr will go through pipe instead (fmt.print, log)
	os.Stdout = w
	os.Stderr = w
	log.SetOutput(mw)

	//create channel to control exit | will block until all copies are finished
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

func uploadOutput(output string) (string, error) {
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
