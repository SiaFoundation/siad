package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vbauerster/mpb/v5"
	"github.com/vbauerster/mpb/v5/decor"
	"go.sia.tech/siad/node/api"
	"go.sia.tech/siad/types"
)

// abs returns the absolute representation of a path.
// TODO: bad things can happen if you run siac from a non-existent directory.
// Implement some checks to catch this problem.
func abs(path string) string {
	abspath, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abspath
}

// absDuration is a small helper function that sanitizes the output for the
// given time duration. If the duration is less than 0 it will return 0,
// otherwise it will return the duration rounded to the nearest second.
func absDuration(t time.Duration) time.Duration {
	if t <= 0 {
		return 0
	}
	return t.Round(time.Second)
}

// askForConfirmation prints a question and waits for confirmation until the
// user gives a valid answer ("y", "yes", "n", "no" with any capitalization).
func askForConfirmation(s string) bool {
	r := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf("%s [y/n]: ", s)
		answer, err := r.ReadString('\n')
		if err != nil {
			log.Fatal(err)
		}
		answer = strings.ToLower(strings.TrimSpace(answer))
		if answer == "y" || answer == "yes" {
			return true
		} else if answer == "n" || answer == "no" {
			return false
		}
	}
}

// calculateAverageUint64 calculates the average of a uint64 slice and returns the average as a uint64
func calculateAverageUint64(input []uint64) uint64 {
	total := uint64(0)
	if len(input) == 0 {
		return 0
	}
	for _, v := range input {
		total += v
	}
	return total / uint64(len(input))
}

// calculateMedianUint64 calculates the median of a uint64 slice and returns the median as a uint64
func calculateMedianUint64(mm []uint64) uint64 {
	sort.Slice(mm, func(i, j int) bool { return mm[i] < mm[j] }) // sort the numbers

	mNumber := len(mm) / 2

	if len(mm)%2 == 0 {
		return mm[mNumber]
	}

	return (mm[mNumber-1] + mm[mNumber]) / 2
}

func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}

// estimatedHeightAt returns the estimated block height for the given time.
// Block height is estimated by calculating the minutes since a known block in
// the past and dividing by 10 minutes (the block time).
func estimatedHeightAt(t time.Time, cg api.ConsensusGET) types.BlockHeight {
	gt := cg.GenesisTimestamp
	bf := cg.BlockFrequency
	return types.BlockHeight(types.Timestamp(t.Unix())-gt) / bf
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

// sanitizeErr is a small helper function that sanitizes the output for the
// given error string. It will print "-", if the error string is the equivalent
// of a nil error.
func sanitizeErr(errStr string) string {
	if errStr == "" {
		return "-"
	}
	if !verbose && len(errStr) > truncateErrLength {
		errStr = errStr[:truncateErrLength] + "..."
	}
	return errStr
}

// sanitizeTime is a small helper function that sanitizes the output for the
// given time. If the given 'cond' value is false, it will print "-", if it is
// true it will print the time in a predefined format.
func sanitizeTime(t time.Time, cond bool) string {
	if !cond {
		return "-"
	}
	return fmt.Sprintf("%v", t.Format(time.RFC3339))
}
