package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/fastrand"
)

func main() {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	iterations := 5
	fileAmounts := []int{10, 100, 1_000, 2_000, 5_000, 10_000, 20_000}

	fmt.Fprintln(w, "# Files \t Avg. # Sectors \t Avg. % Utilization")
	fmt.Fprintln(w, "------- \t -------------- \t ------------------")

	for _, amount := range fileAmounts {
		sumSectors := float64(0)
		sumUtil := float64(0)

		for i := 0; i < iterations; i++ {
			files := randomFileMap(amount)
			placements, numSectors, err := modules.PackFiles(files)
			if err != nil {
				fmt.Errorf("Error: %v", err)
				os.Exit(1)
			}

			totalSpace := uint64(0)
			for _, p := range placements {
				totalSpace += p.Size
			}
			utilization := float64(totalSpace) / float64(numSectors*modules.SectorSize)
			sumUtil += utilization
			sumSectors += float64(numSectors)
		}

		avgSectors := sumSectors / float64(iterations)
		avgUtil := sumUtil / float64(iterations)
		fmt.Fprintln(w, amount, "\t", avgSectors, "\t", avgUtil)
	}

	w.Flush()
}

func randomFileMap(numFiles int) map[string]uint64 {
	files := make(map[string]uint64, numFiles)

	// Construct a map of random files and sizes.
	for i := 0; i < numFiles; i++ {
		name := string(fastrand.Bytes(16))
		size := fastrand.Uint64n(modules.SectorSize) + 1 // Prevent values of 0.
		files[name] = size
	}

	return files
}
