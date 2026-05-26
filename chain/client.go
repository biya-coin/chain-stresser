package chain

import (
	"context"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"time"

	errorsmod "cosmossdk.io/errors"
	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"
	"github.com/InjectiveLabs/sdk-go/chain/crypto/ethsecp256k1"
	retry "github.com/avast/retry-go/v4"
	"github.com/cosmos/cosmos-sdk/client"
	sdk "github.com/cosmos/cosmos-sdk/types"
	cosmoserrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	requestTimeout             = 10 * time.Minute
	confirmTimeout             = 10 * time.Minute
	defaultBroadcastStatusPoll = 1 * time.Second
)

var errRetry = errors.New("retry required")

// isNetworkError returns true for transient network errors that should be retried
// (e.g. EOF, connection reset by peer, connection refused)
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "no such host")
}

// TODO: replace with https://github.com/InjectiveLabs/sdk-go/tree/master/client/chain
func NewClient(chainID string, addr string, grpcAddr string) Client {
	rpcClient, err := client.NewClientFromNode("tcp://" + addr)
	orPanic(err)

	grpcClient, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	orPanic(err)

	clientCtx := NewContext(chainID, rpcClient, grpcClient)

	return Client{
		clientCtx: clientCtx,
	}
}

type Client struct {
	clientCtx client.Context
}

func (c Client) TxConfig() client.TxConfig {
	return c.clientCtx.TxConfig
}
func (c Client) GetNumberSequence(address string) (uint64, uint64, error) {
	addr, err := sdk.AccAddressFromBech32(address)
	orPanic(err)

	accNum, accSeq, err := c.clientCtx.AccountRetriever.GetAccountNumberSequence(c.clientCtx, addr)
	if err != nil {
		return 0, 0, err
	}

	return accNum, accSeq, nil
}

type Tx struct {
	Msgs     []sdk.Msg
	Fee      sdk.Coins
	GasLimit uint64
	Memo     string
}

func (c Client) BuildAndSignTx(
	signerAccount Account,
	tx Tx,
) (signedTx authsigning.Tx, err error) {
	return buildAndSignTx(
		c.clientCtx,
		signerAccount.Key,
		signerAccount.Number,
		signerAccount.Sequence,
		tx.Fee,
		tx.GasLimit,
		tx.Memo,
		tx.Msgs...,
	)
}

func (c Client) SignTx(
	signerAccount Account,
	txBuilder client.TxBuilder,
) (signedTx authsigning.Tx, err error) {
	return signTx(
		c.clientCtx,
		signerAccount.Key,
		signerAccount.Number,
		signerAccount.Sequence,
		txBuilder,
	)
}

func (c Client) Encode(signedTx authsigning.Tx) []byte {
	return bytesOrPanic(c.clientCtx.TxConfig.TxEncoder()(signedTx))
}

func (c Client) Broadcast(ctx context.Context, encodedTx []byte, await bool) (string, error) {
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	var txHash string

	retryOpts := []retry.Option{
		retry.UntilSucceeded(),
		retry.MaxDelay(10 * time.Second),
		retry.MaxJitter(time.Second),
		retry.DelayType(retry.RandomDelay),
	}

	// copy bytes just in case
	encodedTxBody := append([]byte{}, encodedTx...)

	if finalError := retry.Do(
		func() error {
			res, err := c.clientCtx.Client.BroadcastTxSync(requestCtx, encodedTxBody)
			if err != nil {
				errRes := client.CheckCometError(err, encodedTxBody)
				if isMempoolFull(errRes) {
					return errRetry
				}

				if !isTxInMempool(errRes) {
					// Transient network errors (EOF, connection reset, etc.) should be retried.
					// The node may briefly drop connections during block commit or under load.
					if isNetworkError(err) {
						return errors.WithStack(err) // retryable
					}
					// JSON-RPC -32603 errors embed the sequence error in the error string.
					// Surface them as Unrecoverable so the caller (stresser) can parse
					// the expected sequence and skip forward.
					if _, ok := IsSequenceError(err); ok {
						return retry.Unrecoverable(err)
					}
					return retry.Unrecoverable(errors.WithStack(err))
				}

				// tx in mempool - all is ok
				txHash = errRes.TxHash
				return nil
			}

			txHash = res.Hash.String()

			if res.Code != 0 {
				if err := checkSequence(res.Codespace, res.Code, res.Log); err != nil {
					return retry.Unrecoverable(err)
				}

				if err := checkNonce(res.Codespace, res.Code, res.Log); err != nil {
					return retry.Unrecoverable(err)
				}

				// If we get a sequence/nonce error and txHash is set, the tx may have
				// already been committed on a previous retry attempt (network error caused
				// a retry but the original broadcast already succeeded). Check on-chain.
				if txHash != "" && isSDKErrorResult(res.Codespace, res.Code, cosmoserrors.ErrWrongSequence) {
					txHashBytes, hexErr := hex.DecodeString(txHash)
					if hexErr == nil {
						if resultTx, queryErr := c.clientCtx.Client.Tx(requestCtx, txHashBytes, false); queryErr == nil && resultTx.TxResult.Code == 0 {
							// tx already committed successfully on a previous attempt
							return nil
						}
					}
				}

				err := errors.Errorf(
					"node returned non-zero code for tx '%s' (code: %d, codespace: %s): %s",
					txHash,
					res.Code,
					res.Codespace,
					res.Log,
				)

				return retry.Unrecoverable(err)
			}

			return nil
		},
		retryOpts...,
	); finalError != nil {
		return txHash, finalError
	}

	txHashBytes, err := hex.DecodeString(txHash)
	if err != nil {
		return "", errors.Wrap(err, "failed to decode tx hash as hex")
	}

	if !await {
		return txHash, nil
	}

	t := time.NewTimer(defaultBroadcastStatusPoll)

	timeoutCtx, cancel := context.WithTimeout(ctx, confirmTimeout)
	defer cancel()

	for {
		select {
		case <-timeoutCtx.Done():
			err := errors.Errorf("transaction timed out while await until included in the block %s", txHash)
			t.Stop()
			return txHash, err

		case <-t.C:
			resultTx, err := c.clientCtx.Client.Tx(timeoutCtx, txHashBytes, false)
			if err != nil {
				if errRes := client.CheckCometError(err, encodedTx); errRes != nil {
					err := errors.Errorf("got tendermint error: %s", errRes.RawLog)
					return "", err
				}

				t.Reset(defaultBroadcastStatusPoll)
				continue

			} else if resultTx.TxResult.Code != 0 {
				res := resultTx.TxResult

				if err := checkSequence(res.Codespace, res.Code, res.Log); err != nil {
					return "", err
				}

				if err := checkNonce(res.Codespace, res.Code, res.Log); err != nil {
					return "", err
				}

				err = errors.Errorf(
					"node returned non-zero code for tx '%s' (code: %d, codespace: %s): %s",
					txHash,
					res.Code,
					res.Codespace,
					res.Log,
				)

				return txHash, err
			} else if resultTx.Height > 0 {
				t.Stop()

				return txHash, nil
			}

			t.Reset(defaultBroadcastStatusPoll)
		}
	}
}

