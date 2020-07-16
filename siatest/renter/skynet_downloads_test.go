package renter

import (
	"fmt"
	"net/http"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/errors"
)

// TestSkynetDownloads verifies the functionality of Skynet downloads.
func TestSkynetDownloads(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a testgroup.
	groupParams := siatest.GroupParams{
		Hosts:   3,
		Miners:  1,
		Renters: 1,
	}
	groupDir := renterTestDir(t.Name())

	// Specify subtests to run
	subTests := []siatest.SubTest{
		{Name: "ContentDisposition", Test: testDownloadContentDisposition},
	}

	// Run tests
	if err := siatest.RunSubTests(t, groupParams, groupDir, subTests); err != nil {
		t.Fatal(err)
	}
}

// testDownloadContentDisposition tests that downloads have the correct
// 'Content-Disposition' header set when downloading as an attachment or as an
// archive.
func testDownloadContentDisposition(t *testing.T, tg *siatest.TestGroup) {
	r := tg.Renters()[0]

	// define a helper function that validates the 'Content-Disposition' header
	verifyCDHeader := func(header http.Header, value string) error {
		actual := header.Get("Content-Disposition")
		if actual != value {
			return fmt.Errorf("Unexpected 'Content-Disposition' header, '%v' != '%v'", actual, value)
		}
		return nil
	}

	// define both values for the 'Content-Disposition' header
	name := "TestContentDisposition"
	inline := fmt.Sprintf("inline; filename=\"%v\"", name)
	attachment := fmt.Sprintf("attachment; filename=\"%v\"", name)

	params := make(map[string]string)
	var header http.Header

	// upload a single file
	skylink, _, _, err := r.UploadNewSkyfileBlocking(name, 100, false)
	if err != nil {
		t.Fatal(err)
	}

	// no params
	_, header, err = r.SkynetSkylinkHead(skylink)
	err = errors.Compose(err, verifyCDHeader(header, inline))
	if err != nil {
		t.Fatal(errors.AddContext(err, "noparams"))
	}

	// 'attachment=false'
	params["attachment"] = fmt.Sprintf("%t", false)
	err = errors.Compose(err, verifyCDHeader(header, inline))
	if err != nil {
		t.Fatal(errors.AddContext(err, "attachment=false"))
	}

	// 'attachment=true'
	params["attachment"] = fmt.Sprintf("%t", true)
	_, header, err = r.SkynetSkylinkHeadWithParameters(skylink, params)
	err = errors.Compose(err, verifyCDHeader(header, attachment))
	if err != nil {
		t.Fatal(errors.AddContext(err, "attachment=true"))
	}

	delete(params, "attachment")

	// 'format=concat'
	params["format"] = string(modules.SkyfileFormatConcat)
	_, header, err = r.SkynetSkylinkHeadWithParameters(skylink, params)
	err = errors.Compose(err, verifyCDHeader(header, inline))
	if err != nil {
		t.Fatal(errors.AddContext(err, "format=concat"))
	}

	// 'format=zip'
	params["format"] = string(modules.SkyfileFormatZip)
	_, header, err = r.SkynetSkylinkHeadWithParameters(skylink, params)
	err = errors.Compose(err, verifyCDHeader(header, attachment))
	if err != nil {
		t.Fatal(errors.AddContext(err, "format=zip"))
	}

	// 'format=tar'
	params["format"] = string(modules.SkyfileFormatTar)
	_, header, err = r.SkynetSkylinkHeadWithParameters(skylink, params)
	err = errors.Compose(err, verifyCDHeader(header, attachment))
	if err != nil {
		t.Fatal(errors.AddContext(err, "format=tar"))
	}

	// 'format=targz'
	params["format"] = string(modules.SkyfileFormatTarGz)
	_, header, err = r.SkynetSkylinkHeadWithParameters(skylink, params)
	err = errors.Compose(err, verifyCDHeader(header, attachment))
	if err != nil {
		t.Fatal(errors.AddContext(err, "format=targz"))
	}

	delete(params, "format")
}
