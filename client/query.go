package client

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	ctypes "github.com/tendermint/tendermint/rpc/core/types"

	sdk "github.com/cosmos/cosmos-sdk/types"
	querytypes "github.com/cosmos/cosmos-sdk/types/query"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	bankTypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	distTypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// QueryTx takes a transaction hash and returns the transaction
func (cc *ChainClient) QueryTx(hashHex string) (*ctypes.ResultTx, error) {
	hash, err := hex.DecodeString(hashHex)
	if err != nil {
		return &ctypes.ResultTx{}, err
	}

	return cc.RPCClient.Tx(context.Background(), hash, true)
}

// QueryTxs returns an array of transactions given a tag
func (cc *ChainClient) QueryTxs(page, limit int, events []string) ([]*ctypes.ResultTx, error) {
	if len(events) == 0 {
		return nil, errors.New("must declare at least one event to search")
	}

	if page <= 0 {
		return nil, errors.New("page must greater than 0")
	}

	if limit <= 0 {
		return nil, errors.New("limit must greater than 0")
	}

	res, err := cc.RPCClient.TxSearch(context.Background(), strings.Join(events, " AND "), true, &page, &limit, "")
	if err != nil {
		return nil, err
	}
	return res.Txs, nil
}

// QueryBalance returns the amount of coins in the relayer account
func (cc *ChainClient) QueryBalance(keyName string) (sdk.Coins, error) {
	var (
		addr string
		err  error
	)
	if keyName == "" {
		addr, err = cc.Address()
	} else {
		cc.Config.Key = keyName
		addr, err = cc.Address()
	}

	if err != nil {
		return nil, err
	}
	return cc.QueryBalanceWithAddress(addr)
}

// QueryBalanceWithAddress returns the amount of coins in the relayer account with address as input
// TODO add pagination support
func (cc *ChainClient) QueryBalanceWithAddress(address string) (sdk.Coins, error) {
	p := &bankTypes.QueryAllBalancesRequest{Address: address, Pagination: DefaultPageRequest()}
	queryClient := bankTypes.NewQueryClient(cc)

	res, err := queryClient.AllBalances(context.Background(), p)
	if err != nil {
		return nil, err
	}

	return res.Balances, nil
}

// QueryUnbondingPeriod returns the unbonding period of the chain
func (cc *ChainClient) QueryUnbondingPeriod() (time.Duration, error) {
	req := stakingtypes.QueryParamsRequest{}
	queryClient := stakingtypes.NewQueryClient(cc)

	res, err := queryClient.Params(context.Background(), &req)
	if err != nil {
		return 0, err
	}

	return res.Params.UnbondingTime, nil
}

func (cc *ChainClient) QueryLatestHeight() (int64, error) {
	stat, err := cc.RPCClient.Status(context.Background())
	if err != nil {
		return -1, err
	} else if stat.SyncInfo.CatchingUp {
		return -1, fmt.Errorf("node at %s running chain %s not caught up", cc.Config.RPCAddr, cc.Config.ChainID)
	}
	return stat.SyncInfo.LatestBlockHeight, nil
}

func (cc *ChainClient) QueryAccount(address sdk.AccAddress) (authtypes.AccountI, error) {
	addr, err := cc.EncodeBech32AccAddr(address)
	if err != nil {
		return nil, err
	}
	res, err := authtypes.NewQueryClient(cc).Account(context.Background(), &authtypes.QueryAccountRequest{Address: addr})
	if err != nil {
		return nil, err
	}
	var acc authtypes.AccountI
	if err := cc.Codec.InterfaceRegistry.UnpackAny(res.Account, &acc); err != nil {
		return nil, err
	}
	return acc, nil
}

func (cc *ChainClient) QueryDelegatorValidators(ctx context.Context, address sdk.AccAddress) ([]string, error) {
	res, err := distTypes.NewQueryClient(cc).DelegatorValidators(ctx, &distTypes.QueryDelegatorValidatorsRequest{
		DelegatorAddress: cc.MustEncodeAccAddr(address),
	})
	if err != nil {
		return nil, err
	}
	return res.Validators, nil
}

func (cc *ChainClient) QueryDistributionCommission(ctx context.Context, address sdk.ValAddress) (sdk.DecCoins, error) {
	valAddr, err := cc.EncodeBech32ValAddr(address)
	if err != nil {
		return nil, err
	}
	request := distTypes.QueryValidatorCommissionRequest{
		ValidatorAddress: valAddr,
	}
	res, err := distTypes.NewQueryClient(cc).ValidatorCommission(ctx, &request)
	if err != nil {
		return nil, err
	}
	return res.Commission.Commission, nil
}