// AwaitTx polls the chain until the tx with the given hex hash is included in a block.
// It returns the block height on success, or an error if the timeout is exceeded.
func (c Client) AwaitTx(ctx context.Context, txHash string) (int64, error) {
	txHashBytes, err := hex.DecodeString(txHash)
	if err != nil {
		return 0, errors.Wrap(err, "failed to decode tx hash as hex")
	}

	t := time.NewTimer(defaultBroadcastStatusPoll)
	timeoutCtx, cancel := context.WithTimeout(ctx, confirmTimeout)
	defer cancel()

	for {
		select {
		case <-timeoutCtx.Done():
			t.Stop()
			return 0, errors.Errorf("AwaitTx timed out waiting for tx %s", txHash)
		case <-t.C:
			resultTx, err := c.clientCtx.Client.Tx(timeoutCtx, txHashBytes, false)
			if err != nil {
				t.Reset(defaultBroadcastStatusPoll)
				continue
			} else if resultTx.Height > 0 {
				t.Stop()
				return resultTx.Height, nil
			}
			t.Reset(defaultBroadcastStatusPoll)
		}
	}
}

func (c Client) NewWasmQueryClient() wasmtypes.QueryClient {
	return wasmtypes.NewQueryClient(c.clientCtx.GRPCClient)
}

func isTxInMempool(errRes *sdk.TxResponse) bool {
	if errRes == nil {
		return false
	}

	return errRes.Codespace == cosmoserrors.ErrTxInMempoolCache.Codespace() &&
		errRes.Code == cosmoserrors.ErrTxInMempoolCache.ABCICode()
}

func isMempoolFull(errRes *sdk.TxResponse) bool {
	if errRes == nil {
		return false
	}

	return errRes.Codespace == cosmoserrors.ErrMempoolIsFull.Codespace() &&
		errRes.Code == cosmoserrors.ErrMempoolIsFull.ABCICode()
}

func buildAndSignTx(
	clientCtx client.Context,
	signerKey Secp256k1PrivateKey,
	accNum, accSeq uint64,
	fee sdk.Coins,
	gasLimit uint64,
	memo string,
	msgs ...sdk.Msg,
) (signedTx authsigning.Tx, err error) {
	txBuilder := clientCtx.TxConfig.NewTxBuilder()
	txBuilder.SetGasLimit(gasLimit)
	txBuilder.SetFeeAmount(fee)
	if memo != "" {
		txBuilder.SetMemo(memo)
	}

	if err = txBuilder.SetMsgs(msgs...); err != nil {
		err = errors.Wrap(err, "failed to set Tx messages")
		return nil, err
	}

	return signTx(
		clientCtx,
		signerKey,
		accNum,
		accSeq,
		txBuilder,
	)
}

