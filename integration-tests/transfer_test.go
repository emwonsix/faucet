//go:build integrationtests

package integrationtests

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	nethttp "net/http"
	"testing"
	"time"

	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/CoreumFoundation/coreum-tools/pkg/logger"
	"github.com/CoreumFoundation/coreum-tools/pkg/must"
	"github.com/CoreumFoundation/coreum/pkg/client"
	coreumconfig "github.com/CoreumFoundation/coreum/pkg/config"
	"github.com/CoreumFoundation/coreum/pkg/config/constant"
	"github.com/CoreumFoundation/faucet/http"
	"github.com/CoreumFoundation/faucet/pkg/config"
)

type testConfig struct {
	coredAddress   string
	faucetAddress  string
	clientCtx      client.Context
	transferAmount string
	network        coreumconfig.Network
}

var cfg testConfig

func init() {
	flag.StringVar(&cfg.coredAddress, "cored-address", "localhost:9090", "Address of cored node started by znet")
	flag.StringVar(&cfg.faucetAddress, "faucet-address", "http://localhost:8090", "Address of the faucet")
	flag.StringVar(&cfg.transferAmount, "transfer-amount", "1000000", "Amount transferred by faucet in each request")
	// accept testing flags
	testing.Init()
	// parse additional flags
	flag.Parse()
	cfg.network, _ = coreumconfig.NetworkByChainID(constant.ChainIDDev)
	cfg.network.SetSDKConfig()
	cfg.clientCtx = client.NewContext(client.DefaultContextConfig(), config.NewModuleManager()).
		WithChainID(string(cfg.network.ChainID())).
		WithBroadcastMode(flags.BroadcastBlock)

	grpcClient, err := grpc.Dial(cfg.coredAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	must.OK(err)
	cfg.clientCtx = cfg.clientCtx.WithGRPCClient(grpcClient)
}

func TestTransferRequest(t *testing.T) {
	t.Parallel()

	log := zaptest.NewLogger(t)
	ctx := logger.WithLogger(context.Background(), log)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	t.Cleanup(cancel)
	address := sdk.AccAddress(secp256k1.GenPrivKey().PubKey().Address()).String()

	// request fund
	clientCtx := cfg.clientCtx
	txHash, err := requestFunds(ctx, address)
	require.NoError(t, err)
	require.Len(t, txHash, 64)

	_, err = client.AwaitTx(ctx, clientCtx, txHash)
	require.NoError(t, err)

	// query funds
	bankQueryClient := banktypes.NewQueryClient(clientCtx)
	resp, err := bankQueryClient.AllBalances(ctx, &banktypes.QueryAllBalancesRequest{Address: address})
	require.NoError(t, err)

	// make assertions
	assert.EqualValues(t, cfg.transferAmount, resp.Balances.AmountOf(cfg.network.Denom()).String())
}

func TestTransferRequestWithGenPrivkey(t *testing.T) {
	t.Parallel()

	log := zaptest.NewLogger(t)
	ctx := logger.WithLogger(context.Background(), log)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	t.Cleanup(cancel)

	// request fund
	clientCtx := cfg.clientCtx
	response, err := requestFundsWithPrivkey(ctx)
	require.NoError(t, err)
	require.Len(t, response.TxHash, 64)

	_, err = client.AwaitTx(ctx, clientCtx, response.TxHash)
	require.NoError(t, err)

	// query funds
	bankQueryClient := banktypes.NewQueryClient(clientCtx)
	resp, err := bankQueryClient.AllBalances(ctx, &banktypes.QueryAllBalancesRequest{Address: response.Address})
	require.NoError(t, err)

	// make assertions
	assert.EqualValues(t, cfg.transferAmount, resp.Balances.AmountOf(cfg.network.Denom()).String())
}

func TestTransferRequest_WrongAddress(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	address := "core1hrlnys435ph2gehthddlg2g2s246my30q0gfs2"

	// request fund
	clientCtx := cfg.clientCtx
	txHash, err := requestFunds(ctx, address)
	assert.Error(t, err)
	assert.Len(t, txHash, 0)

	// query funds
	bankQueryClient := banktypes.NewQueryClient(clientCtx)
	resp, err := bankQueryClient.AllBalances(ctx, &banktypes.QueryAllBalancesRequest{Address: address})
	require.Error(t, err)

	// make assertions
	assert.Nil(t, resp)
}

func requestFunds(ctx context.Context, address string) (string, error) {
	url := cfg.faucetAddress + "/api/faucet/v1/fund"
	method := "POST"

	sendMoneyReq := http.FundRequest{
		Address: address,
	}
	payloadBuffer := bytes.NewBuffer(nil)
	err := json.NewEncoder(payloadBuffer).Encode(sendMoneyReq)
	if err != nil {
		return "", errors.WithStack(err)
	}

	client := &nethttp.Client{}
	req, err := nethttp.NewRequestWithContext(ctx, method, url, payloadBuffer)
	if err != nil {
		return "", errors.WithStack(err)
	}

	req.Header.Add("Content-Type", "application/json")

	res, err := client.Do(req)
	if err != nil {
		return "", errors.WithStack(err)
	}
	defer res.Body.Close()
	if res.StatusCode > 299 {
		body, _ := io.ReadAll(res.Body)
		return "", errors.Errorf("non 2xx response, body: %s", body)
	}

	decoder := json.NewDecoder(res.Body)
	var sendMoneyResponse http.FundResponse
	err = decoder.Decode(&sendMoneyResponse)
	if err != nil {
		return "", errors.WithStack(err)
	}

	return sendMoneyResponse.TxHash, nil
}

func requestFundsWithPrivkey(ctx context.Context) (http.GenFundedResponse, error) {
	url := cfg.faucetAddress + "/api/faucet/v1/gen-funded"
	method := "POST"

	client := &nethttp.Client{}
	req, err := nethttp.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return http.GenFundedResponse{}, errors.WithStack(err)
	}

	res, err := client.Do(req)
	if err != nil {
		return http.GenFundedResponse{}, errors.WithStack(err)
	}
	defer res.Body.Close()
	if res.StatusCode > 299 {
		body, _ := io.ReadAll(res.Body)
		return http.GenFundedResponse{}, errors.Errorf("non 2xx response, body: %s", body)
	}

	decoder := json.NewDecoder(res.Body)
	var responseStruct http.GenFundedResponse
	err = decoder.Decode(&responseStruct)
	if err != nil {
		return http.GenFundedResponse{}, errors.WithStack(err)
	}

	return responseStruct, nil
}
