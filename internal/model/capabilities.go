package model

type LPCapabilities struct {
	ObservePoolLiquidity  string `json:"observe_pool_liquidity"`
	ObserveWalletPosition string `json:"observe_wallet_position"`
	Execute               string `json:"execute"`
}

func ReservedLPCapabilities() LPCapabilities {
	return LPCapabilities{ObservePoolLiquidity: "unknown", ObserveWalletPosition: "unknown", Execute: "not_implemented"}
}
