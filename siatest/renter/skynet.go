package renter

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"io"
	"io/ioutil"

	"gitlab.com/NebulousLabs/errors"
)

// fileMap is a helper type that maps filenames onto the raw file data
type fileMap map[string][]byte

// readTarArchive is a helper function that takes a reader containing a tar
// archive and returns an fileMap, which is a small helper struct that maps the
// filename to the data.
func readTarArchive(r io.Reader) (fileMap, error) {
	a := make(fileMap)
	tr := tar.NewReader(r)
	for {
		header, err := tr.Next()
		if errors.Contains(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		data, err := ioutil.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		a[header.Name] = data
	}
	return a, nil
}

// readZipArchive is a helper function that takes a reader containing a zip
// archive and returns an fileMap, which is a small helper struct that maps the
// filename to the data.
func readZipArchive(r io.Reader) (fileMap, error) {
	a := make(fileMap)

	// copy all data to a buffer (this is necessary because the zipreader
	// expects a `ReaderAt`)
	buff := bytes.NewBuffer([]byte{})
	size, err := io.Copy(buff, r)
	if err != nil {
		return nil, err
	}
	reader := bytes.NewReader(buff.Bytes())
	zr, err := zip.NewReader(reader, size)
	if err != nil {
		return nil, err
	}

	for _, f := range zr.File {
		data, err := func() (data []byte, err error) {
			var rc io.ReadCloser
			rc, err = f.Open()
			if err != nil {
				return
			}

			defer func() {
				err = errors.Compose(err, rc.Close())
			}()

			data, err = ioutil.ReadAll(rc)
			return
		}()

		if errors.Contains(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}

		a[f.FileHeader.Name] = data
	}
	return a, nil
}
