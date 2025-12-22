package keys

import (
	"encoding/hex"
	"encoding/json"
	"fmt"

	_ "embed"

	"github.com/OffchainLabs/prysm/v6/crypto/bls"
	"github.com/hashicorp/go-uuid"
	keystorev4 "github.com/wealdtech/go-eth2-wallet-encryptor-keystorev4"
)

var DefaultSecret = "secret"

//go:embed fixtures/bls_keys.json
var pregeneratesBLSKeys []byte

type Key struct {
	Priv     bls.SecretKey
	Pub      bls.PublicKey
	Keystore []byte
}

func (k *Key) MarshalJSON() ([]byte, error) {
	type keyJSON struct {
		Priv     string `json:"priv"`
		Pub      string `json:"pub"`
		Keystore string `json:"keystore"`
	}

	return json.Marshal(&keyJSON{
		Priv:     fmt.Sprintf("%x", k.Priv.Marshal()),
		Pub:      fmt.Sprintf("%x", k.Pub.Marshal()),
		Keystore: string(k.Keystore),
	})
}

func (k *Key) UnmarshalJSON(data []byte) error {
	type keyJSON struct {
		Priv     string `json:"priv"`
		Pub      string `json:"pub"`
		Keystore string `json:"keystore"`
	}

	var kj keyJSON
	if err := json.Unmarshal(data, &kj); err != nil {
		return err
	}

	privBytes, err := hex.DecodeString(kj.Priv)
	if err != nil {
		return err
	}
	if k.Priv, err = bls.SecretKeyFromBytes(privBytes); err != nil {
		return err
	}

	pubBytes, err := hex.DecodeString(kj.Pub)
	if err != nil {
		return err
	}
	if k.Pub, err = bls.PublicKeyFromBytes(pubBytes); err != nil {
		return err
	}

	k.Keystore = []byte(kj.Keystore)
	return nil
}

func GetPregeneratedBLSKeys() ([]*Key, error) {
	var keys []*Key
	if err := json.Unmarshal(pregeneratesBLSKeys, &keys); err != nil {
		return nil, err
	}
	return keys, nil
}

func GenerateKeystore(key bls.SecretKey, secret string) (map[string]interface{}, error) {
	encryptor := keystorev4.New()
	cryptoFields, err := encryptor.Encrypt(key.Marshal(), secret)
	if err != nil {
		return nil, err
	}

	id, _ := uuid.GenerateUUID()

	pubKeyHex := "0x" + hex.EncodeToString(key.PublicKey().Marshal())
	item := map[string]interface{}{
		"crypto":      cryptoFields,
		"uuid":        id,
		"pubkey":      pubKeyHex[2:], // without 0x in the json file
		"version":     4,
		"description": "",
	}

	return item, nil
}
