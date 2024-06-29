package main

type Config struct {
	Chains []struct {
		Name          string   `json:"name"`
		ChainID       string   `json:"chain-id"`
		AccountPrefix string   `json:"account-prefix"`
		RpcAddresses  []string `json:"rpc-addresses"`
		Timeout       string   `json:"timeout"`
	} `json:"chains"`
}
