package renter

import (
	"compress/gzip"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/filesystem"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
)

// v137Persistence is the persistence struct of a renter that doesn't use the
// new SiaFile format yet.
type v137Persistence struct {
	MaxDownloadSpeed int64
	MaxUploadSpeed   int64
	StreamCacheSize  uint64
	Tracking         map[string]v137TrackedFile
}

// v137TrackedFile is the tracking information stored about a file on a legacy
// renter.
type v137TrackedFile struct {
	RepairPath string
}

// The v1.3.7 in-memory file format.
//
// A file is a single file that has been uploaded to the network. Files are
// split into equal-length chunks, which are then erasure-coded into pieces.
// Each piece is separately encrypted, using a key derived from the file's
// master key. The pieces are uploaded to hosts in groups, such that one file
// contract covers many pieces.
type file struct {
	name        string
	size        uint64 // Static - can be accessed without lock.
	contracts   map[types.FileContractID]fileContract
	masterKey   [crypto.EntropySize]byte // Static - can be accessed without lock.
	erasureCode modules.ErasureCoder     // Static - can be accessed without lock.
	pieceSize   uint64                   // Static - can be accessed without lock.
	mode        uint32                   // actually an os.FileMode
	deleted     bool                     // indicates if the file has been deleted.

	staticUID string // A UID assigned to the file when it gets created.

	mu sync.RWMutex
}

// The v1.3.7 in-memory format for a contract used by the v1.3.7 file format.
//
// A fileContract is a contract covering an arbitrary number of file pieces.
// Chunk/Piece metadata is used to split the raw contract data appropriately.
type fileContract struct {
	ID     types.FileContractID
	IP     modules.NetAddress
	Pieces []pieceData

	WindowStart types.BlockHeight
}

// The v1.3.7 in-memory format for a piece used by the v1.3.7 file format.
//
// pieceData contains the metadata necessary to request a piece from a
// fetcher.
//
// TODO: Add an 'Unavailable' flag that can be set if the host loses the piece.
// Some TODOs exist in 'repair.go' related to this field.
type pieceData struct {
	Chunk      uint64      // which chunk the piece belongs to
	Piece      uint64      // the index of the piece in the chunk
	MerkleRoot crypto.Hash // the Merkle root of the piece
}

// numChunks returns the number of chunks that f was split into.
func (f *file) numChunks() uint64 {
	// empty files still need at least one chunk
	if f.size == 0 {
		return 1
	}
	n := f.size / f.staticChunkSize()
	// last chunk will be padded, unless chunkSize divides file evenly.
	if f.size%f.staticChunkSize() != 0 {
		n++
	}
	return n
}

// staticChunkSize returns the size of one chunk.
func (f *file) staticChunkSize() uint64 {
	return f.pieceSize * uint64(f.erasureCode.MinPieces())
}

// MarshalSia implements the encoding.SiaMarshaller interface, writing the
// file data to w.
func (f *file) MarshalSia(w io.Writer) error {
	enc := encoding.NewEncoder(w)

	// encode easy fields
	err := enc.EncodeAll(
		f.name,
		f.size,
		f.masterKey,
		f.pieceSize,
		f.mode,
	)
	if err != nil {
		return err
	}
	// COMPATv0.4.3 - encode the bytesUploaded and chunksUploaded fields
	// TODO: the resulting .sia file may confuse old clients.
	err = enc.EncodeAll(f.pieceSize*f.numChunks()*uint64(f.erasureCode.NumPieces()), f.numChunks())
	if err != nil {
		return err
	}

	// encode erasureCode
	switch code := f.erasureCode.(type) {
	case *siafile.RSCode:
		err = enc.EncodeAll(
			"Reed-Solomon",
			uint64(code.MinPieces()),
			uint64(code.NumPieces()-code.MinPieces()),
		)
		if err != nil {
			return err
		}
	default:
		if build.DEBUG {
			panic("unknown erasure code")
		}
		return errors.New("unknown erasure code")
	}
	// encode contracts
	if err := enc.Encode(uint64(len(f.contracts))); err != nil {
		return err
	}
	for _, c := range f.contracts {
		if err := enc.Encode(c); err != nil {
			return err
		}
	}
	return nil
}

