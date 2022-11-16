package client

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/avast/retry-go"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/gogo/protobuf/proto"
)

var (
	RtyAttNum = uint(5)
	RtyAtt    = retry.Attempts(RtyAttNum)
	RtyDel    = retry.Delay(time.Millisecond * 400)
	RtyErr    = retry.LastErrorOnly(true)
)

type CosmosMessage struct {
	Msg sdk.Msg
}

type RelayerMessage interface {
	Type() string
	MsgBytes() ([]byte, error)
}

type RelayerTxResponse struct {
	Height int64
	TxHash string
	Code   uint32
	Data   string
	Events map[string]string
}

func NewCosmosMessage(msg sdk.Msg) RelayerMessage {
	return CosmosMessage{
		Msg: msg,
	}
}

func CosmosMsg(rm RelayerMessage) sdk.Msg {
	if val, ok := rm.(CosmosMessage); !ok {
		fmt.Printf("got data of type %T but wanted CosmosMessage \n", val)
		return nil
	} else {
		return val.Msg
	}
}

func CosmosMsgs(rm ...RelayerMessage) []sdk.Msg {
	sdkMsgs := make([]sdk.Msg, 0)
	for _, rMsg := range rm {
		if val, ok := rMsg.(CosmosMessage); !ok {
			fmt.Printf("got data of type %T but wanted CosmosMessage \n", val)
			return nil
		} else {
			sdkMsgs = append(sdkMsgs, val.Msg)
		}
	}
	return sdkMsgs
}

func (cm CosmosMessage) Type() string {
	return sdk.MsgTypeURL(cm.Msg)
}

func (cm CosmosMessage) MsgBytes() ([]byte, error) {
	return proto.Marshal(cm.Msg)
}

func (cc *ChainClient) ChainId() string {
	return cc.Config.ChainID
}

func (cc *ChainClient) Type() string {
	return "cosmos"
}

func (cc *ChainClient) Key() string {
	return cc.Config.Key
}

func (cc *ChainClient) Timeout() string {
	return cc.Config.Timeout
}

// Address returns the chains configured address as a string
func (cc *ChainClient) Address() (string, error) {
	var (
		err  error
		info keyring.Info
	)
	info, err = cc.Keybase.Key(cc.Config.Key)
	if err != nil {
		return "", err
	}
	out, err := cc.EncodeBech32AccAddr(info.GetAddress())
	if err != nil {
		return "", err
	}

	return out, err
}

func (cc *ChainClient) TrustingPeriod() (time.Duration, error) {
	res, err := cc.QueryStakingParams(context.Background())
	if err != nil {
		return 0, err
	}

	integer, _ := math.Modf(res.UnbondingTime.Hours() * 0.7)
	trustingStr := fmt.Sprintf("%vh", integer)
	tp, err := time.ParseDuration(trustingStr)
	if err != nil {
		return 0, nil
	}

	return tp, nil
}

// WaitForNBlocks blocks until the next block on a given chain
func (cc *ChainClient) WaitForNBlocks(n int64) error {
	var initial int64
	h, err := cc.RPCClient.Status(context.Background())
	if err != nil {
		return err
	}
	if h.SyncInfo.CatchingUp {
		return fmt.Errorf("chain catching up")
	}
	initial = h.SyncInfo.LatestBlockHeight
	for {
		h, err = cc.RPCClient.Status(context.Background())
		if err != nil {
			return err
		}
		if h.SyncInfo.LatestBlockHeight > initial+n {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}
