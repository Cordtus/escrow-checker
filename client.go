package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"reflect"
	"strconv"
	"time"

	sdkerrors "cosmossdk.io/errors"
	abci "github.com/cometbft/cometbft/abci/types"
	rpcclient "github.com/cometbft/cometbft/rpc/client"
	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
	libclient "github.com/cometbft/cometbft/rpc/jsonrpc/client"
	"github.com/cosmos/cosmos-sdk/codec/types"
	legacyerrors "github.com/cosmos/cosmos-sdk/types/errors"
	grpctypes "github.com/cosmos/cosmos-sdk/types/grpc"
	"github.com/cosmos/cosmos-sdk/types/tx"
	gogogrpc "github.com/cosmos/gogoproto/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/encoding/proto"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var _ gogogrpc.ClientConn = &Client{}

var protoCodec = encoding.GetCodec(proto.Name)

type Client struct {
	ChainID       string
	Address       string
	RPCClient     rpcclient.Client
	AccountPrefix string
	Cdc           Codec
	Timeout       time.Duration
}

type Clients []*Client

func (c Clients) clientByChainID(chainID string) (*Client, error) {
	for _, client := range c {
		if client.ChainID == chainID {
			return client, nil
		}
	}

	return nil, fmt.Errorf("client with chain ID %s is not configured, check config and re-run the program", chainID)
}

func NewClient(chainID, rpcAddr, accountPrefix string, timeout time.Duration) *Client {
	rpcClient, err := NewRPCClient(rpcAddr, timeout)
	if err != nil {
		panic(err)
	}

	return &Client{
		ChainID:       chainID,
		Address:       rpcAddr,
		RPCClient:     rpcClient,
		AccountPrefix: accountPrefix,
		Cdc:           MakeCodec(ModuleBasics, accountPrefix, accountPrefix+"valoper"),
	}
}

