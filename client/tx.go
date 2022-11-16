package client

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	"github.com/cosmos/cosmos-sdk/store/rootmulti"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	abci "github.com/tendermint/tendermint/abci/types"
	rpcclient "github.com/tendermint/tendermint/rpc/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (cc *ChainClient) TxFactory() tx.Factory {
	return tx.Factory{}.
		WithAccountRetriever(cc).
		WithChainID(cc.Config.ChainID).
		WithTxConfig(cc.Codec.TxConfig).
		WithGasAdjustment(cc.Config.GasAdjustment).
		WithGasPrices(cc.Config.GasPrices).
		WithKeybase(cc.Keybase).
		WithSignMode(cc.Config.SignMode())
}

func (ccc *ChainClientConfig) SignMode() signing.SignMode {
	signMode := signing.SignMode_SIGN_MODE_UNSPECIFIED
	switch ccc.SignModeStr {
	case "direct":
		signMode = signing.SignMode_SIGN_MODE_DIRECT
	case "amino-json":
		signMode = signing.SignMode_SIGN_MODE_LEGACY_AMINO_JSON
	}
	return signMode
}

func (cc *ChainClient) SendMessage(msg RelayerMessage) (*RelayerTxResponse, bool, error) {
	return cc.SendMessages([]RelayerMessage{msg})
}

func (cc *ChainClient) SendMessages(msgs []RelayerMessage) (*RelayerTxResponse, bool, error) {
	// Query account details
	txf, err := cc.PrepareFactory(cc.TxFactory())
	if err != nil {
		return nil, false, err
	}

	// TODO: Make this work with new CalculateGas method
	// TODO: This is related to GRPC client stuff?
	// https://github.com/cosmos/cosmos-sdk/blob/5725659684fc93790a63981c653feee33ecf3225/client/tx/tx.go#L297
	// If users pass gas adjustment, then calculate gas
	_, adjusted, err := cc.CalculateGas(txf, CosmosMsgs(msgs...)...)
	if err != nil {
		return nil, false, err
	}

	// Set the gas amount on the transaction factory
	txf = txf.WithGas(adjusted)

	// Build the transaction builder
	txb, err := tx.BuildUnsignedTx(txf, CosmosMsgs(msgs...)...)
	if err != nil {
		return nil, false, err
	}

	// Attach the signature to the transaction
	// Force encoding in the chain specific address
	for _, msg := range msgs {
		cc.Codec.Marshaler.MustMarshalJSON(CosmosMsg(msg))
	}

	done := cc.SetSDKContext()
	if err = tx.Sign(txf, cc.Config.Key, txb, false); err != nil {
		return nil, false, err
	}
	done()

	// Generate the transaction bytes
	txBytes, err := cc.Codec.TxConfig.TxEncoder()(txb.GetTx())
	if err != nil {
		return nil, false, err
	}

	// Broadcast those bytes
	res, err := cc.BroadcastTx(context.Background(), txBytes)
	if err != nil {
		return nil, false, err
	}

	// Parse events and build a map where the key is event.Type+"."+attribute.Key
	events := make(map[string]string, 1)
	for _, logs := range res.Logs {
		for _, ev := range logs.Events {
			for _, attr := range ev.Attributes {
				key := ev.Type + "." + attr.Key
				events[key] = attr.Value
			}
		}
	}

	rlyRes := &RelayerTxResponse{
		Height: res.Height,
		TxHash: res.TxHash,
		Code:   res.Code,
		Data:   res.Data,
		Events: events,
	}

	// transaction was executed, log the success or failure using the tx response code
	// NOTE: error is nil, logic should use the returned error to determine if the
	// transaction was successfully executed.
	if rlyRes.Code != 0 {
		//cc.LogFailedTx(res, err, CosmosMsgs(msgs...))
		return rlyRes, false, fmt.Errorf("transaction failed with code: %d", res.Code)
	}

	//cc.LogSuccessTx(res, CosmosMsgs(msgs...))
	return rlyRes, true, nil
}

func (cc *ChainClient) SendMsg(ctx context.Context, msg sdk.Msg) (*sdk.TxResponse, error) {
	return cc.SendMsgs(ctx, []sdk.Msg{msg}, nil)
}

// If msgPackage is privided and not empty, the package in the Any typeUrl of the msg will be replaced with the provided msgPackage
func (cc *ChainClient) SendMsgWithPackageName(ctx context.Context, msg sdk.Msg, msgPackage *string) (*sdk.TxResponse, error) {
	return cc.SendMsgs(ctx, []sdk.Msg{msg}, msgPackage)
}