func signTx(
	clientCtx client.Context,
	signerKey Secp256k1PrivateKey,
	accNum, accSeq uint64,
	txBuilder client.TxBuilder,
) (signedTx authsigning.Tx, err error) {

	signerData := authsigning.SignerData{
		ChainID:       clientCtx.ChainID,
		AccountNumber: accNum,
		Sequence:      accSeq,
	}

	sigData := &signing.SingleSignatureData{
		SignMode:  signing.SignMode_SIGN_MODE_DIRECT,
		Signature: nil,
	}

	privKey := &ethsecp256k1.PrivKey{
		Key: signerKey,
	}

	sig := signing.SignatureV2{
		PubKey:   privKey.PubKey(),
		Data:     sigData,
		Sequence: accSeq,
	}

	if err := txBuilder.SetSignatures(sig); err != nil {
		err = errors.Wrap(err, "failed to set Tx signatures")
		return nil, err
	}

	bytesToSign := bytesOrPanic(
		authsigning.GetSignBytesAdapter(
			context.Background(),
			clientCtx.TxConfig.SignModeHandler(),
			signing.SignMode_SIGN_MODE_DIRECT,
			signerData,
			txBuilder.GetTx(),
		),
	)

	sigBytes, err := privKey.Sign(bytesToSign)
	if err != nil {
		err = errors.Wrap(err, "failed to sign Tx")
		return nil, err
	}

	sigData.Signature = sigBytes
	if err := txBuilder.SetSignatures(sig); err != nil {
		err = errors.Wrap(err, "failed to set Tx signatures")
		return nil, err
	}

	signedTx = txBuilder.GetTx()
	return signedTx, nil
}

type sequenceError struct {
	expectedSequence uint64
	message          string
}

func (e sequenceError) Error() string {
	return e.message
}

var expectedSequenceRegExp = regexp.MustCompile(`account sequence[^,]*, expected \D*(\d+), got \d+`)
var expectedNonceRegExp = regexp.MustCompile(`invalid nonce; got \d+, expected (\d+)`)

// firstNonEmpty returns the first non-empty string from the slice, or "".
func firstNonEmpty(ss []string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func isSDKErrorResult(codespace string, code uint32, sdkErr *errorsmod.Error) bool {
	return codespace == sdkErr.Codespace() &&
		code == sdkErr.ABCICode()
}

func checkSequence(codespace string, code uint32, log string) error {
	// Cosmos SDK doesn't return expected sequence number as a parameter from RPC call,
	// so we must parse the error message in a hacky way.

	if !isSDKErrorResult(codespace, code, cosmoserrors.ErrWrongSequence) {
		return nil
	}

	matches := expectedSequenceRegExp.FindStringSubmatch(log)
	var seqStr string
	if len(matches) > 1 {
		seqStr = firstNonEmpty(matches[1:])
	}
	if seqStr == "" {
		return errors.Errorf("cosmos sdk hasn't returned expected sequence number, log mesage received: %s", log)
	}

	expectedSequence, err := strconv.ParseUint(seqStr, 10, 64)
	if err != nil {
		return errors.Wrapf(err, "can't parse expected sequence number, log mesage received: %s", log)
	}

	return errors.WithStack(sequenceError{
		message:          log,
		expectedSequence: expectedSequence,
	})
}

func checkNonce(codespace string, code uint32, log string) error {
	if !isSDKErrorResult(codespace, code, cosmoserrors.ErrInvalidSequence) {
		return nil
	}

	matches := expectedNonceRegExp.FindStringSubmatch(log)
	if len(matches) != 2 {
		return errors.Errorf("cosmos sdk hasn't returned expected nonce number, log mesage received: %s", log)
	}

	expectedSequence, err := strconv.ParseUint(matches[1], 10, 64)
	if err != nil {
		return errors.Wrapf(err, "can't parse expected nonce number, log mesage received: %s", log)
	}

	return errors.WithStack(sequenceError{
		message:          log,
		expectedSequence: expectedSequence,
	})
}

// IsSequenceError checks if error is related to account sequence mismatch, and returns expected account sequence.
// It handles both typed sequenceError (from checkSequence) and raw error strings (e.g. from JSON-RPC -32603 errors).
func IsSequenceError(err error) (uint64, bool) {
	var seqErr sequenceError

	if errors.As(err, &seqErr) {
		return seqErr.expectedSequence, true
	}

	// Fallback: parse expected sequence from the raw error message.
	// This covers cases like JSON-RPC -32603 Internal errors where the message is embedded
	// directly in the error string rather than in res.Code/res.Log.
	if err != nil {
		matches := expectedSequenceRegExp.FindStringSubmatch(err.Error())
		if len(matches) > 1 {
			if seqStr := firstNonEmpty(matches[1:]); seqStr != "" {
				if expectedSeq, parseErr := strconv.ParseUint(seqStr, 10, 64); parseErr == nil {
					return expectedSeq, true
				}
			}
		}
	}

	return 0, false
}

func IsMempoolFullError(err error) bool {
	if err == nil {
		return false
	}

	errMsg := err.Error()

	return strings.Contains(errMsg, "mempool is full") ||
		strings.Contains(errMsg, "lane default is full") ||
		strings.Contains(errMsg, "lane ") && strings.Contains(errMsg, " is full")
}