// UnmarshalSia implements the encoding.SiaUnmarshaler interface,
// reconstructing a file from the encoded bytes read from r.
func (f *file) UnmarshalSia(r io.Reader) error {
	dec := encoding.NewDecoder(r, 100e6)

	// COMPATv0.4.3 - decode bytesUploaded and chunksUploaded into dummy vars.
	var bytesUploaded, chunksUploaded uint64

	// Decode easy fields.
	err := dec.DecodeAll(
		&f.name,
		&f.size,
		&f.masterKey,
		&f.pieceSize,
		&f.mode,
		&bytesUploaded,
		&chunksUploaded,
	)
	if err != nil {
		return err
	}
	f.staticUID = persist.RandomSuffix()

	// Decode erasure coder.
	var codeType string
	if err := dec.Decode(&codeType); err != nil {
		return err
	}
	switch codeType {
	case "Reed-Solomon":
		var nData, nParity uint64
		err = dec.DecodeAll(
			&nData,
			&nParity,
		)
		if err != nil {
			return err
		}
		rsc, err := siafile.NewRSCode(int(nData), int(nParity))
		if err != nil {
			return err
		}
		f.erasureCode = rsc
	default:
		return errors.New("unrecognized erasure code type: " + codeType)
	}

	// Decode contracts.
	var nContracts uint64
	if err := dec.Decode(&nContracts); err != nil {
		return err
	}
	f.contracts = make(map[types.FileContractID]fileContract)
	var contract fileContract
	for i := uint64(0); i < nContracts; i++ {
		if err := dec.Decode(&contract); err != nil {
			return err
		}
		f.contracts[contract.ID] = contract
	}
	return nil
}

// loadSiaFiles walks through the directory searching for siafiles and loading
// them into memory.
func (r *Renter) compatV137ConvertSiaFiles(tracking map[string]v137TrackedFile, oldContracts []modules.RenterContract) error {
	// Recursively convert all files found in renter directory.
	err := filepath.Walk(r.persistDir, func(path string, info os.FileInfo, err error) error {
		// This error is non-nil if filepath.Walk couldn't stat a file or
		// folder.
		if err != nil {
			r.log.Println("WARN: could not stat file or folder during walk:", err)
			return nil
		}

		// Skip folders and non-sia files.
		if info.IsDir() || filepath.Ext(path) != modules.SiaFileExtension {
			return nil
		}

		// Check if file was already converted.
		_, err = siafile.LoadSiaFile(path, r.wal)
		if err == nil {
			return nil
		}

		// Open the file.
		file, err := os.Open(path)
		if err != nil {
			return errors.AddContext(err, "unable to open file for conversion"+path)
		}

		// Load the file contents into the renter.
		_, err = r.compatV137loadSiaFilesFromReader(file, tracking, oldContracts)
		if err != nil {
			err = errors.AddContext(err, "unable to load v137 siafiles from reader")
			return errors.Compose(err, file.Close())
		}

		// Close the file and delete it since it was converted.
		if err := file.Close(); err != nil {
			return err
		}
		return os.Remove(path)
	})
	if err != nil {
		return err
	}
	// Cleanup folders in the renter subdir.
	fis, err := ioutil.ReadDir(r.persistDir)
	if err != nil {
		return err
	}
	for _, fi := range fis {
		// Ignore files.
		if !fi.IsDir() {
			continue
		}
		// Skip siafiles and contracts folders.
		if fi.Name() == modules.FileSystemRoot || fi.Name() == "contracts" {
			continue
		}
		// Delete the folder.
		if err := os.RemoveAll(filepath.Join(r.persistDir, fi.Name())); err != nil {
			return err
		}
	}
	return nil
}

