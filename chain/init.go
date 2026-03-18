package chain

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
)

func init() {
	setAccountPrefixes("byb")
}

// DefaultPorts are the default ports the node listens on
var offset = 0
var DefaultPorts = Ports{
	RPC:        26657 + offset,
	P2P:        26656 + offset,
	API:        10337 + offset,
	GRPC:       9900 + offset,
	GRPCWeb:    9091 + offset,
	PProf:      6060 + offset,
	Prometheus: 26660 + offset,
	EVMRPC:     8545 + offset,
	EVMWSPort:  8546 + offset,
	ProxyApp:   26658 + offset,
}

func setAccountPrefixes(accountAddressPrefix string) {
	// Set prefixes
	accountPubKeyPrefix := accountAddressPrefix + "pub"
	validatorAddressPrefix := accountAddressPrefix + "valoper"
	validatorPubKeyPrefix := accountAddressPrefix + "valoperpub"
	consNodeAddressPrefix := accountAddressPrefix + "valcons"
	consNodePubKeyPrefix := accountAddressPrefix + "valconspub"

	// Set and seal config
	config := sdk.GetConfig()
	config.SetBech32PrefixForAccount(accountAddressPrefix, accountPubKeyPrefix)
	config.SetBech32PrefixForValidator(validatorAddressPrefix, validatorPubKeyPrefix)
	config.SetBech32PrefixForConsensusNode(consNodeAddressPrefix, consNodePubKeyPrefix)
	config.Seal()
}

func orPanic(err error) {
	if err != nil {
		panic(err)
	}
}

func bytesOrPanic(out []byte, err error) []byte {
	if err != nil {
		panic(err)
	}

	return out
}
