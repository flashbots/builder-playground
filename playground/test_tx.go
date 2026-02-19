package playground

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

// buildernetSigningTransport is an http.RoundTripper that adds the X-BuilderNet-Signature
// header to every request. FlowProxy requires this header for orderflow authentication.
// The signature is: keccak256(body) → format as hex → EIP-191 sign → header.
// Any valid key pair works — it's an identity tag, not access control.
type buildernetSigningTransport struct {
	base       http.RoundTripper
	privateKey *ecdsa.PrivateKey
	address    common.Address
}

func (t *buildernetSigningTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	defer req.Body.Close()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}

	// Sign: keccak256(body) → hex string → EIP-191 hash → ECDSA sign
	bodyHash := crypto.Keccak256(body)
	hashHex := "0x" + hex.EncodeToString(bodyHash)

	// EIP-191: "\x19Ethereum Signed Message:\n" + len + message
	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(hashHex))
	msgHash := crypto.Keccak256(append([]byte(prefix), []byte(hashHex)...))

	sig, err := crypto.Sign(msgHash, t.privateKey)
	if err != nil {
		return nil, fmt.Errorf("buildernet signing failed: %w", err)
	}
	sig[64] += 27 // V: 0/1 → 27/28

	req.Header.Set("X-BuilderNet-Signature",
		fmt.Sprintf("%s:0x%s", t.address.Hex(), hex.EncodeToString(sig)))

	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	return t.base.RoundTrip(req)
}

// TestTxConfig holds configuration for the test transaction
type TestTxConfig struct {
	RPCURL     string // Target RPC URL for sending transactions (e.g., rbuilder)
	ELRPCURL   string // EL RPC URL for chain queries (e.g., reth). If empty, uses RPCURL
	PrivateKey string
	ToAddress  string
	Value      *big.Int
	GasLimit   uint64
	GasPrice   *big.Int
	Timeout    time.Duration // Timeout for waiting for receipt. If 0, no timeout.
	Retries    int           // Max failed receipt requests before giving up. If 0, retry forever.
	Insecure   bool          // Skip TLS certificate verification
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
	// Determine EL RPC URL (for chain queries)
	elRPCURL := cfg.ELRPCURL
	if elRPCURL == "" {
		elRPCURL = cfg.RPCURL
	}

	// Parse private key (used for both tx signing and BuilderNet header)
	privateKey, err := crypto.HexToECDSA(cfg.PrivateKey)
	if err != nil {
		return fmt.Errorf("failed to parse private key: %w", err)
	}
	fromAddress := crypto.PubkeyToAddress(privateKey.PublicKey)

	// dialRPC connects to an RPC endpoint, adding BuilderNet signature header
	// and optionally skipping TLS verification
	dialRPC := func(url string) (*ethclient.Client, error) {
		var base http.RoundTripper
		if cfg.Insecure {
			base = &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			}
		} else {
			base = http.DefaultTransport
		}
		httpClient := &http.Client{
			Transport: &buildernetSigningTransport{
				base:       base,
				privateKey: privateKey,
				address:    fromAddress,
			},
		}
		rpcClient, err := rpc.DialOptions(ctx, url, rpc.WithHTTPClient(httpClient))
		if err != nil {
			return nil, err
		}
		return ethclient.NewClient(rpcClient), nil
	}

	// Connect to the EL RPC endpoint (for chain queries)
	elClient, err := dialRPC(elRPCURL)
	if err != nil {
		return fmt.Errorf("failed to connect to EL RPC: %w", err)
	}
	defer elClient.Close()

	// Connect to the target RPC endpoint (for sending transactions)
	var targetClient *ethclient.Client
	if cfg.RPCURL != elRPCURL {
		targetClient, err = dialRPC(cfg.RPCURL)
		if err != nil {
			return fmt.Errorf("failed to connect to target RPC: %w", err)
		}
		defer targetClient.Close()
	} else {
		targetClient = elClient
	}

	// Get chain ID (from EL)
	chainID, err := elClient.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("failed to get chain ID: %w", err)
	}
	fmt.Printf("Chain ID: %d\n", chainID)

	// Get nonce (from EL)
	nonce, err := elClient.PendingNonceAt(ctx, fromAddress)
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

	// Send transaction (to target RPC)
	fmt.Printf("Sending transaction at %s\n", time.Now().Format("15:04:05"))
	err = targetClient.SendTransaction(ctx, signedTx)
	if err != nil {
		return fmt.Errorf("failed to send transaction: %w", err)
	}

	txHash := signedTx.Hash()
	fmt.Printf("TX Hash: %s\n", txHash.Hex())

	// Apply timeout if configured
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}

	fmt.Println("Waiting for receipt...")
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	failedAttempts := 0
	for {
		select {
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				return fmt.Errorf("timeout waiting for transaction receipt after %s", cfg.Timeout)
			}
			return ctx.Err()
		case <-ticker.C:
			receipt, err := elClient.TransactionReceipt(ctx, txHash)
			if err == nil && receipt != nil {
				fmt.Printf("Receipt received!\n")
				fmt.Printf("  Block Number: %d\n", receipt.BlockNumber)
				fmt.Printf("  Gas Used: %d\n", receipt.GasUsed)
				fmt.Printf("  Status: %d\n", receipt.Status)

				// Get block to show extra data (builder name)
				block, err := elClient.BlockByNumber(ctx, receipt.BlockNumber)
				if err == nil && block != nil {
					fmt.Printf("  Extra Data: %s\n", string(block.Extra()))
				}
				return nil
			}
			failedAttempts++
			if cfg.Retries > 0 && failedAttempts >= cfg.Retries {
				return fmt.Errorf("failed to get transaction receipt after %d attempts", cfg.Retries)
			}
		}
	}
}
