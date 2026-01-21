package playground

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// TestTxConfig holds configuration for the test transaction
type TestTxConfig struct {
	RPCURL     string
	PrivateKey string
	ToAddress  string
	Value      *big.Int
	GasLimit   uint64
	GasPrice   *big.Int
}

// DefaultTestTxConfig returns the default test transaction configuration
// Sends from second prefunded account to first prefunded account (builder/coinbase)
func DefaultTestTxConfig() *TestTxConfig {
	value := new(big.Int)
	value.SetString("100000000000000000", 10) // 0.1 ether in wei

	gasPrice := new(big.Int)
	gasPrice.SetString("1000000000", 10) // 1 gwei

	// Use the second prefunded account as sender
	privateKey := staticPrefundedAccounts[1]
	// Strip 0x prefix if present
	if len(privateKey) > 2 && privateKey[:2] == "0x" {
		privateKey = privateKey[2:]
	}

	// Derive the first prefunded account address (builder/coinbase) as recipient
	firstPrivKey, _ := crypto.HexToECDSA(stripHexPrefix(staticPrefundedAccounts[0]))
	toAddress := crypto.PubkeyToAddress(firstPrivKey.PublicKey)

	return &TestTxConfig{
		RPCURL:     "http://localhost:8545",
		PrivateKey: privateKey,
		ToAddress:  toAddress.Hex(),
		Value:      value,
		GasLimit:   21000,
		GasPrice:   gasPrice,
	}
}

func stripHexPrefix(s string) string {
	if len(s) > 2 && s[:2] == "0x" {
		return s[2:]
	}
	return s
}

// SendTestTransaction sends a test transaction and waits for the receipt
func SendTestTransaction(ctx context.Context, cfg *TestTxConfig) error {

	// Connect to the RPC endpoint
	client, err := ethclient.Dial(cfg.RPCURL)
	if err != nil {
		return fmt.Errorf("failed to connect to RPC: %w", err)
	}
	defer client.Close()

	// Parse private key
	privateKey, err := crypto.HexToECDSA(cfg.PrivateKey)
	if err != nil {
		return fmt.Errorf("failed to parse private key: %w", err)
	}

	publicKey := privateKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return fmt.Errorf("failed to get public key")
	}
	fromAddress := crypto.PubkeyToAddress(*publicKeyECDSA)

	// Get chain ID
	chainID, err := client.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("failed to get chain ID: %w", err)
	}
	fmt.Printf("Chain ID: %d\n", chainID)

	// Get nonce
	nonce, err := client.PendingNonceAt(ctx, fromAddress)
	if err != nil {
		return fmt.Errorf("failed to get nonce: %w", err)
	}
	fmt.Printf("Nonce: %d\n", nonce)

	// Parse to address
	toAddress := common.HexToAddress(cfg.ToAddress)

	// Create transaction
	tx := types.NewTransaction(
		nonce,
		toAddress,
		cfg.Value,
		cfg.GasLimit,
		cfg.GasPrice,
		nil,
	)

	// Sign transaction
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), privateKey)
	if err != nil {
		return fmt.Errorf("failed to sign transaction: %w", err)
	}

	// Send transaction
	fmt.Printf("Sending transaction at %s\n", time.Now().Format("15:04:05"))
	err = client.SendTransaction(ctx, signedTx)
	if err != nil {
		return fmt.Errorf("failed to send transaction: %w", err)
	}

	txHash := signedTx.Hash()
	fmt.Printf("TX Hash: %s\n", txHash.Hex())

	// Wait for receipt
	fmt.Println("Waiting for receipt...")
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		receipt, err := client.TransactionReceipt(ctx, txHash)
		if err == nil && receipt != nil {
			fmt.Printf("Receipt received!\n")
			fmt.Printf("  Block Number: %d\n", receipt.BlockNumber)
			fmt.Printf("  Gas Used: %d\n", receipt.GasUsed)
			fmt.Printf("  Status: %d\n", receipt.Status)

			// Get block to show extra data (builder name)
			block, err := client.BlockByNumber(ctx, receipt.BlockNumber)
			if err == nil && block != nil {
				fmt.Printf("  Extra Data: %s\n", string(block.Extra()))
			}
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
}
