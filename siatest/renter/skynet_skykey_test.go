package renter

import (
	"bytes"
	"fmt"
	"net/url"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/Sia/node/api/client"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/Sia/skykey"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestSkykey verifies the functionality of the Skykeys.
func TestSkykey(t *testing.T) {
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
		{Name: "TestAddSkykey", Test: testAddSkykey},
		{Name: "TestCreateSkykey", Test: testCreateSkykey},
		{Name: "TestDeleteSkykey", Test: testDeleteSkykey},
		{Name: "TestSkynetEncryptionPublicID", Test: testSkynetEncryptionWithType(skykey.TypePublicID)},
		{Name: "TestSkynetEncryptionPrivateID", Test: testSkynetEncryptionWithType(skykey.TypePrivateID)},
		{Name: "TestSkynetEncryptionLargeFilePublicID", Test: testSkynetEncryptionLargeFileWithType(skykey.TypePublicID)},
		{Name: "TestSkynetEncryptionLargeFilePrivateID", Test: testSkynetEncryptionLargeFileWithType(skykey.TypePrivateID)},
		{Name: "TestUnsafeClient", Test: testUnsafeClient},
	}

	// Run tests
	if err := siatest.RunSubTests(t, groupParams, groupDir, subTests); err != nil {
		t.Fatal(err)
	}
}

// testAddSkykey tests the Add functionality of the Skykey manager.
func testAddSkykey(t *testing.T, tg *siatest.TestGroup) {
	r := tg.Renters()[0]

	// The renter should be initialized with 0 skykeys.
	//
	// NOTE: This is order dependent. Since this is the first test run on the
	// renter we can test for this.
	skykeys, err := r.SkykeySkykeysGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(skykeys) != 0 {
		t.Log(skykeys)
		t.Fatal("Expected 0 skykeys")
	}

	// Create a testkey from a hard-coded skykey string.
	testSkykeyString := "skykey:AbAc7Uz4NxBrVIzR2lY-LsVs3VWsuCA0D01jxYjaHdRwrfVUuo8DutiGD7OF1B1b3P1olWPXZO1X?name=hardcodedtestkey"
	var testSkykey skykey.Skykey
	err = testSkykey.FromString(testSkykeyString)
	if err != nil {
		t.Fatal(err)
	}

	// Add the skykey
	err = r.SkykeyAddKeyPost(testSkykey)
	if err != nil {
		t.Fatal(err)
	}

	// Check that the newly added skykey shows up.
	skykeys, err = r.SkykeySkykeysGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(skykeys) != 1 {
		t.Log(skykeys)
		t.Fatal("Expected 1 skykey")
	}
	if skykeys[0].ID() != testSkykey.ID() || skykeys[0].Name != testSkykey.Name {
		t.Log(skykeys[0])
		t.Log(testSkykey)
		t.Fatal("Expected same skykey")
	}

	// Adding the same key should return an error.
	err = r.SkykeyAddKeyPost(testSkykey)
	if err == nil {
		t.Fatal("Expected error", err)
	}

	// Verify the skykey information through the API
	sk2, err := r.SkykeyGetByName(testSkykey.Name)
	if err != nil {
		t.Fatal(err)
	}
	skStr, err := testSkykey.ToString()
	if err != nil {
		t.Fatal(err)
	}
	sk2Str, err := sk2.ToString()
	if err != nil {
		t.Fatal(err)
	}
	if skStr != sk2Str {
		t.Fatal("Expected same Skykey string")
	}

	// Check byte equality and string equality.
	skID := testSkykey.ID()
	sk2ID := sk2.ID()
	if !bytes.Equal(skID[:], sk2ID[:]) {
		t.Fatal("Expected byte level equality in IDs")
	}
	if sk2.ID().ToString() != testSkykey.ID().ToString() {
		t.Fatal("Expected to get same key")
	}

	// Check the GetByID endpoint
	sk3, err := r.SkykeyGetByID(testSkykey.ID())
	if err != nil {
		t.Fatal(err)
	}
	sk3Str, err := sk3.ToString()
	if err != nil {
		t.Fatal(err)
	}
	if skStr != sk3Str {
		t.Fatal("Expected same Skykey string")
	}
}