// v137FileToSiaFile converts a legacy file to a SiaFile. Fields that can't be
// populated using the legacy file remain blank.
func (r *Renter) v137FileToSiaFile(f *file, repairPath string, oldContracts []modules.RenterContract) (*filesystem.FileNode, error) {
	// Create a mapping of contract ids to host keys.
	contracts := r.hostContractor.Contracts()
	idToPk := make(map[types.FileContractID]types.SiaPublicKey)
	for _, c := range contracts {
		idToPk[c.ID] = c.HostPublicKey
	}
	// Add old contracts to the mapping too.
	for _, c := range oldContracts {
		idToPk[c.ID] = c.HostPublicKey
	}

	fileData := siafile.FileData{
		Name:        f.name,
		FileSize:    f.size,
		MasterKey:   f.masterKey,
		ErasureCode: f.erasureCode,
		RepairPath:  repairPath,
		PieceSize:   f.pieceSize,
		Mode:        os.FileMode(f.mode),
		Deleted:     f.deleted,
		UID:         siafile.SiafileUID(f.staticUID),
	}
	chunks := make([]siafile.FileChunk, f.numChunks())
	for i := 0; i < len(chunks); i++ {
		chunks[i].Pieces = make([][]siafile.Piece, f.erasureCode.NumPieces())
	}
	for _, contract := range f.contracts {
		pk, exists := idToPk[contract.ID]
		if !exists {
			r.log.Printf("Couldn't find pubKey for contract %v with WindowStart %v",
				contract.ID, contract.WindowStart)
			continue
		}

		for _, piece := range contract.Pieces {
			// Make sure we don't add the same piece on the same host multiple
			// times.
			duplicate := false
			for _, p := range chunks[piece.Chunk].Pieces[piece.Piece] {
				if p.HostPubKey.Equals(pk) {
					duplicate = true
					break
				}
			}
			if duplicate {
				continue
			}
			chunks[piece.Chunk].Pieces[piece.Piece] = append(chunks[piece.Chunk].Pieces[piece.Piece], siafile.Piece{
				HostPubKey: pk,
				MerkleRoot: piece.MerkleRoot,
			})
		}
	}
	fileData.Chunks = chunks
	return r.staticFileSystem.NewSiaFileFromLegacyData(fileData)
}

// compatV137LoadSiaFilesFromReader reads .sia data from reader and registers
// the contained files in the renter. It returns the nicknames of the loaded
// files.
func (r *Renter) compatV137loadSiaFilesFromReader(reader io.Reader, tracking map[string]v137TrackedFile, oldContracts []modules.RenterContract) ([]string, error) {
	// read header
	var header [15]byte
	var version string
	var numFiles uint64
	err := encoding.NewDecoder(reader, encoding.DefaultAllocLimit).DecodeAll(
		&header,
		&version,
		&numFiles,
	)
	if err != nil {
		return nil, errors.AddContext(err, "unable to read header")
	} else if header != shareHeader {
		return nil, ErrBadFile
	} else if version != shareVersion {
		return nil, ErrIncompatible
	}

	// Create decompressor.
	unzip, err := gzip.NewReader(reader)
	if err != nil {
		return nil, errors.AddContext(err, "unable to create gzip decompressor")
	}
	dec := encoding.NewDecoder(unzip, 100e6)

	// Read each file.
	files := make([]*file, numFiles)
	for i := range files {
		files[i] = new(file)
		err := dec.Decode(files[i])
		if err != nil {
			return nil, errors.AddContext(err, "unable to decode file")
		}

		// Make sure the file's name does not conflict with existing files.
		dupCount := 0
		origName := files[i].name
		for {
			siaPath, err := modules.NewSiaPath(files[i].name)
			if err != nil {
				return nil, err
			}
			exists, _ := r.staticFileSystem.FileExists(siaPath)
			if !exists {
				break
			}
			dupCount++
			files[i].name = origName + "_" + strconv.Itoa(dupCount)
		}
	}

	// Add files to renter.
	names := make([]string, numFiles)
	for i, f := range files {
		// Figure out the repair path.
		var repairPath string
		tf, ok := tracking[f.name]
		if ok {
			repairPath = tf.RepairPath
		}
		// v137FileToSiaFile adds siafile to the SiaFileSet so it does not need to
		// be returned here
		entry, err := r.v137FileToSiaFile(f, repairPath, oldContracts)
		if err != nil {
			return nil, errors.AddContext(err, "unable to transform old file to new file")
		}
		names[i] = f.name
		entry.Close()
	}
	return names, err
}

