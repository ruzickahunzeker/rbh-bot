package rbhparser

import "github.com/ethereum/go-ethereum/common"

type AddressBook struct {
	PoolManager       common.Address
	UniversalRouter   common.Address
	WETH              common.Address
	USDG              common.Address
	PonsFactory       common.Address
	PonsLaunchAndBuy  common.Address
	PonsMemeHook      common.Address
	LongLauncher      common.Address
	O1Factory         common.Address
	O1Hook            common.Address
	PoolsEntry        common.Address
	PoolsTokenFactory common.Address
	PAIRLaunchpad     common.Address
	BagsFactory       common.Address
	BagsHook          common.Address
	LetsCashFactory   common.Address
	LetsCashHook      common.Address
	LetsCashRouter    common.Address
	FlapController    common.Address
	FlapPortalImpl    common.Address
	FlapNonTaxToken   common.Address
	FlapTaxTokenV3    common.Address
	UniswapV2Router   common.Address
	// Deprecated compatibility aliases. These are token implementations, not factories.
	FlapTaxFactory      common.Address
	FlapStocksFactory   common.Address
	GMGNRouter          common.Address
	GMGNRouterImpl      common.Address
	VaroLaunchpad       common.Address
	VaroRouter          common.Address
	VirtualsLaunchpad   common.Address
	VirtualsBondingImpl common.Address
	VirtualsFactory     common.Address
	VirtualsFactoryImpl common.Address
	VirtualsFRouter     common.Address
	VirtualsFRouterImpl common.Address
	VirtualsRouter      common.Address
	VirtualToken        common.Address
	WETHVirtualPair     common.Address
}

func DefaultAddressBook() AddressBook {
	return AddressBook{
		PoolManager:         common.HexToAddress("0x8366a39CC670B4001A1121B8F6A443A643e40951"),
		UniversalRouter:     common.HexToAddress("0x8876789976dEcBfCbBbe364623C63652db8C0904"),
		WETH:                common.HexToAddress("0x0Bd7D308f8E1639FAb988df18A8011F41EAcAD73"),
		USDG:                common.HexToAddress("0x5fc5360D0400a0Fd4f2af552ADD042D716F1d168"),
		PonsFactory:         common.HexToAddress("0x7eD598BcEf8bd9Edd8C97A195C6d13f40801EC7e"),
		PonsLaunchAndBuy:    common.HexToAddress("0xe33E9E479dF8802cb0866d5d05258bEc4cF62948"),
		PonsMemeHook:        common.HexToAddress("0xE5e702641Ea86F4ae6cC3cDaeD2B886f976Be044"),
		LongLauncher:        common.HexToAddress("0x22e99278308B393ea1260859B181AD7E78f5eeED"),
		O1Factory:           common.HexToAddress("0xcE9C48cFa068947f77738c81Be406B53338E5B0d"),
		O1Hook:              common.HexToAddress("0x0310cFEbE1D7A69f2414f6595bBe9d17c5342aCc"),
		PoolsEntry:          common.HexToAddress("0x0000FffFBE8efE702c8703aE3477FF5dE3d319C0"),
		PoolsTokenFactory:   common.HexToAddress("0x000000e200088D55C39a11F609E5F667729ad49b"),
		PAIRLaunchpad:       common.HexToAddress("0x8660A7F019C7943b0b0A91B8E39AFf3b6DB6Ae62"),
		BagsFactory:         common.HexToAddress("0xe8Cc4431adF8b5A847C113EF0c6af9043219Cb37"),
		BagsHook:            common.HexToAddress("0x2380aBf72C17aABAb76480244759AC7E2932EEcC"),
		LetsCashFactory:     common.HexToAddress("0x5bd1fBE78A78fe8236fA00cF48FBEba74AE34661"),
		LetsCashHook:        common.HexToAddress("0x75a54357D9C78A2DB19004A5fdc76C50f9242Aec"),
		LetsCashRouter:      common.HexToAddress("0x93AeC6502da90A30F1888C886918f3ba96Ab3fEC"),
		FlapController:      common.HexToAddress("0x26605f322f7ff986f381bB9a6e3f5DAb0BeaeB09"),
		FlapPortalImpl:      common.HexToAddress("0xa3b96Df56f254B926B17D5f7FB6CD858c216ff44"),
		FlapNonTaxToken:     common.HexToAddress("0x88882688a067FE97E11C2185b996286e53132222"),
		FlapTaxTokenV3:      common.HexToAddress("0x7777C8743C88B3aff3cf262135beF2c8b2e83333"),
		UniswapV2Router:     common.HexToAddress("0x89e5db8b5aa49aa85ac63f691524311aeb649eba"),
		FlapTaxFactory:      common.HexToAddress("0x7777C8743C88B3aff3cf262135beF2c8b2e83333"),
		FlapStocksFactory:   common.HexToAddress("0x88882688a067FE97E11C2185b996286e53132222"),
		GMGNRouter:          common.HexToAddress("0x65050A9B7E5075A2BA5Ced7b1B64EE66262C40Dc"),
		GMGNRouterImpl:      common.HexToAddress("0x133E48d1B5d44BC4465D6f132a56F53b35931Ba8"),
		VaroLaunchpad:       common.HexToAddress("0x851153fE84239C2DC55FA191ac2F099e20a6D0B8"),
		VaroRouter:          common.HexToAddress("0x3E257f38851B97090b40d7597A529cd7316E0ABC"),
		VirtualsLaunchpad:   common.HexToAddress("0xD4cCbfa37e2f35611B3042e4096AD7A3459BD007"),
		VirtualsBondingImpl: common.HexToAddress("0x66Fc520c7F316B8623eee2A5dA821c3b34D0539D"),
		VirtualsFactory:     common.HexToAddress("0xFC2E4Da3EdB2E18100473339c763705d263D20A9"),
		VirtualsFactoryImpl: common.HexToAddress("0x38b1A526f25b40bf7bb2a8c40957Ae3598C0356f"),
		VirtualsFRouter:     common.HexToAddress("0xCa6395246B4382Ba70F886526dD9a9De984F6081"),
		VirtualsFRouterImpl: common.HexToAddress("0x09256b9D607c53fD946681F7C5a7a4381ba285A1"),
		VirtualsRouter:      common.HexToAddress("0x94F0590bBaC0e9942b95275576319571aCFcD48F"),
		VirtualToken:        common.HexToAddress("0xC6911796042b15D7fA4f6cDE69E245DdCd3D9C31"),
		WETHVirtualPair:     common.HexToAddress("0xd95e8e2Cd04c207625C6F23c974d365a5F3A91D3"),
	}
}