// testCreateSkykey tests the Create functionality of the Skykey manager.
func testCreateSkykey(t *testing.T, tg *siatest.TestGroup) {
	r := tg.Renters()[0]

	// Check for any keys already with the renter
	skykeys, err := r.SkykeySkykeysGet()
	if err != nil {
		t.Fatal(err)
	}
	numInitialKeys := len(skykeys)

	// Create a new skykey using the name of the test to avoid conflicts
	sk, err := r.SkykeyCreateKeyPost(t.Name(), skykey.TypePrivateID)
	if err != nil {
		t.Fatal(err)
	}
	totalKeys := numInitialKeys + 1

	// Check that the newly created skykey shows up.
	skykeys, err = r.SkykeySkykeysGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(skykeys) != totalKeys {
		t.Log(skykeys)
		t.Fatalf("Expected %v skykeys, got %v", totalKeys, len(skykeys))
	}
	found := false
	for _, skykey := range skykeys {
		if skykey.ID() != sk.ID() && skykey.Name != sk.Name {
			found = true
			break
		}
	}
	if !found {
		siatest.PrintJSON(skykeys)
		t.Fatal("Skykey not found in skykeys")
	}

	// Creating the same key should return an error.
	_, err = r.SkykeyCreateKeyPost(t.Name(), skykey.TypePrivateID)
	if err == nil {
		t.Fatal("Expected error", err)
	}

	// Verify the skykey by getting by name
	sk2, err := r.SkykeyGetByName(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	skStr, err := sk.ToString()
	if err != nil {
		t.Fatal(err)
	}
	sk2Str, err := sk2.ToString()
	if err != nil {
		t.Fatal(err)
	}
	if skStr != sk2Str {
		t.Fatal("Expected same Skykey string")
	}

	// Check byte equality and string equality.
	skID := sk.ID()
	sk2ID := sk2.ID()
	if !bytes.Equal(skID[:], sk2ID[:]) {
		t.Fatal("Expected byte level equality in IDs")
	}
	if sk2.ID().ToString() != sk.ID().ToString() {
		t.Fatal("Expected to get same key")
	}

	// Check the GetByID endpoint
	sk3, err := r.SkykeyGetByID(sk.ID())
	if err != nil {
		t.Fatal(err)
	}
	sk3Str, err := sk3.ToString()
	if err != nil {
		t.Fatal(err)
	}
	if skStr != sk3Str {
		t.Fatal("Expected same Skykey string")
	}

	// Create a set with the strings of every skykey in the test.
	skykeySet := make(map[string]struct{})
	skykeySet[sk2Str] = struct{}{}
	for _, skykey := range skykeys {
		skykeyStr, err := skykey.ToString()
		if err != nil {
			t.Fatal(err)
		}
		skykeySet[skykeyStr] = struct{}{}
	}

	// Create a bunch of skykeys and check that they all get returned.
	nKeys := 10
	for i := 0; i < nKeys; i++ {
		nextSk, err := r.SkykeyCreateKeyPost(fmt.Sprintf(t.Name()+"-%d", i), skykey.TypePrivateID)
		if err != nil {
			t.Fatal(err)
		}
		nextSkStr, err := nextSk.ToString()
		if err != nil {
			t.Fatal(err)
		}
		skykeySet[nextSkStr] = struct{}{}
	}

	// Check that the expected number of keys was created.
	skykeys, err = r.SkykeySkykeysGet()
	if err != nil {
		t.Fatal(err)
	}
	totalKeys += nKeys
	if len(skykeys) != totalKeys {
		t.Log(len(skykeys), totalKeys)
		t.Fatal("Wrong number of keys")
	}

	// Check that getting all the keys returns all the keys we just created.
	for _, skFromList := range skykeys {
		skStrFromList, err := skFromList.ToString()
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := skykeySet[skStrFromList]; !ok {
			t.Log(skStrFromList, skykeys)
			t.Fatal("Didn't find key")
		}
	}
}

// testDeleteSkykey tests the delete functionality of the Skykey manager.
func testDeleteSkykey(t *testing.T, tg *siatest.TestGroup) {
	r := tg.Renters()[0]

	// Add any skykeys the renter already has to a map.
	skykeys, err := r.SkykeySkykeysGet()
	if err != nil {
		t.Fatal(err)
	}
	skykeySet := make(map[string]struct{})
	for _, skykey := range skykeys {
		skykeyStr, err := skykey.ToString()
		if err != nil {
			t.Fatal(err)
		}
		skykeySet[skykeyStr] = struct{}{}
	}

	// Create a bunch of skykeys and check that they all get returned.
	nKeys := 10
	for i := 0; i < nKeys; i++ {
		nextSk, err := r.SkykeyCreateKeyPost(fmt.Sprintf(t.Name()+"-%d", i), skykey.TypePrivateID)
		if err != nil {
			t.Fatal(err)
		}
		nextSkStr, err := nextSk.ToString()
		if err != nil {
			t.Fatal(err)
		}
		skykeySet[nextSkStr] = struct{}{}
	}

	// Check that the expected number of keys was created.
	skykeys, err = r.SkykeySkykeysGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(skykeys) != len(skykeySet) {
		t.Log(len(skykeys), len(skykeySet))
		t.Fatal("Wrong number of keys")
	}

	// Check that getting all the keys returns all the keys we just created.
	for _, skFromList := range skykeys {
		skStrFromList, err := skFromList.ToString()
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := skykeySet[skStrFromList]; !ok {
			t.Log(skStrFromList, skykeys)
			t.Fatal("Didn't find key")
		}
	}

	// Test deletion endpoints by deleting half of the keys.
	skykeys, err = r.SkykeySkykeysGet()
	if err != nil {
		t.Fatal(err)
	}

	deletedKeys := make(map[skykey.SkykeyID]struct{})
	nKeys = len(skykeys)
	nToDelete := nKeys / 2
	for i, sk := range skykeys {
		if i >= nToDelete {
			break
		}

		if i%2 == 0 {
			err = r.SkykeyDeleteByNamePost(sk.Name)
		} else {
			err = r.SkykeyDeleteByIDPost(sk.ID())
		}
		if err != nil {
			t.Fatal(err)
		}
		deletedKeys[sk.ID()] = struct{}{}
	}

	// Check that the skykeys were deleted.
	skykeys, err = r.SkykeySkykeysGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(skykeys) != nKeys-nToDelete {
		t.Fatalf("Expected %d keys, got %d", nKeys-nToDelete, len(skykeys))
	}

	// Sanity check: Make sure deleted keys are not still around.
	for _, sk := range skykeys {
		if _, ok := deletedKeys[sk.ID()]; ok {
			t.Fatal("Found a key that should have been deleted")
		}
	}
}

// testUnsafeClient tests the Skykey manager functionality using an unsafe
// client.
func testUnsafeClient(t *testing.T, tg *siatest.TestGroup) {
	r := tg.Renters()[0]

	// Check to make sure the renter has at least 1 skykey.
	skykeys, err := r.SkykeySkykeysGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(skykeys) == 0 {
		// Add a skykey
		_, err = r.SkykeyCreateKeyPost(persist.RandomSuffix(), skykey.TypePrivateID)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Create a set with the strings of every skykey in the test.
	skykeySet := make(map[string]struct{})

	// Check that the expected number of keys was created.
	skykeys, err = r.SkykeySkykeysGet()
	if err != nil {
		t.Fatal(err)
	}
	for _, skykey := range skykeys {
		skykeyStr, err := skykey.ToString()
		if err != nil {
			t.Fatal(err)
		}
		skykeySet[skykeyStr] = struct{}{}
	}

	// Test misuse of the /skynet/skykey endpoint using an UnsafeClient.
	uc := client.NewUnsafeClient(r.Client)

	// Passing in 0 params shouild return an error.
	baseQuery := "/skynet/skykey"
	var skykeyGet api.SkykeyGET
	err = uc.Get(baseQuery, &skykeyGet)
	if err == nil {
		t.Fatal("Expected an error")
	}

	// Passing in 2 params shouild return an error.
	sk := skykeys[0]
	skID := sk.ID()
	values := url.Values{}
	values.Set("name", "testkey1")
	values.Set("id", skID.ToString())
	err = uc.Get(fmt.Sprintf("%s?%s", baseQuery, values.Encode()), &skykeyGet)
	if err == nil {
		t.Fatal("Expected an error")
	}

	// Sanity check: uc.Get should return the same value as the safe client
	// method.
	values = url.Values{}
	values.Set("name", sk.Name)
	err = uc.Get(fmt.Sprintf("%s?%s", baseQuery, values.Encode()), &skykeyGet)
	if err != nil {
		t.Fatal(err)
	}
	skStr, err := sk.ToString()
	if err != nil {
		t.Fatal(err)
	}
	if skykeyGet.Skykey != skStr {
		t.Fatal("Expected same result from  unsafe client")
	}

	// Use the unsafe client to check the Name and ID parameters are set in the
	// GET response.
	values = url.Values{}
	values.Set("name", sk.Name)
	getQuery := fmt.Sprintf("/skynet/skykey?%s", values.Encode())

	skykeyGet = api.SkykeyGET{}
	err = uc.Get(getQuery, &skykeyGet)
	if err != nil {
		t.Fatal(err)
	}
	if skykeyGet.Name != sk.Name {
		t.Log(skykeyGet)
		t.Fatal("Wrong skykey name")
	}
	if skykeyGet.ID != sk.ID().ToString() {
		t.Log(skykeyGet)
		t.Fatal("Wrong skykey ID")
	}
	if skykeyGet.Skykey != skStr {
		t.Log(skykeyGet)
		t.Fatal("Wrong skykey string")
	}

	// Check the Name and ID params from the /skynet/skykeys GET response.
	var skykeysGet api.SkykeysGET
	err = uc.Get("/skynet/skykeys", &skykeysGet)
	if err != nil {
		t.Fatal(err)
	}
	if len(skykeysGet.Skykeys) != len(skykeySet) {
		t.Fatalf("Got %d skykeys, expected %d", len(skykeysGet.Skykeys), len(skykeySet))
	}
	for _, skGet := range skykeysGet.Skykeys {
		if _, ok := skykeySet[skGet.Skykey]; !ok {
			t.Fatal("skykey not in test set")
		}

		var nextSk skykey.Skykey
		err = nextSk.FromString(skGet.Skykey)
		if err != nil {
			t.Fatal(err)
		}
		if nextSk.Name != skGet.Name {
			t.Fatal("Wrong skykey name")
		}
		if nextSk.ID().ToString() != skGet.ID {
			t.Fatal("Wrong skykey id")
		}
		if nextSk.Type.ToString() != skGet.Type {
			t.Fatal("Wrong skykey type")
		}
	}
}

// testSkynetEncryptionWithType returns the encryption test with the given
// skykeyType set.
func testSkynetEncryptionWithType(skykeyType skykey.SkykeyType) func(t *testing.T, tg *siatest.TestGroup) {
	return func(t *testing.T, tg *siatest.TestGroup) {
		testSkynetEncryption(t, tg, skykeyType)
	}
}

// testSkynetEncryptionLargeFileWithType returns the large-file encryption test with the given
// skykeyType.
func testSkynetEncryptionLargeFileWithType(skykeyType skykey.SkykeyType) func(t *testing.T, tg *siatest.TestGroup) {
	return func(t *testing.T, tg *siatest.TestGroup) {
		testSkynetEncryptionLargeFile(t, tg, skykeyType)
	}
}

// testSkynetEncryption tests the uploading and pinning of small skyfiles using
// encryption with the given skykeyType.
func testSkynetEncryption(t *testing.T, tg *siatest.TestGroup, skykeyType skykey.SkykeyType) {
	r := tg.Renters()[0]
	encKeyName := "encryption-test-key-" + skykeyType.ToString()

	// Create some data to upload as a skyfile.
	data := fastrand.Bytes(100 + siatest.Fuzz())
	// Need it to be a reader.
	reader := bytes.NewReader(data)
	// Call the upload skyfile client call.
	filename := "testEncryptSmall-" + skykeyType.ToString()
	uploadSiaPath, err := modules.NewSiaPath(filename)
	if err != nil {
		t.Fatal(err)
	}
	sup := modules.SkyfileUploadParameters{
		SiaPath:             uploadSiaPath,
		Force:               false,
		Root:                false,
		BaseChunkRedundancy: 2,
		FileMetadata: modules.SkyfileMetadata{
			Filename: filename,
			Mode:     0640, // Intentionally does not match any defaults.
		},
		Reader:     reader,
		SkykeyName: encKeyName,
	}

	_, _, err = r.SkynetSkyfilePost(sup)
	if err == nil {
		t.Fatal("Expected error for using unknown key")
	}

	// Try again after adding a key.
	// Note we must create a new reader in the params!
	sup.Reader = bytes.NewReader(data)

	_, err = r.SkykeyCreateKeyPost(encKeyName, skykeyType)
	if err != nil {
		t.Fatal(err)
	}

	skylink, sfMeta, err := r.SkynetSkyfilePost(sup)
	if err != nil {
		t.Fatal(err)
	}

	// Try to download the file behind the skylink.
	fetchedData, metadata, err := r.SkynetSkylinkGet(sfMeta.Skylink)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fetchedData, data) {
		t.Error("upload and download doesn't match")
		t.Log(data)
		t.Log(fetchedData)
	}
	if metadata.Mode != 0640 {
		t.Error("bad mode")
	}
	if metadata.Filename != filename {
		t.Error("bad filename")
	}

	if sfMeta.Skylink != skylink {
		t.Log(sfMeta.Skylink)
		t.Log(skylink)
		t.Fatal("Expected metadata skylink to match returned skylink")
	}

	// Pin the encrypted Skyfile.
	pinSiaPath, err := modules.NewSiaPath("testSmallEncryptedPinPath" + skykeyType.ToString())
	if err != nil {
		t.Fatal(err)
	}
	pinLUP := modules.SkyfilePinParameters{
		SiaPath:             pinSiaPath,
		Force:               false,
		Root:                false,
		BaseChunkRedundancy: 3,
	}
	err = r.SkynetSkylinkPinPost(skylink, pinLUP)
	if err != nil {
		t.Fatal(err)
	}

	// See if the file is present.
	fullPinSiaPath, err := modules.SkynetFolder.Join(pinSiaPath.String())
	if err != nil {
		t.Fatal(err)
	}
	pinnedFile, err := r.RenterFileRootGet(fullPinSiaPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(pinnedFile.File.Skylinks) != 1 {
		t.Fatal("expecting 1 skylink")
	}
	if pinnedFile.File.Skylinks[0] != skylink {
		t.Fatal("skylink mismatch")
	}
}

// testSkynetEncryption tests the uploading and pinning of large skyfiles using
// encryption.
func testSkynetEncryptionLargeFile(t *testing.T, tg *siatest.TestGroup, skykeyType skykey.SkykeyType) {
	r := tg.Renters()[0]
	encKeyName := "large-file-encryption-test-key-" + skykeyType.ToString()

	// Create some data to upload as a skyfile.
	data := fastrand.Bytes(5 * int(modules.SectorSize))
	// Need it to be a reader.
	reader := bytes.NewReader(data)
	// Call the upload skyfile client call.
	filename := "testEncryptLarge-" + skykeyType.ToString()
	uploadSiaPath, err := modules.NewSiaPath(filename)
	if err != nil {
		t.Fatal(err)
	}
	sup := modules.SkyfileUploadParameters{
		SiaPath:             uploadSiaPath,
		Force:               false,
		Root:                false,
		BaseChunkRedundancy: 2,
		FileMetadata: modules.SkyfileMetadata{
			Filename: filename,
			Mode:     0640, // Intentionally does not match any defaults.
		},
		Reader:     reader,
		SkykeyName: encKeyName,
	}

	_, err = r.SkykeyCreateKeyPost(encKeyName, skykeyType)
	if err != nil {
		t.Fatal(err)
	}

	skylink, sfMeta, err := r.SkynetSkyfilePost(sup)
	if err != nil {
		t.Fatal(err)
	}

	if sfMeta.Skylink != skylink {
		t.Log(sfMeta.Skylink)
		t.Log(skylink)
		t.Fatal("Expected metadata skylink to match returned skylink")
	}

	// Try to download the file behind the skylink.
	fetchedData, metadata, err := r.SkynetSkylinkGet(sfMeta.Skylink)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fetchedData, data) {
		t.Error("upload and download doesn't match")
		t.Log(data)
		t.Log(fetchedData)
	}
	if metadata.Mode != 0640 {
		t.Error("bad mode")
	}
	if metadata.Filename != filename {
		t.Error("bad filename")
	}

	// Pin the encrypted Skyfile.
	pinSiaPath, err := modules.NewSiaPath("testEncryptedPinPath" + skykeyType.ToString())
	if err != nil {
		t.Fatal(err)
	}
	pinLUP := modules.SkyfilePinParameters{
		SiaPath:             pinSiaPath,
		Force:               false,
		Root:                false,
		BaseChunkRedundancy: 2,
	}
	err = r.SkynetSkylinkPinPost(skylink, pinLUP)
	if err != nil {
		t.Fatal(err)
	}

	// See if the file is present.
	fullPinSiaPath, err := modules.SkynetFolder.Join(pinSiaPath.String())
	if err != nil {
		t.Fatal(err)
	}
	pinnedFile, err := r.RenterFileRootGet(fullPinSiaPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(pinnedFile.File.Skylinks) != 1 {
		t.Fatal("expecting 1 skylink")
	}
	if pinnedFile.File.Skylinks[0] != skylink {
		t.Fatal("skylink mismatch")
	}
}
