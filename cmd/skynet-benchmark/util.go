package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

func captureOutput(f func()) string {
	reader, writer, err := os.Pipe()
	if err != nil {
		panic(err)
	}
	stdout := os.Stdout
	stderr := os.Stderr
	defer func() {
		os.Stdout = stdout
		os.Stderr = stderr
		log.SetOutput(os.Stderr)
	}()
	os.Stdout = writer
	os.Stderr = writer
	log.SetOutput(writer)
	out := make(chan string)
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		var buf bytes.Buffer
		wg.Done()
		io.Copy(&buf, reader)
		out <- buf.String()
	}()
	wg.Wait()
	f()
	writer.Close()
	return <-out
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
