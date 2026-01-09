package main

import "github.com/flashbots/builder-playground/playground"

var version = "dev"

func main() {
	playground.Version = version
	playground.Main()
}
