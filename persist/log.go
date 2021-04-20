package persist

import (
	"io"

	"gitlab.com/NebulousLabs/log"
	"go.sia.tech/siad/build"
)

// Logger is a wrapper for log.Logger.
type Logger struct {
	*log.Logger
}

var (
	// options contains log options with Sia- and build-specific information.
	options = log.Options{
		BinaryName:   build.BinaryName,
		BugReportURL: build.IssuesURL,
		Debug:        build.DEBUG,
		Release:      buildReleaseType(),
		Version:      build.NodeVersion,
	}
)

// printCommitHash logs build.GitRevision at startup.
func printCommitHash(logger *log.Logger) {
	if build.GitRevision != "" {
		logger.Printf("STARTUP: Commit hash %v", build.GitRevision)
	} else {
		logger.Println("STARTUP: Unknown commit hash")
	}
}

// NewFileLogger returns a logger that logs to logFilename. The file is opened
// in append mode, and created if it does not exist.
func NewFileLogger(logFilename string) (*Logger, error) {
	logger, err := log.NewFileLogger(logFilename, options)
	if err != nil {
		return nil, err
	}
	printCommitHash(logger)
	return &Logger{logger}, nil
}

// NewLogger returns a logger that can be closed. Calls should not be made to
// the logger after 'Close' has been called.
func NewLogger(w io.Writer) (*Logger, error) {
	logger, err := log.NewLogger(w, options)
	if err != nil {
		return nil, err
	}
	printCommitHash(logger)
	return &Logger{logger}, nil
}

// buildReleaseType returns the release type for this build, defaulting to
// Release.
func buildReleaseType() log.ReleaseType {
	switch build.Release {
	case "standard":
		return log.Release
	case "dev":
		return log.Dev
	case "testing":
		return log.Testing
	default:
		return log.Release
	}
}