// convertPersistVersionFrom140To142 upgrades a legacy persist file to the next
// version, converting the old filesystem to the new one.
func (r *Renter) convertPersistVersionFrom140To142(path string) error {
	metadata := persist.Metadata{
		Header:  settingsMetadata.Header,
		Version: persistVersion140,
	}
	var p persistence
	err := persist.LoadJSON(metadata, &p, path)
	if err != nil {
		return errors.AddContext(err, "could not load json")
	}
	// Rename siafiles folder to fs/home/user and snapshots to fs/snapshots.
	fsRoot := filepath.Join(r.persistDir, modules.FileSystemRoot)
	newHomePath := modules.HomeSiaPath().SiaDirSysPath(fsRoot)
	newSiaFilesPath := modules.UserSiaPath().SiaDirSysPath(fsRoot)
	newSnapshotsPath := modules.SnapshotsSiaPath().SiaDirSysPath(fsRoot)
	if err := os.MkdirAll(newHomePath, 0700); err != nil {
		return errors.AddContext(err, "failed to create new home dir")
	}
	if err := os.Rename(filepath.Join(r.persistDir, "siafiles"), newSiaFilesPath); err != nil && !os.IsNotExist(err) {
		return errors.AddContext(err, "failed to rename legacy siafiles folder")
	}
	if err := os.Rename(filepath.Join(r.persistDir, "snapshots"), newSnapshotsPath); err != nil && !os.IsNotExist(err) {
		return errors.AddContext(err, "failed to rename legacy snapshots dir")
	}
	// Save metadata with updated version
	metadata.Version = persistVersion142
	err = persist.SaveJSON(metadata, p, path)
	if err != nil {
		return errors.AddContext(err, "could not save json")
	}
	return nil
}

// convertPersistVersionFrom133To140 upgrades a legacy persist file to the next
// version, converting legacy SiaFiles in the process.
func (r *Renter) convertPersistVersionFrom133To140(path string, oldContracts []modules.RenterContract) error {
	metadata := persist.Metadata{
		Header:  settingsMetadata.Header,
		Version: persistVersion133,
	}
	p := v137Persistence{
		Tracking: make(map[string]v137TrackedFile),
	}

	err := persist.LoadJSON(metadata, &p, path)
	if err != nil {
		return errors.AddContext(err, "could not load json")
	}
	metadata.Version = persistVersion140
	// Load potential legacy SiaFiles.
	if err := r.compatV137ConvertSiaFiles(p.Tracking, oldContracts); err != nil {
		return errors.AddContext(err, "conversion from v137 failed")
	}
	err = persist.SaveJSON(metadata, p, path)
	if err != nil {
		return errors.AddContext(err, "could not save json")
	}
	return nil
}

// convertPersistVersionFrom040to133 upgrades a legacy persist file to the next
// version, adding new fields with their default values.
func convertPersistVersionFrom040To133(path string) error {
	metadata := persist.Metadata{
		Header:  settingsMetadata.Header,
		Version: persistVersion040,
	}
	p := persistence{}

	err := persist.LoadJSON(metadata, &p, path)
	if err != nil {
		return err
	}
	metadata.Version = persistVersion133
	p.MaxDownloadSpeed = DefaultMaxDownloadSpeed
	p.MaxUploadSpeed = DefaultMaxUploadSpeed
	return persist.SaveJSON(metadata, p, path)
}
