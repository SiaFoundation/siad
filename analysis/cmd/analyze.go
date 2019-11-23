package main

import (
	"gitlab.com/NebulousLabs/Sia/analysis/lockcheck"
	"gitlab.com/NebulousLabs/Sia/analysis/returncheck"
	"golang.org/x/tools/go/analysis/multichecker"
)

func main() {
	multichecker.Main(
		lockcheck.Analyzer,
		returncheck.Analyzer,
	)
}
