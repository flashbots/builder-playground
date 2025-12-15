package utils

import (
	petname "github.com/dustinkirkland/golang-petname"
)

// GeneratePetName generates a random pet name like perfect-bee.
// These names are useful as human friendly identifiers.
func GeneratePetName() string {
	petname.NonDeterministicMode()
	return petname.Generate(2, "-")
}