// SendMsgs wraps the msgs in a StdTx, signs and sends it. An error is returned if there
// was an issue sending the transaction. A successfully sent, but failed transaction will
// not return an error. If a transaction is successfully sent, the result of the execution
// of that transaction will be logged. A boolean indicating if a transaction was successfully
// sent and executed successfully is returned.
func (cc *ChainClient) SendMsgs(ctx context.Context, msgs []sdk.Msg, msgPackage *string) (*sdk.TxResponse, error) {
	txf, err := cc.PrepareFactory(cc.TxFactory())
	if err != nil {
		return nil, err
	}

	// TODO: Make this work with new CalculateGas method
	// TODO: This is related to GRPC client stuff?
	// https://github.com/cosmos/cosmos-sdk/blob/5725659684fc93790a63981c653feee33ecf3225/client/tx/tx.go#L297
	_, adjusted, err := cc.CalculateGasWithPackageName(txf, msgPackage, msgs...)
	if err != nil {
		return nil, err
	}

	// Set the gas amount on the transaction factory
	txf = txf.WithGas(adjusted)

	// Build the transaction builder
	txb, err := tx.BuildUnsignedTx(txf, msgs...)
	if err != nil {
		return nil, err
	}

	if msgPackage != nil && *msgPackage != "" {
		protoProvider, ok := txb.(protoTxProvider)
		if !ok {
			return nil, fmt.Errorf("not a protoTxProvider")
		}
		for _, message := range protoProvider.GetProtoTx().GetBody().GetMessages() {
			temps := strings.Split(message.TypeUrl, ".")
			typeName := temps[len(temps)-1]
			message.TypeUrl = "/" + *msgPackage + "." + typeName
		}
	}

	done := cc.SetSDKContext()
	if err = tx.Sign(txf, cc.Config.Key, txb, false); err != nil {
		return nil, err
	}
	done()

	// Generate the transaction bytes
	txBytes, err := cc.Codec.TxConfig.TxEncoder()(txb.GetTx())
	if err != nil {
		return nil, err
	}

	// Broadcast those bytes
	txResponse, err := cc.BroadcastTx(ctx, txBytes)
	if err != nil {
		return nil, fmt.Errorf("BroadcastTx err: %w", err)
	}
	if txResponse.Code != sdkerrors.SuccessABCICode {
		txResponseErr := fmt.Errorf("BroadcastTx failed with code: %d, rawLog: %s",
			txResponse.Code, txResponse.RawLog)

		return txResponse, txResponseErr
	}

	txResponse, err = cc.waitMined(txResponse.TxHash)
	if err != nil {
		return nil, fmt.Errorf("waitMined err: %w", err)
	}

	// transaction was executed, log the success or failure using the tx response code
	// NOTE: error is nil, logic should use the returned error to determine if the
	// transaction was successfully executed.
	if txResponse.Code != 0 {
		return txResponse, fmt.Errorf("transaction failed with code: %d", txResponse.Code)
	}

	return txResponse, nil
}

var errGasCode = fmt.Errorf("code %d", sdkerrors.ErrOutOfGas.ABCICode())
var errSeqCode = fmt.Errorf("code %d", sdkerrors.ErrWrongSequence.ABCICode())

func (cc *ChainClient) waitMined(txHash string) (*sdk.TxResponse, error) {
	var err error
	mined := false
	var txResponse *sdk.TxResponse
	for try := 0; try < 30; try++ {
		time.Sleep(1 * time.Second)
		if txResponse, err = cc.queryTx(txHash); err == nil {
			mined = true
			break
		}
	}
	if !mined {
		return txResponse, fmt.Errorf("tx not mined, err: %w", err)
	} else if txResponse.Code != sdkerrors.SuccessABCICode {
		if txResponse.Code == sdkerrors.ErrOutOfGas.ABCICode() { // out of gas
			return txResponse, fmt.Errorf("tx failed with %w, %s", errGasCode, txResponse.RawLog)
		} else if txResponse.Code == sdkerrors.ErrWrongSequence.ABCICode() {
			return txResponse, fmt.Errorf("tx failed with %w, %s", errSeqCode, txResponse.RawLog)
		} else {
			return txResponse, fmt.Errorf("tx failed with code %d, %s", txResponse.Code, txResponse.RawLog)
		}
	}
	return txResponse, nil
}

func (cc *ChainClient) queryTx(hashHexStr string) (*sdk.TxResponse, error) {
	hash, err := hex.DecodeString(hashHexStr)
	if err != nil {
		return nil, err
	}

	resTx, err := cc.RPCClient.Tx(context.Background(), hash, true)
	if err != nil {
		return nil, err
	}

	out, err := cc.mkTxResult(resTx)
	if err != nil {
		return out, err
	}

	return out, nil
}

func (cc *ChainClient) PrepareFactory(txf tx.Factory) (tx.Factory, error) {
	from, err := cc.GetKeyAddress()
	if err != nil {
		return tx.Factory{}, err
	}

	cliCtx := client.Context{}.WithClient(cc.RPCClient).
		WithInterfaceRegistry(cc.Codec.InterfaceRegistry).
		WithChainID(cc.Config.ChainID).
		WithCodec(cc.Codec.Marshaler)

	// Set the account number and sequence on the transaction factory
	if err := txf.AccountRetriever().EnsureExists(cliCtx, from); err != nil {
		return txf, err
	}

	// TODO: why this code? this may potentially require another query when we don't want one
	initNum, initSeq := txf.AccountNumber(), txf.Sequence()
	if initNum == 0 || initSeq == 0 {
		num, seq, err := txf.AccountRetriever().GetAccountNumberSequence(cliCtx, from)
		if err != nil {
			return txf, err
		}

		if initNum == 0 {
			txf = txf.WithAccountNumber(num)
		}

		if initSeq == 0 {
			txf = txf.WithSequence(seq)
		}
	}

	return txf, nil
}

