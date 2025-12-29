package keys

import (
	"encoding/json"
	"testing"

	"github.com/OffchainLabs/prysm/v6/crypto/bls"
	"github.com/stretchr/testify/require"
	keystorev4 "github.com/wealdtech/go-eth2-wallet-encryptor-keystorev4"
)

func TestKeystoreEncoding(t *testing.T) {
	blsKey, err := bls.RandKey()
	require.NoError(t, err)

	key, err := NewKey(blsKey, DefaultSecret)
	require.NoError(t, err)

	res, err := decodeKeystore(key, DefaultSecret)
	require.NoError(t, err)
	require.Equal(t, res, key.Priv.Marshal())

	// Try to marshal/unmarhsal it and it should still decode properly
	keyMarshal, err := key.MarshalJSON()
	require.NoError(t, err)

	var key1 Key
	require.NoError(t, key1.UnmarshalJSON(keyMarshal))

	_, err = decodeKeystore(&key1, DefaultSecret)
	require.NoError(t, err)
}

func TestKeystoreBuiltin(t *testing.T) {
	keys, err := GetPregeneratedBLSKeys()
	require.NoError(t, err)

	for _, key := range keys {
		res, err := decodeKeystore(key, DefaultSecret)
		require.NoError(t, err)
		require.Equal(t, res, key.Priv.Marshal())
	}
}

func decodeKeystore(key *Key, secret string) ([]byte, error) {
	var input map[string]interface{}
	if err := json.Unmarshal(key.Keystore, &input); err != nil {
		return nil, err
	}

	encryptor := keystorev4.New()
	decrypted, err := encryptor.Decrypt(input["crypto"].(map[string]interface{}), secret)
	if err != nil {
		return nil, err
	}
	return decrypted, nil
}
