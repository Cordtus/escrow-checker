package main

import (
	"context"
	"log"
	"os"
	"strings"
	"time"

	"cosmossdk.io/store/rootmulti"
	abci "github.com/cometbft/cometbft/abci/types"
	client2 "github.com/cometbft/cometbft/rpc/client"
	sdktypes "github.com/cosmos/cosmos-sdk/types"
	legacyerrors "github.com/cosmos/cosmos-sdk/types/errors"
	querytypes "github.com/cosmos/cosmos-sdk/types/query"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	chantypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const paginationDelay = 10 * time.Millisecond

var logger = log.New(os.Stdout, "query: ", log.LstdFlags|log.Lshortfile)

func (c *Client) QueryBalances(ctx context.Context, addr string) (*banktypes.QueryAllBalancesResponse, error) {
	qc := banktypes.NewQueryClient(c)

	req := &banktypes.QueryAllBalancesRequest{
		Address:      addr,
		Pagination:   defaultPageRequest(),
		ResolveDenom: true,
	}

	res, err := qc.AllBalances(ctx, req, nil)
	if err != nil {
		logger.Printf("Error querying balances for address %s: %v", addr, err)
		return nil, err
	}

	logger.Printf("Successfully queried balances for address %s", addr)
	return res, nil
}

func (c *Client) QueryBalance(ctx context.Context, addr, denom string) (sdktypes.Coin, error) {
	qc := banktypes.NewQueryClient(c)

	req := &banktypes.QueryBalanceRequest{
		Address: addr,
		Denom:   denom,
	}

	res, err := qc.Balance(ctx, req, nil)
	if err != nil {
		logger.Printf("Error querying balance for address %s and denom %s: %v", addr, denom, err)
		return sdktypes.Coin{}, err
	}

	logger.Printf("Successfully queried balance for address %s and denom %s", addr, denom)
	return *res.Balance, nil
}

func (c *Client) QueryBankTotalSupply(ctx context.Context, denom string) (sdktypes.Coin, error) {
	qc := banktypes.NewQueryClient(c)
	req := &banktypes.QuerySupplyOfRequest{Denom: denom}

	res, err := qc.SupplyOf(ctx, req, nil)
	if err != nil {
		logger.Printf("Error querying total supply for denom %s: %v", denom, err)
		return sdktypes.Coin{}, err
	}

	logger.Printf("Successfully queried total supply for denom %s", denom)
	return res.Amount, nil
}

func (c *Client) QueryEscrowAddress(ctx context.Context, portID, channelID string) (string, error) {
	qc := transfertypes.NewQueryClient(c)

	req := &transfertypes.QueryEscrowAddressRequest{
		PortId:    portID,
		ChannelId: channelID,
	}

	res, err := qc.EscrowAddress(ctx, req, nil)
	if err != nil {
		logger.Printf("Error querying escrow address for port %s and channel %s: %v", portID, channelID, err)
		return "", err
	}

	logger.Printf("Successfully queried escrow address for port %s and channel %s", portID, channelID)
	return res.EscrowAddress, nil
}

func (c *Client) QueryTotalEscrowForDenom(ctx context.Context, denom string) (sdktypes.Coin, error) {
	qc := transfertypes.NewQueryClient(c)

	req := &transfertypes.QueryTotalEscrowForDenomRequest{
		Denom: denom,
	}

	res, err := qc.TotalEscrowForDenom(ctx, req)
	if err != nil {
		logger.Printf("Error querying total escrow for denom %s: %v", denom, err)
		return sdktypes.Coin{}, err
	}

	logger.Printf("Successfully queried total escrow for denom %s", denom)
	return res.Amount, nil
}

func (c *Client) QueryEscrowAmount(ctx context.Context, denom string) (sdktypes.Coin, error) {
	qc := transfertypes.NewQueryClient(c)
	req := &transfertypes.QueryTotalEscrowForDenomRequest{Denom: denom}

	res, err := qc.TotalEscrowForDenom(ctx, req, nil)
	if err != nil {
		logger.Printf("Error querying escrow amount for denom %s: %v", denom, err)
		return sdktypes.Coin{}, err
	}

	logger.Printf("Successfully queried escrow amount for denom %s", denom)
	return res.Amount, nil
}

func (c *Client) QueryDenomHash(ctx context.Context, denomTrace string) (string, error) {
	qc := transfertypes.NewQueryClient(c)

	req := &transfertypes.QueryDenomHashRequest{
		Trace: denomTrace,
	}

	res, err := qc.DenomHash(ctx, req, nil)
	if err != nil {
		logger.Printf("Error querying denom hash for trace %s: %v", denomTrace, err)
		return "", err
	}

	logger.Printf("Successfully queried denom hash for trace %s", denomTrace)
	return res.Hash, nil
}

func (c *Client) QueryDenomTrace(ctx context.Context, hash string) (*transfertypes.DenomTrace, error) {
	qc := transfertypes.NewQueryClient(c)
	req := &transfertypes.QueryDenomTraceRequest{Hash: hash}

	res, err := qc.DenomTrace(ctx, req, nil)
	if err != nil {
		logger.Printf("Error querying denom trace for hash %s: %v", hash, err)
		return nil, err
	}

	logger.Printf("Successfully queried denom trace for hash %s", hash)
	return res.DenomTrace, nil
}

func (c *Client) QueryDenomTraces(ctx context.Context, offset, limit uint64, height int64) ([]transfertypes.DenomTrace, error) {
	qc := transfertypes.NewQueryClient(c)
	p := defaultPageRequest()
	var transfers []transfertypes.DenomTrace

	for {
		res, err := qc.DenomTraces(ctx, &transfertypes.QueryDenomTracesRequest{Pagination: p})
		if err != nil || res == nil {
			logger.Printf("Error querying denom traces: %v", err)
			return nil, err
		}

		transfers = append(transfers, res.DenomTraces...)
		next := res.GetPagination().GetNextKey()
		if len(next) == 0 {
			break
		}

		time.Sleep(paginationDelay)
		p.Key = next
	}

	logger.Printf("Successfully queried denom traces")
	return transfers, nil
}

func (c *Client) QueryChannelClientState(portID, channelID string) (*chantypes.QueryChannelClientStateResponse, error) {
	qc := chantypes.NewQueryClient(c)

	req := &chantypes.QueryChannelClientStateRequest{
		PortId:    portID,
		ChannelId: channelID,
	}

	res, err := qc.ChannelClientState(context.Background(), req)
	if err != nil {
		logger.Printf("Error querying channel client state for port %s and channel %s: %v", portID, channelID, err)
		return nil, err
	}

	logger.Printf("Successfully queried channel client state for port %s and channel %s", portID, channelID)
	return res, nil
}

func (c *Client) QueryChannel(ctx context.Context, channelID string) (*chantypes.IdentifiedChannel, error) {
	qc := chantypes.NewQueryClient(c)

	const portID = "transfer"
	req := &chantypes.QueryChannelRequest{
		PortId:    portID,
		ChannelId: channelID,
	}

	resp, err := qc.Channel(ctx, req, nil)
	if err != nil {
		logger.Printf("Error querying channel for channelID %s: %v", channelID, err)
		return nil, err
	}

	ch := chantypes.NewIdentifiedChannel(portID, channelID, *resp.Channel)
	logger.Printf("Successfully queried channel for channelID %s", channelID)
	return &ch, nil
}

func (c *Client) QueryChannels(ctx context.Context) ([]*chantypes.IdentifiedChannel, error) {
	p := defaultPageRequest()
	var chans []*chantypes.IdentifiedChannel

	for {
		res, next, err := c.QueryChannelsPaginated(ctx, p)
		if err != nil {
			logger.Printf("Error querying channels: %v", err)
			return nil, err
		}

		chans = append(chans, res...)
		if len(next) == 0 {
			break
		}

		time.Sleep(paginationDelay)
		p.Key = next
	}

	logger.Printf("Successfully queried channels")
	return chans, nil
}

func (c *Client) QueryChannelsPaginated(ctx context.Context, pageRequest *querytypes.PageRequest) ([]*chantypes.IdentifiedChannel, []byte, error) {
	qc := chantypes.NewQueryClient(c)

	ctx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()

	res, err := qc.Channels(ctx, &chantypes.QueryChannelsRequest{Pagination: pageRequest})
	if err != nil {
		logger.Printf("Error querying channels with pagination: %v", err)
		return nil, nil, err
	}

	next := res.GetPagination().GetNextKey()
	logger.Printf("Successfully queried channels with pagination")
	return res.Channels, next, nil
}

func (c *Client) QueryABCI(ctx context.Context, req abci.RequestQuery) (abci.ResponseQuery, error) {
	opts := client2.ABCIQueryOptions{
		Height: req.Height,
		Prove:  req.Prove,
	}

	result, err := c.RPCClient.ABCIQueryWithOptions(ctx, req.Path, req.Data, opts)
	if err != nil {
		logger.Printf("Error querying ABCI: %v", err)
		return abci.ResponseQuery{}, err
	}

	if !result.Response.IsOK() {
		logger.Printf("Error response in ABCI query: %v", result.Response)
		return abci.ResponseQuery{}, sdkErrorToGRPCError(result.Response)
	}

	if !opts.Prove || !isQueryStoreWithProof(req.Path) {
		logger.Printf("Successfully queried ABCI without proof for path %s", req.Path)
		return result.Response, nil
	}

	logger.Printf("Successfully queried ABCI with proof for path %s", req.Path)
	return result.Response, nil
}

func sdkErrorToGRPCError(resp abci.ResponseQuery) error {
	switch resp.Code {
	case legacyerrors.ErrInvalidRequest.ABCICode():
		return status.Error(codes.InvalidArgument, resp.Log)
	case legacyerrors.ErrUnauthorized.ABCICode():
		return status.Error(codes.Unauthenticated, resp.Log)
	case legacyerrors.ErrKeyNotFound.ABCICode():
		return status.Error(codes.NotFound, resp.Log)
	default:
		return status.Error(codes.Unknown, resp.Log)
	}
}

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

func defaultPageRequest() *querytypes.PageRequest {
	return &querytypes.PageRequest{
		Key:        []byte(""),
		Offset:     0,
		Limit:      1000,
		CountTotal: false,
	}
}
