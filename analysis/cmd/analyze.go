package main

import (
	"gitlab.com/NebulousLabs/Sia/analysis/jsontag"
	"gitlab.com/NebulousLabs/Sia/analysis/lockcheck"
	"gitlab.com/NebulousLabs/Sia/analysis/responsewritercheck"
	"golang.org/x/tools/go/analysis/multichecker"
)

func main() {
	multichecker.Main(
		lockcheck.Analyzer,
		responsewritercheck.Analyzer,
		jsontag.Analyzer,
	)
}