func isHealthy(address string) bool {
	fmt.Printf("Checking health of address: %s\n", address)

	parsedURL, err := url.Parse(address)
	if err != nil {
		fmt.Printf("Failed to parse URL: %s, error: %v\n", address, err)
		return false
	}

	host := parsedURL.Hostname()
	port := parsedURL.Port()
	fmt.Printf("Parsed host: %s, port: %s\n", host, port)

	if port == "" {
		if parsedURL.Scheme == "http" {
			port = "80"
		} else if parsedURL.Scheme == "https" {
			port = "443"
		}
		fmt.Printf("Defaulted port to: %s\n", port)
	}

	// Check if the host is an IP or domain
	if net.ParseIP(host) != nil {
		fmt.Printf("Host is an IP address: %s\n", host)
		// Host is an IP, do a ping check
		pinger := exec.Command("ping", "-c", "1", host)
		if err := pinger.Run(); err != nil {
			fmt.Printf("Ping to IP failed: %s, error: %v\n", host, err)
			return false
		}
	} else {
		fmt.Printf("Host is a domain: %s\n", host)
		// Host is a domain, do a DNS lookup
		if _, err := net.LookupHost(host); err != nil {
			fmt.Printf("DNS lookup failed for domain: %s, error: %v\n", host, err)
			return false
		}
	}

	// Check the sync status of the node
	statusURL := fmt.Sprintf("%s://%s:%s/status", parsedURL.Scheme, host, port)
	fmt.Printf("Checking status URL: %s\n", statusURL)
	resp, err := http.Get(statusURL)
	if err != nil {
		fmt.Printf("HTTP request to %s failed: %v\n", statusURL, err)
		return false
	}
	defer resp.Body.Close()

	var result struct {
		Result struct {
			SyncInfo struct {
				LatestBlockTime string `json:"latest_block_time"`
			} `json:"sync_info"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Printf("Failed to decode JSON response from %s: %v\n", statusURL, err)
		return false
	}

	blockTime, err := time.Parse(time.RFC3339, result.Result.SyncInfo.LatestBlockTime)
	if err != nil {
		fmt.Printf("Failed to parse latest_block_time: %s, error: %v\n", result.Result.SyncInfo.LatestBlockTime, err)
		return false
	}

	if time.Since(blockTime) > 60*time.Second {
		fmt.Printf("Block time is more than 60 seconds old: %s\n", blockTime)
		return false
	}

	fmt.Printf("Address %s is healthy\n", address)
	return true
}

// Invoke implements the grpc ClientConn.Invoke method
func (c *Client) Invoke(ctx context.Context, method string, req, reply interface{}, opts ...grpc.CallOption) (err error) {
	// Two things can happen here:
	// 1. either we're broadcasting a Tx, in which call we call Tendermint's broadcast endpoint directly,
	// 2. or we are querying for state, in which case we call ABCI's Querier.

	// In both cases, we don't allow empty request req (it will panic unexpectedly).
	if reflect.ValueOf(req).IsNil() {
		return sdkerrors.Wrap(legacyerrors.ErrInvalidRequest, "request cannot be nil")
	}

	// Case 1. Broadcasting a Tx.
	if reqProto, ok := req.(*tx.BroadcastTxRequest); ok {
		if !ok {
			return sdkerrors.Wrapf(legacyerrors.ErrInvalidRequest, "expected %T, got %T", (*tx.BroadcastTxRequest)(nil), req)
		}
		resProto, ok := reply.(*tx.BroadcastTxResponse)
		if !ok {
			return sdkerrors.Wrapf(legacyerrors.ErrInvalidRequest, "expected %T, got %T", (*tx.BroadcastTxResponse)(nil), req)
		}

		broadcastRes, err := c.TxServiceBroadcast(ctx, reqProto)
		if err != nil {
			return err
		}
		*resProto = *broadcastRes
		return err
	}

	// Case 2. Querying state.
	inMd, _ := metadata.FromOutgoingContext(ctx)
	abciRes, outMd, err := c.RunGRPCQuery(ctx, method, req, inMd)
	if err != nil {
		return err
	}

	if err = protoCodec.Unmarshal(abciRes.Value, reply); err != nil {
		return err
	}

	for _, callOpt := range opts {
		header, ok := callOpt.(grpc.HeaderCallOption)
		if !ok {
			continue
		}

		*header.HeaderAddr = outMd
	}

	if c.Cdc.InterfaceRegistry != nil {
		return types.UnpackInterfaces(reply, c.Cdc.Marshaler)
	}

	return nil
}

// NewStream implements the grpc ClientConn.NewStream method
func (c *Client) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	return nil, fmt.Errorf("streaming rpc not supported")
}

// RunGRPCQuery runs a gRPC query from the clientCtx, given all necessary
// arguments for the gRPC method, and returns the ABCI response. It is used
// to factorize code between client (Invoke) and server (RegisterGRPCServer)
// gRPC handlers.
func (c *Client) RunGRPCQuery(ctx context.Context, method string, req interface{}, md metadata.MD) (abci.ResponseQuery, metadata.MD, error) {
	reqBz, err := protoCodec.Marshal(req)
	if err != nil {
		return abci.ResponseQuery{}, nil, err
	}

	// parse height header
	if heights := md.Get(grpctypes.GRPCBlockHeightHeader); len(heights) > 0 {
		height, err := strconv.ParseInt(heights[0], 10, 64)
		if err != nil {
			return abci.ResponseQuery{}, nil, err
		}
		if height < 0 {
			return abci.ResponseQuery{}, nil, sdkerrors.Wrapf(
				legacyerrors.ErrInvalidRequest,
				"client.Context.Invoke: height (%d) from %q must be >= 0", height, grpctypes.GRPCBlockHeightHeader)
		}

	}

	height, err := GetHeightFromMetadata(md)
	if err != nil {
		return abci.ResponseQuery{}, nil, err
	}

	prove, err := GetProveFromMetadata(md)
	if err != nil {
		return abci.ResponseQuery{}, nil, err
	}

	abciReq := abci.RequestQuery{
		Path:   method,
		Data:   reqBz,
		Height: height,
		Prove:  prove,
	}

	abciRes, err := c.QueryABCI(ctx, abciReq)
	if err != nil {
		return abci.ResponseQuery{}, nil, err
	}

	// Create header metadata. For now the headers contain:
	// - block height
	// We then parse all the call options, if the call option is a
	// HeaderCallOption, then we manually set the value of that header to the
	// metadata.
	md = metadata.Pairs(grpctypes.GRPCBlockHeightHeader, strconv.FormatInt(abciRes.Height, 10))

	return abciRes, md, nil
}

// TxServiceBroadcast is a helper function to broadcast a Tx with the correct gRPC types
// from the tx service. Calls `clientCtx.BroadcastTx` under the hood.
func (c *Client) TxServiceBroadcast(ctx context.Context, req *tx.BroadcastTxRequest) (*tx.BroadcastTxResponse, error) {
	if req == nil || req.TxBytes == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid empty tx")
	}

	//var (
	//	blockTimeout = defaultBroadcastWaitTimeout
	//	err          error
	//	rlyResp      *provider.RelayerTxResponse
	//	callbackErr  error
	//	wg           sync.WaitGroup
	//)
	//
	//if cc.PCfg.BlockTimeout != "" {
	//	blockTimeout, err = time.ParseDuration(cc.PCfg.BlockTimeout)
	//	if err != nil {
	//		// Did you call Validate() method on CosmosProviderConfig struct
	//		// before coming here?
	//		return nil, err
	//	}
	//}
	//
	//callback := func(rtr *provider.RelayerTxResponse, err error) {
	//	rlyResp = rtr
	//	callbackErr = err
	//	wg.Done()
	//}
	//
	//wg.Add(1)
	//
	//if err := cc.broadcastTx(ctx, req.TxBytes, nil, nil, ctx, blockTimeout, []func(*provider.RelayerTxResponse, error){callback}); err != nil {
	//	return nil, err
	//}
	//
	//wg.Wait()

	//if callbackErr != nil {
	//	return nil, callbackErr
	//}
	//
	//return &tx.BroadcastTxResponse{
	//	TxResponse: &sdk.TxResponse{
	//		Height:    rlyResp.Height,
	//		TxHash:    rlyResp.TxHash,
	//		Codespace: rlyResp.Codespace,
	//		Code:      rlyResp.Code,
	//		Data:      rlyResp.Data,
	//	},
	//}, nil
	return nil, nil
}

func GetHeightFromMetadata(md metadata.MD) (int64, error) {
	height := md.Get(grpctypes.GRPCBlockHeightHeader)
	if len(height) == 1 {
		return strconv.ParseInt(height[0], 10, 64)
	}
	return 0, nil
}

func GetProveFromMetadata(md metadata.MD) (bool, error) {
	prove := md.Get("x-cosmos-query-prove")
	if len(prove) == 1 {
		return strconv.ParseBool(prove[0])
	}
	return false, nil
}

func NewRPCClient(addr string, timeout time.Duration) (*rpchttp.HTTP, error) {
	httpClient, err := libclient.DefaultHTTPClient(addr)
	if err != nil {
		return nil, err
	}
	httpClient.Timeout = timeout
	rpcClient, err := rpchttp.NewWithClient(addr, "/websocket", httpClient)
	if err != nil {
		return nil, err
	}
	return rpcClient, nil
}