func (cc *ChainClient) CalculateGas(txf tx.Factory, msgs ...sdk.Msg) (txtypes.SimulateResponse, uint64, error) {
	return cc.CalculateGasWithPackageName(txf, nil, msgs...)
}

func (cc *ChainClient) CalculateGasWithPackageName(txf tx.Factory, msgPackage *string, msgs ...sdk.Msg) (txtypes.SimulateResponse, uint64, error) {
	txBytes, err := BuildSimTx(txf, msgPackage, msgs...)
	if err != nil {
		return txtypes.SimulateResponse{}, 0, err
	}

	simQuery := abci.RequestQuery{
		Path: "/cosmos.tx.v1beta1.Service/Simulate",
		Data: txBytes,
	}

	res, err := cc.QueryABCI(simQuery)
	if err != nil {
		return txtypes.SimulateResponse{}, 0, err
	}

	var simRes txtypes.SimulateResponse
	if err := simRes.Unmarshal(res.Value); err != nil {
		return txtypes.SimulateResponse{}, 0, err
	}

	return simRes, uint64(txf.GasAdjustment() * float64(simRes.GasInfo.GasUsed)), nil
}

func (cc *ChainClient) QueryABCI(req abci.RequestQuery) (abci.ResponseQuery, error) {
	opts := rpcclient.ABCIQueryOptions{
		Height: req.Height,
		Prove:  req.Prove,
	}
	result, err := cc.RPCClient.ABCIQueryWithOptions(context.Background(), req.Path, req.Data, opts)
	if err != nil {
		return abci.ResponseQuery{}, err
	}

	if !result.Response.IsOK() {
		return abci.ResponseQuery{}, sdkErrorToGRPCError(result.Response)
	}

	// data from trusted node or subspace query doesn't need verification
	if !opts.Prove || !isQueryStoreWithProof(req.Path) {
		return result.Response, nil
	}

	return result.Response, nil
}

func sdkErrorToGRPCError(resp abci.ResponseQuery) error {
	switch resp.Code {
	case sdkerrors.ErrInvalidRequest.ABCICode():
		return status.Error(codes.InvalidArgument, resp.Log)
	case sdkerrors.ErrUnauthorized.ABCICode():
		return status.Error(codes.Unauthenticated, resp.Log)
	case sdkerrors.ErrKeyNotFound.ABCICode():
		return status.Error(codes.NotFound, resp.Log)
	default:
		return status.Error(codes.Unknown, resp.Log)
	}
}

// isQueryStoreWithProof expects a format like /<queryType>/<storeName>/<subpath>
// queryType must be "store" and subpath must be "key" to require a proof.
func isQueryStoreWithProof(path string) bool {
	if !strings.HasPrefix(path, "/") {
		return false
	}

	paths := strings.SplitN(path[1:], "/", 3)

	switch {
	case len(paths) != 3:
		return false
	case paths[0] != "store":
		return false
	case rootmulti.RequireProof("/" + paths[2]):
		return true
	}

	return false
}

// protoTxProvider is a type which can provide a proto transaction. It is a
// workaround to get access to the wrapper TxBuilder's method GetProtoTx().
type protoTxProvider interface {
	GetProtoTx() *txtypes.Tx
}

// BuildSimTx creates an unsigned tx with an empty single signature and returns
// the encoded transaction or an error if the unsigned transaction cannot be built.
func BuildSimTx(txf tx.Factory, msgPackage *string, msgs ...sdk.Msg) ([]byte, error) {
	txb, err := tx.BuildUnsignedTx(txf, msgs...)
	if err != nil {
		return nil, err
	}

	protoProvider, ok := txb.(protoTxProvider)
	if !ok {
		return nil, fmt.Errorf("cannot simulate amino tx")
	} else {
		if msgPackage != nil && *msgPackage != "" {
			for _, message := range protoProvider.GetProtoTx().GetBody().GetMessages() {
				temps := strings.Split(message.TypeUrl, ".")
				typeName := temps[len(temps)-1]
				message.TypeUrl = "/" + *msgPackage + "." + typeName
			}
		}
	}

	// Create an empty signature literal as the ante handler will populate with a
	// sentinel pubkey.
	sig := signing.SignatureV2{
		PubKey: &secp256k1.PubKey{},
		Data: &signing.SingleSignatureData{
			SignMode: txf.SignMode(),
		},
		Sequence: txf.Sequence(),
	}
	if err := txb.SetSignatures(sig); err != nil {
		return nil, err
	}

	simReq := txtypes.SimulateRequest{Tx: protoProvider.GetProtoTx()}
	return simReq.Marshal()
}
