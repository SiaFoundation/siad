package main

import (
	reactrenterd "go.sia.tech/siad/v2/ui/libs/react-renterd"
	reactsiad "go.sia.tech/siad/v2/ui/libs/react-siad"
)

func main() {
	reactsiad.GenerateTypes()
	reactrenterd.GenerateTypes()
}