func (cc *ChainClient) QueryDistributionCommunityPool(ctx context.Context) (sdk.DecCoins, error) {
	res, err := distTypes.NewQueryClient(cc).CommunityPool(ctx, &distTypes.QueryCommunityPoolRequest{})
	if err != nil {
		return nil, err
	}
	return res.Pool, err
}

func (cc *ChainClient) QueryDistributionParams(ctx context.Context) (*distTypes.Params, error) {
	res, err := distTypes.NewQueryClient(cc).Params(ctx, &distTypes.QueryParamsRequest{})
	if err != nil {
		return nil, err
	}
	return &res.Params, nil
}

func (cc *ChainClient) QueryDistributionRewards(ctx context.Context, delegatorAddress sdk.AccAddress, validatorAddress sdk.ValAddress) (sdk.DecCoins, error) {
	delAddr, err := cc.EncodeBech32AccAddr(delegatorAddress)
	if err != nil {
		return nil, err
	}
	valAddr, err := cc.EncodeBech32ValAddr(validatorAddress)
	if err != nil {
		return nil, err
	}
	request := distTypes.QueryDelegationRewardsRequest{
		DelegatorAddress: delAddr,
		ValidatorAddress: valAddr,
	}
	res, err := distTypes.NewQueryClient(cc).DelegationRewards(ctx, &request)
	if err != nil {
		return nil, err
	}
	return res.Rewards, nil
}

// QueryDistributionSlashes returns all slashes of a validator, optionally pass the start and end height
func (cc *ChainClient) QueryDistributionSlashes(ctx context.Context, validatorAddress sdk.ValAddress, startHeight, endHeight uint64, pageReq *querytypes.PageRequest) (*distTypes.QueryValidatorSlashesResponse, error) {
	valAddr, err := cc.EncodeBech32ValAddr(validatorAddress)
	if err != nil {
		return nil, err
	}
	request := distTypes.QueryValidatorSlashesRequest{
		ValidatorAddress: valAddr,
		StartingHeight:   startHeight,
		EndingHeight:     endHeight,
		Pagination:       pageReq,
	}
	return distTypes.NewQueryClient(cc).ValidatorSlashes(ctx, &request)
}

// QueryDistributionValidatorRewards returns all the validator distribution rewards from a given height
func (cc *ChainClient) QueryDistributionValidatorRewards(ctx context.Context, validatorAddress sdk.ValAddress) (sdk.DecCoins, error) {
	valAddr, err := cc.EncodeBech32ValAddr(validatorAddress)
	if err != nil {
		return nil, err
	}
	request := distTypes.QueryValidatorOutstandingRewardsRequest{
		ValidatorAddress: valAddr,
	}
	res, err := distTypes.NewQueryClient(cc).ValidatorOutstandingRewards(ctx, &request)
	if err != nil {
		return nil, err
	}
	return res.Rewards.Rewards, nil
}

// QueryTotalSupply returns the total supply of coins on a chain
func (cc *ChainClient) QueryTotalSupply(ctx context.Context, pageReq *querytypes.PageRequest) (*bankTypes.QueryTotalSupplyResponse, error) {
	return bankTypes.NewQueryClient(cc).TotalSupply(ctx, &bankTypes.QueryTotalSupplyRequest{Pagination: pageReq})
}

func (cc *ChainClient) QueryDenomsMetadata(ctx context.Context, pageReq *querytypes.PageRequest) (*bankTypes.QueryDenomsMetadataResponse, error) {
	return bankTypes.NewQueryClient(cc).DenomsMetadata(ctx, &bankTypes.QueryDenomsMetadataRequest{Pagination: pageReq})
}

func (cc *ChainClient) QueryStakingParams(ctx context.Context) (*stakingtypes.Params, error) {
	res, err := stakingtypes.NewQueryClient(cc).Params(ctx, &stakingtypes.QueryParamsRequest{})
	if err != nil {
		return nil, err
	}
	return &res.Params, nil
}

func DefaultPageRequest() *querytypes.PageRequest {
	return &querytypes.PageRequest{
		Key:        []byte(""),
		Offset:     0,
		Limit:      1000,
		CountTotal: true,
	}
}
