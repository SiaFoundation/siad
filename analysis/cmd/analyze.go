package main

import (
	"gitlab.com/NebulousLabs/Sia/analysis/lockcheck"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	singlechecker.Main(lockcheck.Analyzer)
}
