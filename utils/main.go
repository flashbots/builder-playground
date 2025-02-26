package main

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/ethereum/go-ethereum/core/types"
)

func main() {
	data, err := os.ReadFile("state.json")
	if err != nil {
		log.Fatal(err)
	}

	var state struct {
		L1StateDump string `json:"l1StateDump"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		log.Fatal(err)
	}

	decoded, err := base64.StdEncoding.DecodeString(state.L1StateDump)
	if err != nil {
		log.Fatal(err)
	}

	// Create gzip reader from the base64 decoded data
	gr, err := gzip.NewReader(bytes.NewReader(decoded))
	if err != nil {
		log.Fatal(err)
	}
	defer gr.Close()

	// Read and decode the contents
	contents, err := io.ReadAll(gr)
	if err != nil {
		log.Fatal(err)
	}

	var alloc types.GenesisAlloc
	if err := json.Unmarshal(contents, &alloc); err != nil {
		log.Fatal(err)
	}

	fmt.Println(alloc)
}
