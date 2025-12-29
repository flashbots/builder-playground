package main

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/OffchainLabs/prysm/v6/runtime/interop"
	"github.com/flashbots/builder-playground/utils/keys"
)

func main() {
	if err := generateKeys(); err != nil {
		log.Fatalf(err.Error())
	}
}

func generateKeys() error {
	priv, _, err := interop.DeterministicallyGenerateKeys(0, 100)
	if err != nil {
		return err
	}

	keysResult := []*keys.Key{}
	for i := 0; i < len(priv); i++ {
		key, err := keys.NewKey(priv[i], keys.DefaultSecret)
		if err != nil {
			return err
		}
		keysResult = append(keysResult, key)
	}

	data, err := json.Marshal(keysResult)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(data))
	return nil
}
