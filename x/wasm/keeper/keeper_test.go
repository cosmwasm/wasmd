package keeper

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/CosmWasm/wasmd/x/wasm/keeper/wasmtesting"
	wasmvm "github.com/CosmWasm/wasmvm"
	wasmvmtypes "github.com/CosmWasm/wasmvm/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"io/ioutil"
	"math"
	"testing"
	"time"

	"github.com/CosmWasm/wasmd/x/wasm/types"
	stypes "github.com/cosmos/cosmos-sdk/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
)

const SupportedFeatures = "staking,stargate"

func TestNewKeeper(t *testing.T) {
	_, keepers := CreateTestInput(t, false, SupportedFeatures)
	require.NotNil(t, keepers.ContractKeeper)
}

func TestCreate(t *testing.T) {
	ctx, keepers := CreateTestInput(t, false, SupportedFeatures)
	accKeeper, keeper, bankKeeper := keepers.AccountKeeper, keepers.ContractKeeper, keepers.BankKeeper

	deposit := sdk.NewCoins(sdk.NewInt64Coin("denom", 100000))
	creator := createFakeFundedAccount(t, ctx, accKeeper, bankKeeper, deposit)

	wasmCode, err := ioutil.ReadFile("./testdata/hackatom.wasm")
	require.NoError(t, err)

	contractID, err := keeper.Create(ctx, creator, wasmCode, "https://github.com/CosmWasm/wasmd/blob/master/x/wasm/testdata/escrow.wasm", "any/builder:tag", nil)
	require.NoError(t, err)
	require.Equal(t, uint64(1), contractID)
	// and verify content
	storedCode, err := keepers.WasmKeeper.GetByteCode(ctx, contractID)
	require.NoError(t, err)
	require.Equal(t, wasmCode, storedCode)
}

func TestCreateStoresInstantiatePermission(t *testing.T) {
	wasmCode, err := ioutil.ReadFile("./testdata/hackatom.wasm")
	require.NoError(t, err)
	var (
		deposit                = sdk.NewCoins(sdk.NewInt64Coin("denom", 100000))
		myAddr  sdk.AccAddress = bytes.Repeat([]byte{1}, sdk.AddrLen)
	)

	specs := map[string]struct {
		srcPermission types.AccessType
		expInstConf   types.AccessConfig
	}{
		"default": {
			srcPermission: types.DefaultParams().InstantiateDefaultPermission,
			expInstConf:   types.AllowEverybody,
		},
		"everybody": {
			srcPermission: types.AccessTypeEverybody,
			expInstConf:   types.AllowEverybody,
		},
		"nobody": {
			srcPermission: types.AccessTypeNobody,
			expInstConf:   types.AllowNobody,
		},
		"onlyAddress with matching address": {
			srcPermission: types.AccessTypeOnlyAddress,
			expInstConf:   types.AccessConfig{Permission: types.AccessTypeOnlyAddress, Address: myAddr.String()},
		},
	}
	for msg, spec := range specs {
		t.Run(msg, func(t *testing.T) {
			ctx, keepers := CreateTestInput(t, false, SupportedFeatures)
			accKeeper, keeper, bankKeeper := keepers.AccountKeeper, keepers.ContractKeeper, keepers.BankKeeper
			keepers.WasmKeeper.setParams(ctx, types.Params{
				CodeUploadAccess:             types.AllowEverybody,
				InstantiateDefaultPermission: spec.srcPermission,
				MaxWasmCodeSize:              types.DefaultMaxWasmCodeSize,
			})
			fundAccounts(t, ctx, accKeeper, bankKeeper, myAddr, deposit)

			codeID, err := keeper.Create(ctx, myAddr, wasmCode, "https://github.com/CosmWasm/wasmd/blob/master/x/wasm/testdata/escrow.wasm", "any/builder:tag", nil)
			require.NoError(t, err)

			codeInfo := keepers.WasmKeeper.GetCodeInfo(ctx, codeID)
			require.NotNil(t, codeInfo)
			assert.True(t, spec.expInstConf.Equals(codeInfo.InstantiateConfig), "got %#v", codeInfo.InstantiateConfig)
		})
	}
}

func TestCreateWithParamPermissions(t *testing.T) {
	ctx, keepers := CreateTestInput(t, false, SupportedFeatures)
	accKeeper, bankKeeper, keeper := keepers.AccountKeeper, keepers.BankKeeper, keepers.ContractKeeper

	deposit := sdk.NewCoins(sdk.NewInt64Coin("denom", 100000))
	creator := createFakeFundedAccount(t, ctx, accKeeper, bankKeeper, deposit)
	otherAddr := createFakeFundedAccount(t, ctx, accKeeper, bankKeeper, deposit)

	wasmCode, err := ioutil.ReadFile("./testdata/hackatom.wasm")
	require.NoError(t, err)

	specs := map[string]struct {
		srcPermission types.AccessConfig
		expError      *sdkerrors.Error
	}{
		"default": {
			srcPermission: types.DefaultUploadAccess,
		},
		"everybody": {
			srcPermission: types.AllowEverybody,
		},
		"nobody": {
			srcPermission: types.AllowNobody,
			expError:      sdkerrors.ErrUnauthorized,
		},
		"onlyAddress with matching address": {
			srcPermission: types.AccessTypeOnlyAddress.With(creator),
		},
		"onlyAddress with non matching address": {
			srcPermission: types.AccessTypeOnlyAddress.With(otherAddr),
			expError:      sdkerrors.ErrUnauthorized,
		},
	}
	for msg, spec := range specs {
		t.Run(msg, func(t *testing.T) {
			params := types.DefaultParams()
			params.CodeUploadAccess = spec.srcPermission
			keepers.WasmKeeper.setParams(ctx, params)
			_, err := keeper.Create(ctx, creator, wasmCode, "https://github.com/CosmWasm/wasmd/blob/master/x/wasm/testdata/escrow.wasm", "any/builder:tag", nil)
			require.True(t, spec.expError.Is(err), err)
			if spec.expError != nil {
				return
			}
		})
	}
}

func TestCreateDuplicate(t *testing.T) {
	ctx, keepers := CreateTestInput(t, false, SupportedFeatures)
	accKeeper, keeper, bankKeeper := keepers.AccountKeeper, keepers.ContractKeeper, keepers.BankKeeper

	deposit := sdk.NewCoins(sdk.NewInt64Coin("denom", 100000))
	creator := createFakeFundedAccount(t, ctx, accKeeper, bankKeeper, deposit)

	wasmCode, err := ioutil.ReadFile("./testdata/hackatom.wasm")
	require.NoError(t, err)

	// create one copy
	contractID, err := keeper.Create(ctx, creator, wasmCode, "https://github.com/CosmWasm/wasmd/blob/master/x/wasm/testdata/escrow.wasm", "any/builder:tag", nil)
	require.NoError(t, err)
	require.Equal(t, uint64(1), contractID)

	// create second copy
	duplicateID, err := keeper.Create(ctx, creator, wasmCode, "https://github.com/CosmWasm/wasmd/blob/master/x/wasm/testdata/escrow.wasm", "any/builder:tag", nil)
	require.NoError(t, err)
	require.Equal(t, uint64(2), duplicateID)

	// and verify both content is proper
	storedCode, err := keepers.WasmKeeper.GetByteCode(ctx, contractID)
	require.NoError(t, err)
	require.Equal(t, wasmCode, storedCode)
	storedCode, err = keepers.WasmKeeper.GetByteCode(ctx, duplicateID)
	require.NoError(t, err)
	require.Equal(t, wasmCode, storedCode)
}

func TestCreateWithSimulation(t *testing.T) {
	ctx, keepers := CreateTestInput(t, false, SupportedFeatures)
	accKeeper, keeper, bankKeeper := keepers.AccountKeeper, keepers.ContractKeeper, keepers.BankKeeper

	ctx = ctx.WithBlockHeader(tmproto.Header{Height: 1}).
		WithGasMeter(stypes.NewInfiniteGasMeter())

	deposit := sdk.NewCoins(sdk.NewInt64Coin("denom", 100000))
	creator := createFakeFundedAccount(t, ctx, accKeeper, bankKeeper, deposit)

	wasmCode, err := ioutil.ReadFile("./testdata/hackatom.wasm")
	require.NoError(t, err)

	// create this once in simulation mode
	contractID, err := keeper.Create(ctx, creator, wasmCode, "https://github.com/CosmWasm/wasmd/blob/master/x/wasm/testdata/escrow.wasm", "any/builder:tag", nil)
	require.NoError(t, err)
	require.Equal(t, uint64(1), contractID)

	// then try to create it in non-simulation mode (should not fail)
	ctx, keepers = CreateTestInput(t, false, SupportedFeatures)
	accKeeper, keeper = keepers.AccountKeeper, keepers.ContractKeeper
	contractID, err = keeper.Create(ctx, creator, wasmCode, "https://github.com/CosmWasm/wasmd/blob/master/x/wasm/testdata/escrow.wasm", "any/builder:tag", nil)
	require.NoError(t, err)
	require.Equal(t, uint64(1), contractID)

	// and verify content
	code, err := keepers.WasmKeeper.GetByteCode(ctx, contractID)
	require.NoError(t, err)
	require.Equal(t, code, wasmCode)
}

func TestIsSimulationMode(t *testing.T) {
	specs := map[string]struct {
		ctx sdk.Context
		exp bool
	}{
		"genesis block": {
			ctx: sdk.Context{}.WithBlockHeader(tmproto.Header{}).WithGasMeter(stypes.NewInfiniteGasMeter()),
			exp: false,
		},
		"any regular block": {
			ctx: sdk.Context{}.WithBlockHeader(tmproto.Header{Height: 1}).WithGasMeter(stypes.NewGasMeter(10000000)),
			exp: false,
		},
		"simulation": {
			ctx: sdk.Context{}.WithBlockHeader(tmproto.Header{Height: 1}).WithGasMeter(stypes.NewInfiniteGasMeter()),
			exp: true,
		},
	}
	for msg := range specs {
		t.Run(msg, func(t *testing.T) {
			//assert.Equal(t, spec.exp, isSimulationMode(spec.ctx))
		})
	}
}

func TestCreateWithGzippedPayload(t *testing.T) {
	ctx, keepers := CreateTestInput(t, false, SupportedFeatures)
	accKeeper, keeper, bankKeeper := keepers.AccountKeeper, keepers.ContractKeeper, keepers.BankKeeper

	deposit := sdk.NewCoins(sdk.NewInt64Coin("denom", 100000))
	creator := createFakeFundedAccount(t, ctx, accKeeper, bankKeeper, deposit)

	wasmCode, err := ioutil.ReadFile("./testdata/hackatom.wasm.gzip")
	require.NoError(t, err)

	contractID, err := keeper.Create(ctx, creator, wasmCode, "https://github.com/CosmWasm/wasmd/blob/master/x/wasm/testdata/escrow.wasm", "", nil)
	require.NoError(t, err)
	require.Equal(t, uint64(1), contractID)
	// and verify content
	storedCode, err := keepers.WasmKeeper.GetByteCode(ctx, contractID)
	require.NoError(t, err)
	rawCode, err := ioutil.ReadFile("./testdata/hackatom.wasm")
	require.NoError(t, err)
	require.Equal(t, rawCode, storedCode)
}

func TestInstantiate(t *testing.T) {
	ctx, keepers := CreateTestInput(t, false, SupportedFeatures)
	accKeeper, keeper, bankKeeper := keepers.AccountKeeper, keepers.ContractKeeper, keepers.BankKeeper

	deposit := sdk.NewCoins(sdk.NewInt64Coin("denom", 100000))
	creator := createFakeFundedAccount(t, ctx, accKeeper, bankKeeper, deposit)

	wasmCode, err := ioutil.ReadFile("./testdata/hackatom.wasm")
	require.NoError(t, err)

	codeID, err := keeper.Create(ctx, creator, wasmCode, "https://github.com/CosmWasm/wasmd/blob/master/x/wasm/testdata/escrow.wasm", "", nil)
	require.NoError(t, err)

	_, _, bob := keyPubAddr()
	_, _, fred := keyPubAddr()

	initMsg := HackatomExampleInitMsg{
		Verifier:    fred,
		Beneficiary: bob,
	}
	initMsgBz, err := json.Marshal(initMsg)
	require.NoError(t, err)

	gasBefore := ctx.GasMeter().GasConsumed()

	// create with no balance is also legal
	gotContractAddr, _, err := keepers.ContractKeeper.Instantiate(ctx, codeID, creator, nil, initMsgBz, "demo contract 1", nil)
	require.NoError(t, err)
	require.Equal(t, "cosmos18vd8fpwxzck93qlwghaj6arh4p7c5n89uzcee5", gotContractAddr.String())

	gasAfter := ctx.GasMeter().GasConsumed()
	if types.EnableGasVerification {
		require.Equal(t, uint64(0x12361), gasAfter-gasBefore)
	}

	// ensure it is stored properly
	info := keepers.WasmKeeper.GetContractInfo(ctx, gotContractAddr)
	require.NotNil(t, info)
	assert.Equal(t, creator.String(), info.Creator)
	assert.Equal(t, codeID, info.CodeID)
	assert.Equal(t, "demo contract 1", info.Label)

	exp := []types.ContractCodeHistoryEntry{{
		Operation: types.ContractCodeHistoryOperationTypeInit,
		CodeID:    codeID,
		Updated:   types.NewAbsoluteTxPosition(ctx),
		Msg:       json.RawMessage(initMsgBz),
	}}
	assert.Equal(t, exp, keepers.WasmKeeper.GetContractHistory(ctx, gotContractAddr))
}

func TestInstantiateWithDeposit(t *testing.T) {
	wasmCode, err := ioutil.ReadFile("./testdata/hackatom.wasm")
	require.NoError(t, err)

	var (
		bob  = bytes.Repeat([]byte{1}, sdk.AddrLen)
		fred = bytes.Repeat([]byte{2}, sdk.AddrLen)

		deposit = sdk.NewCoins(sdk.NewInt64Coin("denom", 100))
		initMsg = HackatomExampleInitMsg{Verifier: fred, Beneficiary: bob}
	)

	initMsgBz, err := json.Marshal(initMsg)
	require.NoError(t, err)

	specs := map[string]struct {
		srcActor sdk.AccAddress
		expError bool
		fundAddr bool
	}{
		"address with funds": {
			srcActor: bob,
			fundAddr: true,
		},
		"address without funds": {
			srcActor: bob,
			expError: true,
		},
		"blocked address": {
			srcActor: authtypes.NewModuleAddress(authtypes.FeeCollectorName),
			fundAddr: true,
			expError: true,
		},
	}
	for msg, spec := range specs {
		t.Run(msg, func(t *testing.T) {
			ctx, keepers := CreateTestInput(t, false, SupportedFeatures)
			accKeeper, bankKeeper, keeper := keepers.AccountKeeper, keepers.BankKeeper, keepers.ContractKeeper

			if spec.fundAddr {
				fundAccounts(t, ctx, accKeeper, bankKeeper, spec.srcActor, sdk.NewCoins(sdk.NewInt64Coin("denom", 200)))
			}
			contractID, err := keeper.Create(ctx, spec.srcActor, wasmCode, "https://github.com/CosmWasm/wasmd/blob/master/x/wasm/testdata/escrow.wasm", "", nil)
			require.NoError(t, err)

			// when
			addr, _, err := keepers.ContractKeeper.Instantiate(ctx, contractID, spec.srcActor, nil, initMsgBz, "my label", deposit)
			// then
			if spec.expError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			balances := bankKeeper.GetAllBalances(ctx, addr)
			assert.Equal(t, deposit, balances)
		})
	}
}

func TestInstantiateWithPermissions(t *testing.T) {
	wasmCode, err := ioutil.ReadFile("./testdata/hackatom.wasm")
	require.NoError(t, err)

	var (
		deposit   = sdk.NewCoins(sdk.NewInt64Coin("denom", 100000))
		myAddr    = bytes.Repeat([]byte{1}, sdk.AddrLen)
		otherAddr = bytes.Repeat([]byte{2}, sdk.AddrLen)
		anyAddr   = bytes.Repeat([]byte{3}, sdk.AddrLen)
	)

	initMsg := HackatomExampleInitMsg{
		Verifier:    anyAddr,
		Beneficiary: anyAddr,
	}
	initMsgBz, err := json.Marshal(initMsg)
	require.NoError(t, err)

	specs := map[string]struct {
		srcPermission types.AccessConfig
		srcActor      sdk.AccAddress
		expError      *sdkerrors.Error
	}{
		"default": {
			srcPermission: types.DefaultUploadAccess,
			srcActor:      anyAddr,
		},
		"everybody": {
			srcPermission: types.AllowEverybody,
			srcActor:      anyAddr,
		},
		"nobody": {
			srcPermission: types.AllowNobody,
			srcActor:      myAddr,
			expError:      sdkerrors.ErrUnauthorized,
		},
		"onlyAddress with matching address": {
			srcPermission: types.AccessTypeOnlyAddress.With(myAddr),
			srcActor:      myAddr,
		},
		"onlyAddress with non matching address": {
			srcPermission: types.AccessTypeOnlyAddress.With(otherAddr),
			expError:      sdkerrors.ErrUnauthorized,
		},
	}
	for msg, spec := range specs {
		t.Run(msg, func(t *testing.T) {
			ctx, keepers := CreateTestInput(t, false, SupportedFeatures)
			accKeeper, bankKeeper, keeper := keepers.AccountKeeper, keepers.BankKeeper, keepers.ContractKeeper
			fundAccounts(t, ctx, accKeeper, bankKeeper, spec.srcActor, deposit)

			contractID, err := keeper.Create(ctx, myAddr, wasmCode, "https://github.com/CosmWasm/wasmd/blob/master/x/wasm/testdata/escrow.wasm", "", &spec.srcPermission)
			require.NoError(t, err)

			_, _, err = keepers.ContractKeeper.Instantiate(ctx, contractID, spec.srcActor, nil, initMsgBz, "demo contract 1", nil)
			assert.True(t, spec.expError.Is(err), "got %+v", err)
		})
	}
}

func TestInstantiateWithNonExistingCodeID(t *testing.T) {
	ctx, keepers := CreateTestInput(t, false, SupportedFeatures)
	accKeeper, bankKeeper := keepers.AccountKeeper, keepers.BankKeeper

	deposit := sdk.NewCoins(sdk.NewInt64Coin("denom", 100000))
	creator := createFakeFundedAccount(t, ctx, accKeeper, bankKeeper, deposit)

	initMsg := HackatomExampleInitMsg{}
	initMsgBz, err := json.Marshal(initMsg)
	require.NoError(t, err)

	const nonExistingCodeID = 9999
	addr, _, err := keepers.ContractKeeper.Instantiate(ctx, nonExistingCodeID, creator, nil, initMsgBz, "demo contract 2", nil)
	require.True(t, types.ErrNotFound.Is(err), err)
	require.Nil(t, addr)
}

func TestInstantiateWithContractDataResponse(t *testing.T) {
	ctx, keepers := CreateTestInput(t, false, SupportedFeatures)

	wasmerMock := &wasmtesting.MockWasmer{
		InstantiateFn: func(codeID wasmvm.Checksum, env wasmvmtypes.Env, info wasmvmtypes.MessageInfo, initMsg []byte, store wasmvm.KVStore, goapi wasmvm.GoAPI, querier wasmvm.Querier, gasMeter wasmvm.GasMeter, gasLimit uint64, deserCost wasmvmtypes.UFraction) (*wasmvmtypes.Response, uint64, error) {
			return &wasmvmtypes.Response{Data: []byte("my-response-data")}, 0, nil
		},
		AnalyzeCodeFn: wasmtesting.WithoutIBCAnalyzeFn,
		CreateFn:      wasmtesting.NoOpCreateFn,
	}

	example := StoreRandomContract(t, ctx, keepers, wasmerMock)
	_, data, err := keepers.ContractKeeper.Instantiate(ctx, example.CodeID, example.CreatorAddr, nil, nil, "test", nil)
	require.NoError(t, err)
	assert.Equal(t, []byte("my-response-data"), data)
}

func TestExecute(t *testing.T) {
	ctx, keepers := CreateTestInput(t, false, SupportedFeatures)
	accKeeper, keeper, bankKeeper := keepers.AccountKeeper, keepers.ContractKeeper, keepers.BankKeeper

	deposit := sdk.NewCoins(sdk.NewInt64Coin("denom", 100000))
	topUp := sdk.NewCoins(sdk.NewInt64Coin("denom", 5000))
	creator := createFakeFundedAccount(t, ctx, accKeeper, bankKeeper, deposit.Add(deposit...))
	fred := createFakeFundedAccount(t, ctx, accKeeper, bankKeeper, topUp)

	wasmCode, err := ioutil.ReadFile("./testdata/hackatom.wasm")
	require.NoError(t, err)

	contractID, err := keeper.Create(ctx, creator, wasmCode, "", "", nil)
	require.NoError(t, err)

	_, _, bob := keyPubAddr()
	initMsg := HackatomExampleInitMsg{
		Verifier:    fred,
		Beneficiary: bob,
	}
	initMsgBz, err := json.Marshal(initMsg)
	require.NoError(t, err)

	addr, _, err := keepers.ContractKeeper.Instantiate(ctx, contractID, creator, nil, initMsgBz, "demo contract 3", deposit)
	require.NoError(t, err)
	require.Equal(t, "cosmos18vd8fpwxzck93qlwghaj6arh4p7c5n89uzcee5", addr.String())

	// ensure bob doesn't exist
	bobAcct := accKeeper.GetAccount(ctx, bob)
	require.Nil(t, bobAcct)

	// ensure funder has reduced balance
	creatorAcct := accKeeper.GetAccount(ctx, creator)
	require.NotNil(t, creatorAcct)
	// we started at 2*deposit, should have spent one above
	assert.Equal(t, deposit, bankKeeper.GetAllBalances(ctx, creatorAcct.GetAddress()))

	// ensure contract has updated balance
	contractAcct := accKeeper.GetAccount(ctx, addr)
	require.NotNil(t, contractAcct)
	assert.Equal(t, deposit, bankKeeper.GetAllBalances(ctx, contractAcct.GetAddress()))

	// unauthorized - trialCtx so we don't change state
	trialCtx := ctx.WithMultiStore(ctx.MultiStore().CacheWrap().(sdk.MultiStore))
	res, err := keepers.ContractKeeper.Execute(trialCtx, addr, creator, []byte(`{"release":{}}`), nil)
	require.Error(t, err)
	require.True(t, errors.Is(err, types.ErrExecuteFailed))
	require.Equal(t, "Unauthorized: execute wasm contract failed", err.Error())

	// verifier can execute, and get proper gas amount
	start := time.Now()
	gasBefore := ctx.GasMeter().GasConsumed()

	res, err = keepers.ContractKeeper.Execute(ctx, addr, fred, []byte(`{"release":{}}`), topUp)
	diff := time.Now().Sub(start)
	require.NoError(t, err)
	require.NotNil(t, res)

	// make sure gas is properly deducted from ctx
	gasAfter := ctx.GasMeter().GasConsumed()
	if types.EnableGasVerification {
		require.Equal(t, uint64(0x12afd), gasAfter-gasBefore)
	}
	// ensure bob now exists and got both payments released
	bobAcct = accKeeper.GetAccount(ctx, bob)
	require.NotNil(t, bobAcct)
	balance := bankKeeper.GetAllBalances(ctx, bobAcct.GetAddress())
	assert.Equal(t, deposit.Add(topUp...), balance)

	// ensure contract has updated balance
	contractAcct = accKeeper.GetAccount(ctx, addr)
	require.NotNil(t, contractAcct)
	assert.Equal(t, sdk.Coins(nil), bankKeeper.GetAllBalances(ctx, contractAcct.GetAddress()))

	t.Logf("Duration: %v (%d gas)\n", diff, gasAfter-gasBefore)
}

func TestExecuteWithDeposit(t *testing.T) {
	wasmCode, err := ioutil.ReadFile("./testdata/hackatom.wasm")
	require.NoError(t, err)

	var (
		bob         = bytes.Repeat([]byte{1}, sdk.AddrLen)
		fred        = bytes.Repeat([]byte{2}, sdk.AddrLen)
		blockedAddr = authtypes.NewModuleAddress(authtypes.FeeCollectorName)
		deposit     = sdk.NewCoins(sdk.NewInt64Coin("denom", 100))
	)

	specs := map[string]struct {
		srcActor      sdk.AccAddress
		beneficiary   sdk.AccAddress
		newBankParams *banktypes.Params
		expError      bool
		fundAddr      bool
	}{
		"actor with funds": {
			srcActor:    bob,
			fundAddr:    true,
			beneficiary: fred,
		},
		"actor without funds": {
			srcActor:    bob,
			beneficiary: fred,
			expError:    true,
		},
		"blocked address as actor": {
			srcActor:    blockedAddr,
			fundAddr:    true,
			beneficiary: fred,
			expError:    true,
		},
		"coin transfer with all transfers disabled": {
			srcActor:      bob,
			fundAddr:      true,
			beneficiary:   fred,
			newBankParams: &banktypes.Params{DefaultSendEnabled: false},
			expError:      true,
		},
		"coin transfer with transfer denom disabled": {
			srcActor:    bob,
			fundAddr:    true,
			beneficiary: fred,
			newBankParams: &banktypes.Params{
				DefaultSendEnabled: true,
				SendEnabled:        []*banktypes.SendEnabled{{Denom: "denom", Enabled: false}},
			},
			expError: true,
		},
		"blocked address as beneficiary": {
			srcActor:    bob,
			fundAddr:    true,
			beneficiary: blockedAddr,
			expError:    true,
		},
	}
	for msg, spec := range specs {
		t.Run(msg, func(t *testing.T) {
			ctx, keepers := CreateTestInput(t, false, SupportedFeatures)
			accKeeper, bankKeeper, keeper := keepers.AccountKeeper, keepers.BankKeeper, keepers.ContractKeeper
			if spec.newBankParams != nil {
				bankKeeper.SetParams(ctx, *spec.newBankParams)
			}
			if spec.fundAddr {
				fundAccounts(t, ctx, accKeeper, bankKeeper, spec.srcActor, sdk.NewCoins(sdk.NewInt64Coin("denom", 200)))
			}
			codeID, err := keeper.Create(ctx, spec.srcActor, wasmCode, "https://example.com/escrow.wasm", "", nil)
			require.NoError(t, err)

			initMsg := HackatomExampleInitMsg{Verifier: spec.srcActor, Beneficiary: spec.beneficiary}
			initMsgBz, err := json.Marshal(initMsg)
			require.NoError(t, err)

			contractAddr, _, err := keepers.ContractKeeper.Instantiate(ctx, codeID, spec.srcActor, nil, initMsgBz, "my label", nil)
			require.NoError(t, err)

			// when
			_, err = keepers.ContractKeeper.Execute(ctx, contractAddr, spec.srcActor, []byte(`{"release":{}}`), deposit)

			// then
			if spec.expError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			balances := bankKeeper.GetAllBalances(ctx, spec.beneficiary)
			assert.Equal(t, deposit, balances)
		})
	}
}

func TestExecuteWithNonExistingAddress(t *testing.T) {
	ctx, keepers := CreateTestInput(t, false, SupportedFeatures)
	accKeeper, keeper, bankKeeper := keepers.AccountKeeper, keepers.ContractKeeper, keepers.BankKeeper

	deposit := sdk.NewCoins(sdk.NewInt64Coin("denom", 100000))
	creator := createFakeFundedAccount(t, ctx, accKeeper, bankKeeper, deposit.Add(deposit...))

	// unauthorized - trialCtx so we don't change state
	nonExistingAddress := addrFromUint64(9999)
	_, err := keeper.Execute(ctx, nonExistingAddress, creator, []byte(`{}`), nil)
	require.True(t, types.ErrNotFound.Is(err), err)
}

func TestExecuteWithPanic(t *testing.T) {
	ctx, keepers := CreateTestInput(t, false, SupportedFeatures)
	accKeeper, keeper, bankKeeper := keepers.AccountKeeper, keepers.ContractKeeper, keepers.BankKeeper

	deposit := sdk.NewCoins(sdk.NewInt64Coin("denom", 100000))
	topUp := sdk.NewCoins(sdk.NewInt64Coin("denom", 5000))
	creator := createFakeFundedAccount(t, ctx, accKeeper, bankKeeper, deposit.Add(deposit...))
	fred := createFakeFundedAccount(t, ctx, accKeeper, bankKeeper, topUp)

	wasmCode, err := ioutil.ReadFile("./testdata/hackatom.wasm")
	require.NoError(t, err)

	contractID, err := keeper.Create(ctx, creator, wasmCode, "", "", nil)
	require.NoError(t, err)

	_, _, bob := keyPubAddr()
	initMsg := HackatomExampleInitMsg{
		Verifier:    fred,
		Beneficiary: bob,
	}
	initMsgBz, err := json.Marshal(initMsg)
	require.NoError(t, err)

	addr, _, err := keepers.ContractKeeper.Instantiate(ctx, contractID, creator, nil, initMsgBz, "demo contract 4", deposit)
	require.NoError(t, err)

	// let's make sure we get a reasonable error, no panic/crash
	_, err = keepers.ContractKeeper.Execute(ctx, addr, fred, []byte(`{"panic":{}}`), topUp)
	require.Error(t, err)
	require.True(t, errors.Is(err, types.ErrExecuteFailed))
	// test with contains as "Display" implementation of the Wasmer "RuntimeError" is different for Mac and Linux
	assert.Contains(t, err.Error(), "Error calling the VM: Error executing Wasm: Wasmer runtime error: RuntimeError: unreachable")
}

func TestExecuteWithCpuLoop(t *testing.T) {
	ctx, keepers := CreateTestInput(t, false, SupportedFeatures)
	accKeeper, keeper, bankKeeper := keepers.AccountKeeper, keepers.ContractKeeper, keepers.BankKeeper

	deposit := sdk.NewCoins(sdk.NewInt64Coin("denom", 100000))
	topUp := sdk.NewCoins(sdk.NewInt64Coin("denom", 5000))
	creator := createFakeFundedAccount(t, ctx, accKeeper, bankKeeper, deposit.Add(deposit...))
	fred := createFakeFundedAccount(t, ctx, accKeeper, bankKeeper, topUp)

	wasmCode, err := ioutil.ReadFile("./testdata/hackatom.wasm")
	require.NoError(t, err)

	contractID, err := keeper.Create(ctx, creator, wasmCode, "", "", nil)
	require.NoError(t, err)

	_, _, bob := keyPubAddr()
	initMsg := HackatomExampleInitMsg{
		Verifier:    fred,
		Beneficiary: bob,
	}
	initMsgBz, err := json.Marshal(initMsg)
	require.NoError(t, err)

	addr, _, err := keepers.ContractKeeper.Instantiate(ctx, contractID, creator, nil, initMsgBz, "demo contract 5", deposit)
	require.NoError(t, err)

	// make sure we set a limit before calling
	var gasLimit uint64 = 400_000
	ctx = ctx.WithGasMeter(sdk.NewGasMeter(gasLimit))
	require.Equal(t, uint64(0), ctx.GasMeter().GasConsumed())

	// ensure we get an out of gas panic
	defer func() {
		r := recover()
		require.NotNil(t, r)
		_, ok := r.(sdk.ErrorOutOfGas)
		require.True(t, ok, "%v", r)
	}()

	// this should throw out of gas exception (panic)
	_, err = keepers.ContractKeeper.Execute(ctx, addr, fred, []byte(`{"cpu_loop":{}}`), nil)
	require.True(t, false, "We must panic before this line")

}

func TestExecuteWithStorageLoop(t *testing.T) {
	ctx, keepers := CreateTestInput(t, false, SupportedFeatures)
	accKeeper, keeper, bankKeeper := keepers.AccountKeeper, keepers.ContractKeeper, keepers.BankKeeper

	deposit := sdk.NewCoins(sdk.NewInt64Coin("denom", 100000))
	topUp := sdk.NewCoins(sdk.NewInt64Coin("denom", 5000))
	creator := createFakeFundedAccount(t, ctx, accKeeper, bankKeeper, deposit.Add(deposit...))
	fred := createFakeFundedAccount(t, ctx, accKeeper, bankKeeper, topUp)

	wasmCode, err := ioutil.ReadFile("./testdata/hackatom.wasm")
	require.NoError(t, err)

	contractID, err := keeper.Create(ctx, creator, wasmCode, "", "", nil)
	require.NoError(t, err)

	_, _, bob := keyPubAddr()
	initMsg := HackatomExampleInitMsg{
		Verifier:    fred,
		Beneficiary: bob,
	}
	initMsgBz, err := json.Marshal(initMsg)
	require.NoError(t, err)

	addr, _, err := keepers.ContractKeeper.Instantiate(ctx, contractID, creator, nil, initMsgBz, "demo contract 6", deposit)
	require.NoError(t, err)

	// make sure we set a limit before calling
	var gasLimit uint64 = 400_002
	ctx = ctx.WithGasMeter(sdk.NewGasMeter(gasLimit))
	require.Equal(t, uint64(0), ctx.GasMeter().GasConsumed())

	// ensure we get an out of gas panic
	defer func() {
		r := recover()
		require.NotNil(t, r)
		_, ok := r.(sdk.ErrorOutOfGas)
		require.True(t, ok, "%v", r)
	}()

	// this should throw out of gas exception (panic)
	_, err = keepers.ContractKeeper.Execute(ctx, addr, fred, []byte(`{"storage_loop":{}}`), nil)
	require.True(t, false, "We must panic before this line")
}

func TestMigrate(t *testing.T) {
	ctx, keepers := CreateTestInput(t, false, SupportedFeatures)
	accKeeper, keeper, bankKeeper := keepers.AccountKeeper, keepers.ContractKeeper, keepers.BankKeeper

	deposit := sdk.NewCoins(sdk.NewInt64Coin("denom", 100000))
	topUp := sdk.NewCoins(sdk.NewInt64Coin("denom", 5000))
	creator := createFakeFundedAccount(t, ctx, accKeeper, bankKeeper, deposit.Add(deposit...))
	fred := createFakeFundedAccount(t, ctx, accKeeper, bankKeeper, topUp)

	originalCodeID := StoreHackatomExampleContract(t, ctx, keepers).CodeID
	newCodeID := StoreHackatomExampleContract(t, ctx, keepers).CodeID
	ibcCodeID := StoreIBCReflectContract(t, ctx, keepers).CodeID
	require.NotEqual(t, originalCodeID, newCodeID)

	anyAddr := RandomAccountAddress(t)
	newVerifierAddr := RandomAccountAddress(t)
	initMsgBz := HackatomExampleInitMsg{
		Verifier:    fred,
		Beneficiary: anyAddr,
	}.GetBytes(t)

	migMsg := struct {
		Verifier sdk.AccAddress `json:"verifier"`
	}{Verifier: newVerifierAddr}
	migMsgBz, err := json.Marshal(migMsg)
	require.NoError(t, err)

	specs := map[string]struct {
		admin                sdk.AccAddress
		overrideContractAddr sdk.AccAddress
		caller               sdk.AccAddress
		fromCodeID           uint64
		toCodeID             uint64
		migrateMsg           []byte
		expErr               *sdkerrors.Error
		expVerifier          sdk.AccAddress
		expIBCPort           bool
		initMsg              []byte
	}{
		"all good with same code id": {
			admin:       creator,
			caller:      creator,
			initMsg:     initMsgBz,
			fromCodeID:  originalCodeID,
			toCodeID:    originalCodeID,
			migrateMsg:  migMsgBz,
			expVerifier: newVerifierAddr,
		},
		"all good with different code id": {
			admin:       creator,
			caller:      creator,
			initMsg:     initMsgBz,
			fromCodeID:  originalCodeID,
			toCodeID:    newCodeID,
			migrateMsg:  migMsgBz,
			expVerifier: newVerifierAddr,
		},
		"all good with admin set": {
			admin:       fred,
			caller:      fred,
			initMsg:     initMsgBz,
			fromCodeID:  originalCodeID,
			toCodeID:    newCodeID,
			migrateMsg:  migMsgBz,
			expVerifier: newVerifierAddr,
		},
		"adds IBC port for IBC enabled contracts": {
			admin:       fred,
			caller:      fred,
			initMsg:     initMsgBz,
			fromCodeID:  originalCodeID,
			toCodeID:    ibcCodeID,
			migrateMsg:  []byte(`{}`),
			expIBCPort:  true,
			expVerifier: fred, // not updated
		},
		"prevent migration when admin was not set on instantiate": {
			caller:     creator,
			initMsg:    initMsgBz,
			fromCodeID: originalCodeID,
			toCodeID:   originalCodeID,
			expErr:     sdkerrors.ErrUnauthorized,
		},
		"prevent migration when not sent by admin": {
			caller:     creator,
			admin:      fred,
			initMsg:    initMsgBz,
			fromCodeID: originalCodeID,
			toCodeID:   originalCodeID,
			expErr:     sdkerrors.ErrUnauthorized,
		},
		"fail with non existing code id": {
			admin:      creator,
			caller:     creator,
			initMsg:    initMsgBz,
			fromCodeID: originalCodeID,
			toCodeID:   99999,
			expErr:     sdkerrors.ErrInvalidRequest,
		},
		"fail with non existing contract addr": {
			admin:                creator,
			caller:               creator,
			initMsg:              initMsgBz,
			overrideContractAddr: anyAddr,
			fromCodeID:           originalCodeID,
			toCodeID:             originalCodeID,
			expErr:               sdkerrors.ErrInvalidRequest,
		},
		"fail in contract with invalid migrate msg": {
			admin:      creator,
			caller:     creator,
			initMsg:    initMsgBz,
			fromCodeID: originalCodeID,
			toCodeID:   originalCodeID,
			migrateMsg: bytes.Repeat([]byte{0x1}, 7),
			expErr:     types.ErrMigrationFailed,
		},
		"fail in contract without migrate msg": {
			admin:      creator,
			caller:     creator,
			initMsg:    initMsgBz,
			fromCodeID: originalCodeID,
			toCodeID:   originalCodeID,
			expErr:     types.ErrMigrationFailed,
		},
		"fail when no IBC callbacks": {
			admin:      fred,
			caller:     fred,
			initMsg:    IBCReflectInitMsg{ReflectCodeID: StoreReflectContract(t, ctx, keepers)}.GetBytes(t),
			fromCodeID: ibcCodeID,
			toCodeID:   newCodeID,
			migrateMsg: migMsgBz,
			expErr:     types.ErrMigrationFailed,
		},
	}

	for msg, spec := range specs {
		t.Run(msg, func(t *testing.T) {
			// given a contract instance
			ctx = ctx.WithBlockHeight(ctx.BlockHeight() + 1)
			contractAddr, _, err := keepers.ContractKeeper.Instantiate(ctx, spec.fromCodeID, creator, spec.admin, spec.initMsg, "demo contract", nil)
			require.NoError(t, err)
			if spec.overrideContractAddr != nil {
				contractAddr = spec.overrideContractAddr
			}
			// when
			_, err = keeper.Migrate(ctx, contractAddr, spec.caller, spec.toCodeID, spec.migrateMsg)

			// then
			require.True(t, spec.expErr.Is(err), "expected %v but got %+v", spec.expErr, err)
			if spec.expErr != nil {
				return
			}
			cInfo := keepers.WasmKeeper.GetContractInfo(ctx, contractAddr)
			assert.Equal(t, spec.toCodeID, cInfo.CodeID)
			assert.Equal(t, spec.expIBCPort, cInfo.IBCPortID != "", cInfo.IBCPortID)

			expHistory := []types.ContractCodeHistoryEntry{{
				Operation: types.ContractCodeHistoryOperationTypeInit,
				CodeID:    spec.fromCodeID,
				Updated:   types.NewAbsoluteTxPosition(ctx),
				Msg:       initMsgBz,
			}, {
				Operation: types.ContractCodeHistoryOperationTypeMigrate,
				CodeID:    spec.toCodeID,
				Updated:   types.NewAbsoluteTxPosition(ctx),
				Msg:       spec.migrateMsg,
			}}
			assert.Equal(t, expHistory, keepers.WasmKeeper.GetContractHistory(ctx, contractAddr))

			// and verify contract state
			raw := keepers.WasmKeeper.QueryRaw(ctx, contractAddr, []byte("config"))
			var stored map[string]string
			require.NoError(t, json.Unmarshal(raw, &stored))
			require.Contains(t, stored, "verifier")
			require.NoError(t, err)
			assert.Equal(t, spec.expVerifier.String(), stored["verifier"])
		})
	}
}

func TestMigrateReplacesTheSecondIndex(t *testing.T) {
	ctx, keepers := CreateTestInput(t, false, SupportedFeatures)
	example := InstantiateHackatomExampleContract(t, ctx, keepers)

	// then assert a second index exists
	store := ctx.KVStore(keepers.WasmKeeper.storeKey)
	oldContractInfo := keepers.WasmKeeper.GetContractInfo(ctx, example.Contract)
	require.NotNil(t, oldContractInfo)
	createHistoryEntry := types.ContractCodeHistoryEntry{
		CodeID:  example.CodeID,
		Updated: types.NewAbsoluteTxPosition(ctx),
	}
	exists := store.Has(types.GetContractByCreatedSecondaryIndexKey(example.Contract, createHistoryEntry))
	require.True(t, exists)

	ctx = ctx.WithBlockHeight(ctx.BlockHeight() + 1) // increment for different block
	// when do migrate
	newCodeExample := StoreBurnerExampleContract(t, ctx, keepers)
	migMsgBz := BurnerExampleInitMsg{Payout: example.CreatorAddr}.GetBytes(t)
	_, err := keepers.ContractKeeper.Migrate(ctx, example.Contract, example.CreatorAddr, newCodeExample.CodeID, migMsgBz)
	require.NoError(t, err)

	// then the new index exists
	migrateHistoryEntry := types.ContractCodeHistoryEntry{
		CodeID:  newCodeExample.CodeID,
		Updated: types.NewAbsoluteTxPosition(ctx),
	}
	exists = store.Has(types.GetContractByCreatedSecondaryIndexKey(example.Contract, migrateHistoryEntry))
	require.True(t, exists)
	// and the old index was removed
	exists = store.Has(types.GetContractByCreatedSecondaryIndexKey(example.Contract, createHistoryEntry))
	require.False(t, exists)
}

func TestMigrateWithDispatchedMessage(t *testing.T) {
	ctx, keepers := CreateTestInput(t, false, SupportedFeatures)
	accKeeper, keeper, bankKeeper := keepers.AccountKeeper, keepers.ContractKeeper, keepers.BankKeeper

	deposit := sdk.NewCoins(sdk.NewInt64Coin("denom", 100000))
	creator := createFakeFundedAccount(t, ctx, accKeeper, bankKeeper, deposit.Add(deposit...))
	fred := createFakeFundedAccount(t, ctx, accKeeper, bankKeeper, sdk.NewCoins(sdk.NewInt64Coin("denom", 5000)))

	wasmCode, err := ioutil.ReadFile("./testdata/hackatom.wasm")
	require.NoError(t, err)
	burnerCode, err := ioutil.ReadFile("./testdata/burner.wasm")
	require.NoError(t, err)

	originalContractID, err := keeper.Create(ctx, creator, wasmCode, "", "", nil)
	require.NoError(t, err)
	burnerContractID, err := keeper.Create(ctx, creator, burnerCode, "", "", nil)
	require.NoError(t, err)
	require.NotEqual(t, originalContractID, burnerContractID)

	_, _, myPayoutAddr := keyPubAddr()
	initMsg := HackatomExampleInitMsg{
		Verifier:    fred,
		Beneficiary: fred,
	}
	initMsgBz := initMsg.GetBytes(t)

	ctx = ctx.WithBlockHeight(ctx.BlockHeight() + 1)
	contractAddr, _, err := keepers.ContractKeeper.Instantiate(ctx, originalContractID, creator, fred, initMsgBz, "demo contract", deposit)
	require.NoError(t, err)

	migMsgBz := BurnerExampleInitMsg{Payout: myPayoutAddr}.GetBytes(t)
	ctx = ctx.WithEventManager(sdk.NewEventManager()).WithBlockHeight(ctx.BlockHeight() + 1)
	data, err := keeper.Migrate(ctx, contractAddr, fred, burnerContractID, migMsgBz)
	require.NoError(t, err)
	assert.Equal(t, "burnt 1 keys", string(data))
	type dict map[string]interface{}
	expEvents := []dict{
		{
			"Type": "wasm",
			"Attr": []dict{
				{"contract_address": contractAddr},
				{"action": "burn"},
				{"payout": myPayoutAddr},
			},
		},
		{
			"Type": "transfer",
			"Attr": []dict{
				{"recipient": myPayoutAddr},
				{"sender": contractAddr},
				{"amount": "100000denom"},
			},
		},
		{
			"Type": "message",
			"Attr": []dict{
				{"sender": contractAddr},
			},
		},
		{
			"Type": "message",
			"Attr": []dict{
				{"module": "bank"},
			},
		},
	}
	expJSONEvts := string(mustMarshal(t, expEvents))
	assert.JSONEq(t, expJSONEvts, prettyEvents(t, ctx.EventManager().Events()))

	// all persistent data cleared
	m := keepers.WasmKeeper.QueryRaw(ctx, contractAddr, []byte("config"))
	require.Len(t, m, 0)

	// and all deposit tokens sent to myPayoutAddr
	balance := bankKeeper.GetAllBalances(ctx, myPayoutAddr)
	assert.Equal(t, deposit, balance)
}

func TestIterateContractsByCode(t *testing.T) {
	ctx, keepers := CreateTestInput(t, false, SupportedFeatures)
	k, c := keepers.WasmKeeper, keepers.ContractKeeper
	example1 := InstantiateHackatomExampleContract(t, ctx, keepers)
	ctx = ctx.WithBlockHeight(ctx.BlockHeight() + 1)
	example2 := InstantiateIBCReflectContract(t, ctx, keepers)
	ctx = ctx.WithBlockHeight(ctx.BlockHeight() + 1)
	initMsg := HackatomExampleInitMsg{
		Verifier:    RandomAccountAddress(t),
		Beneficiary: RandomAccountAddress(t),
	}.GetBytes(t)
	contractAddr3, _, err := c.Instantiate(ctx, example1.CodeID, example1.CreatorAddr, nil, initMsg, "foo", nil)
	require.NoError(t, err)
	specs := map[string]struct {
		codeID uint64
		exp    []sdk.AccAddress
	}{
		"multiple results": {
			codeID: example1.CodeID,
			exp:    []sdk.AccAddress{example1.Contract, contractAddr3},
		},
		"single results": {
			codeID: example2.CodeID,
			exp:    []sdk.AccAddress{example2.Contract},
		},
		"empty results": {
			codeID: 99999,
		},
	}
	for name, spec := range specs {
		t.Run(name, func(t *testing.T) {
			var gotAddr []sdk.AccAddress
			k.IterateContractsByCode(ctx, spec.codeID, func(address sdk.AccAddress) bool {
				gotAddr = append(gotAddr, address)
				return false
			})
			assert.Equal(t, spec.exp, gotAddr)
		})
	}
}

func TestIterateContractsByCodeWithMigration(t *testing.T) {
	// mock migration so that it does not fail when migrate example1 to example2.codeID
	mockWasmVM := wasmtesting.MockWasmer{MigrateFn: func(codeID wasmvm.Checksum, env wasmvmtypes.Env, migrateMsg []byte, store wasmvm.KVStore, goapi wasmvm.GoAPI, querier wasmvm.Querier, gasMeter wasmvm.GasMeter, gasLimit uint64, deserCost wasmvmtypes.UFraction) (*wasmvmtypes.Response, uint64, error) {
		return &wasmvmtypes.Response{}, 1, nil
	}}
	wasmtesting.MakeInstantiable(&mockWasmVM)
	ctx, keepers := CreateTestInput(t, false, SupportedFeatures, WithWasmEngine(&mockWasmVM))
	k, c := keepers.WasmKeeper, keepers.ContractKeeper
	example1 := InstantiateHackatomExampleContract(t, ctx, keepers)
	ctx = ctx.WithBlockHeight(ctx.BlockHeight() + 1)
	example2 := InstantiateIBCReflectContract(t, ctx, keepers)
	ctx = ctx.WithBlockHeight(ctx.BlockHeight() + 1)
	_, err := c.Migrate(ctx, example1.Contract, example1.CreatorAddr, example2.CodeID, []byte("{}"))
	require.NoError(t, err)

	// when
	var gotAddr []sdk.AccAddress
	k.IterateContractsByCode(ctx, example2.CodeID, func(address sdk.AccAddress) bool {
		gotAddr = append(gotAddr, address)
		return false
	})

	// then
	exp := []sdk.AccAddress{example2.Contract, example1.Contract}
	assert.Equal(t, exp, gotAddr)
}

type sudoMsg struct {
	// This is a tongue-in-check demo command. This is not the intended purpose of Sudo.
	// Here we show that some priviledged Go module can make a call that should never be exposed
	// to end users (via Tx/Execute).
	//
	// The contract developer can choose to expose anything to sudo. This functionality is not a true
	// backdoor (it can never be called by end users), but allows the developers of the native blockchain
	// code to make special calls. This can also be used as an authentication mechanism, if you want to expose
	// some callback that only can be triggered by some system module and not faked by external users.
	StealFunds stealFundsMsg `json:"steal_funds"`
}

type stealFundsMsg struct {
	Recipient string            `json:"recipient"`
	Amount    wasmvmtypes.Coins `json:"amount"`
}

func TestSudo(t *testing.T) {
	ctx, keepers := CreateTestInput(t, false, SupportedFeatures)
	accKeeper, keeper, bankKeeper := keepers.AccountKeeper, keepers.ContractKeeper, keepers.BankKeeper

	deposit := sdk.NewCoins(sdk.NewInt64Coin("denom", 100000))
	creator := createFakeFundedAccount(t, ctx, accKeeper, bankKeeper, deposit.Add(deposit...))

	wasmCode, err := ioutil.ReadFile("./testdata/hackatom.wasm")
	require.NoError(t, err)
	contractID, err := keeper.Create(ctx, creator, wasmCode, "", "", nil)
	require.NoError(t, err)

	_, _, bob := keyPubAddr()
	_, _, fred := keyPubAddr()
	initMsg := HackatomExampleInitMsg{
		Verifier:    fred,
		Beneficiary: bob,
	}
	initMsgBz, err := json.Marshal(initMsg)
	require.NoError(t, err)

	addr, _, err := keepers.ContractKeeper.Instantiate(ctx, contractID, creator, nil, initMsgBz, "demo contract 3", deposit)
	require.NoError(t, err)
	require.Equal(t, "cosmos18vd8fpwxzck93qlwghaj6arh4p7c5n89uzcee5", addr.String())

	// the community is broke
	_, _, community := keyPubAddr()
	comAcct := accKeeper.GetAccount(ctx, community)
	require.Nil(t, comAcct)

	// now the community wants to get paid via sudo
	msg := sudoMsg{
		// This is a tongue-in-check demo command. This is not the intended purpose of Sudo.
		// Here we show that some priviledged Go module can make a call that should never be exposed
		// to end users (via Tx/Execute).
		StealFunds: stealFundsMsg{
			Recipient: community.String(),
			Amount:    wasmvmtypes.Coins{wasmvmtypes.NewCoin(76543, "denom")},
		},
	}
	sudoMsg, err := json.Marshal(msg)
	require.NoError(t, err)

	_, err = keepers.WasmKeeper.Sudo(ctx, addr, sudoMsg)
	require.NoError(t, err)

	// ensure community now exists and got paid
	comAcct = accKeeper.GetAccount(ctx, community)
	require.NotNil(t, comAcct)
	balance := bankKeeper.GetBalance(ctx, comAcct.GetAddress(), "denom")
	assert.Equal(t, sdk.NewInt64Coin("denom", 76543), balance)
}

func prettyEvents(t *testing.T, events sdk.Events) string {
	t.Helper()
	type prettyEvent struct {
		Type string
		Attr []map[string]string
	}

	r := make([]prettyEvent, len(events))
	for i, e := range events {
		attr := make([]map[string]string, len(e.Attributes))
		for j, a := range e.Attributes {
			attr[j] = map[string]string{string(a.Key): string(a.Value)}
		}
		r[i] = prettyEvent{Type: e.Type, Attr: attr}
	}
	return string(mustMarshal(t, r))
}

func mustMarshal(t *testing.T, r interface{}) []byte {
	t.Helper()
	bz, err := json.Marshal(r)
	require.NoError(t, err)
	return bz
}

func TestUpdateContractAdmin(t *testing.T) {
	ctx, keepers := CreateTestInput(t, false, SupportedFeatures)
	accKeeper, keeper, bankKeeper := keepers.AccountKeeper, keepers.ContractKeeper, keepers.BankKeeper

	deposit := sdk.NewCoins(sdk.NewInt64Coin("denom", 100000))
	topUp := sdk.NewCoins(sdk.NewInt64Coin("denom", 5000))
	creator := createFakeFundedAccount(t, ctx, accKeeper, bankKeeper, deposit.Add(deposit...))
	fred := createFakeFundedAccount(t, ctx, accKeeper, bankKeeper, topUp)

	wasmCode, err := ioutil.ReadFile("./testdata/hackatom.wasm")
	require.NoError(t, err)

	originalContractID, err := keeper.Create(ctx, creator, wasmCode, "", "", nil)
	require.NoError(t, err)

	_, _, anyAddr := keyPubAddr()
	initMsg := HackatomExampleInitMsg{
		Verifier:    fred,
		Beneficiary: anyAddr,
	}
	initMsgBz, err := json.Marshal(initMsg)
	require.NoError(t, err)
	specs := map[string]struct {
		instAdmin            sdk.AccAddress
		newAdmin             sdk.AccAddress
		overrideContractAddr sdk.AccAddress
		caller               sdk.AccAddress
		expErr               *sdkerrors.Error
	}{
		"all good with admin set": {
			instAdmin: fred,
			newAdmin:  anyAddr,
			caller:    fred,
		},
		"prevent update when admin was not set on instantiate": {
			caller:   creator,
			newAdmin: fred,
			expErr:   sdkerrors.ErrUnauthorized,
		},
		"prevent updates from non admin address": {
			instAdmin: creator,
			newAdmin:  fred,
			caller:    fred,
			expErr:    sdkerrors.ErrUnauthorized,
		},
		"fail with non existing contract addr": {
			instAdmin:            creator,
			newAdmin:             anyAddr,
			caller:               creator,
			overrideContractAddr: anyAddr,
			expErr:               sdkerrors.ErrInvalidRequest,
		},
	}
	for msg, spec := range specs {
		t.Run(msg, func(t *testing.T) {
			addr, _, err := keepers.ContractKeeper.Instantiate(ctx, originalContractID, creator, spec.instAdmin, initMsgBz, "demo contract", nil)
			require.NoError(t, err)
			if spec.overrideContractAddr != nil {
				addr = spec.overrideContractAddr
			}
			err = keeper.UpdateContractAdmin(ctx, addr, spec.caller, spec.newAdmin)
			require.True(t, spec.expErr.Is(err), "expected %v but got %+v", spec.expErr, err)
			if spec.expErr != nil {
				return
			}
			cInfo := keepers.WasmKeeper.GetContractInfo(ctx, addr)
			assert.Equal(t, spec.newAdmin.String(), cInfo.Admin)
		})
	}
}

func TestClearContractAdmin(t *testing.T) {
	ctx, keepers := CreateTestInput(t, false, SupportedFeatures)
	accKeeper, keeper, bankKeeper := keepers.AccountKeeper, keepers.ContractKeeper, keepers.BankKeeper

	deposit := sdk.NewCoins(sdk.NewInt64Coin("denom", 100000))
	topUp := sdk.NewCoins(sdk.NewInt64Coin("denom", 5000))
	creator := createFakeFundedAccount(t, ctx, accKeeper, bankKeeper, deposit.Add(deposit...))
	fred := createFakeFundedAccount(t, ctx, accKeeper, bankKeeper, topUp)

	wasmCode, err := ioutil.ReadFile("./testdata/hackatom.wasm")
	require.NoError(t, err)

	originalContractID, err := keeper.Create(ctx, creator, wasmCode, "", "", nil)
	require.NoError(t, err)

	_, _, anyAddr := keyPubAddr()
	initMsg := HackatomExampleInitMsg{
		Verifier:    fred,
		Beneficiary: anyAddr,
	}
	initMsgBz, err := json.Marshal(initMsg)
	require.NoError(t, err)
	specs := map[string]struct {
		instAdmin            sdk.AccAddress
		overrideContractAddr sdk.AccAddress
		caller               sdk.AccAddress
		expErr               *sdkerrors.Error
	}{
		"all good when called by proper admin": {
			instAdmin: fred,
			caller:    fred,
		},
		"prevent update when admin was not set on instantiate": {
			caller: creator,
			expErr: sdkerrors.ErrUnauthorized,
		},
		"prevent updates from non admin address": {
			instAdmin: creator,
			caller:    fred,
			expErr:    sdkerrors.ErrUnauthorized,
		},
		"fail with non existing contract addr": {
			instAdmin:            creator,
			caller:               creator,
			overrideContractAddr: anyAddr,
			expErr:               sdkerrors.ErrInvalidRequest,
		},
	}
	for msg, spec := range specs {
		t.Run(msg, func(t *testing.T) {
			addr, _, err := keepers.ContractKeeper.Instantiate(ctx, originalContractID, creator, spec.instAdmin, initMsgBz, "demo contract", nil)
			require.NoError(t, err)
			if spec.overrideContractAddr != nil {
				addr = spec.overrideContractAddr
			}
			err = keeper.ClearContractAdmin(ctx, addr, spec.caller)
			require.True(t, spec.expErr.Is(err), "expected %v but got %+v", spec.expErr, err)
			if spec.expErr != nil {
				return
			}
			cInfo := keepers.WasmKeeper.GetContractInfo(ctx, addr)
			assert.Empty(t, cInfo.Admin)
		})
	}
}

func TestInitializePinnedCodes(t *testing.T) {
	ctx, keepers := CreateTestInput(t, false, SupportedFeatures)
	k := keepers.WasmKeeper

	var capturedChecksums []wasmvm.Checksum
	mock := wasmtesting.MockWasmer{PinFn: func(checksum wasmvm.Checksum) error {
		capturedChecksums = append(capturedChecksums, checksum)
		return nil
	}}
	wasmtesting.MakeIBCInstantiable(&mock)

	const testItems = 3
	myCodeIDs := make([]uint64, testItems)
	for i := 0; i < testItems; i++ {
		myCodeIDs[i] = StoreRandomContract(t, ctx, keepers, &mock).CodeID
		require.NoError(t, k.pinCode(ctx, myCodeIDs[i]))
	}
	capturedChecksums = nil

	// when
	gotErr := k.InitializePinnedCodes(ctx)

	// then
	require.NoError(t, gotErr)
	require.Len(t, capturedChecksums, testItems)
	for i, c := range myCodeIDs {
		var exp wasmvm.Checksum = k.GetCodeInfo(ctx, c).CodeHash
		assert.Equal(t, exp, capturedChecksums[i])
	}
}

func TestPinnedContractLoops(t *testing.T) {
	// a pinned contract that calls itself via submessages should terminate with an
	// error at some point
	ctx, keepers := CreateTestInput(t, false, SupportedFeatures)
	k := keepers.WasmKeeper

	var capturedChecksums []wasmvm.Checksum
	mock := wasmtesting.MockWasmer{PinFn: func(checksum wasmvm.Checksum) error {
		capturedChecksums = append(capturedChecksums, checksum)
		return nil
	}}
	wasmtesting.MakeInstantiable(&mock)
	example := SeedNewContractInstance(t, ctx, keepers, &mock)
	require.NoError(t, k.pinCode(ctx, example.CodeID))
	var loops int
	anyMsg := []byte(`{}`)
	mock.ExecuteFn = func(codeID wasmvm.Checksum, env wasmvmtypes.Env, info wasmvmtypes.MessageInfo, executeMsg []byte, store wasmvm.KVStore, goapi wasmvm.GoAPI, querier wasmvm.Querier, gasMeter wasmvm.GasMeter, gasLimit uint64, deserCost wasmvmtypes.UFraction) (*wasmvmtypes.Response, uint64, error) {
		loops++
		return &wasmvmtypes.Response{
			Messages: []wasmvmtypes.SubMsg{
				{
					ID:      1,
					ReplyOn: wasmvmtypes.ReplyError,
					Msg: wasmvmtypes.CosmosMsg{
						Wasm: &wasmvmtypes.WasmMsg{
							Execute: &wasmvmtypes.ExecuteMsg{
								ContractAddr: example.Contract.String(),
								Msg:          anyMsg,
							},
						},
					},
				},
			},
		}, 0, nil
	}
	ctx = ctx.WithGasMeter(sdk.NewGasMeter(20000))
	require.PanicsWithValue(t, sdk.ErrorOutOfGas{Descriptor: "ReadFlat"}, func() {
		k.execute(ctx, example.Contract, RandomAccountAddress(t), anyMsg, nil)
	})
	assert.True(t, ctx.GasMeter().IsOutOfGas())
	assert.Greater(t, loops, 2)

}

func TestNewDefaultWasmVMContractResponseHandler(t *testing.T) {
	specs := map[string]struct {
		srcData []byte
		setup   func(m *wasmtesting.MockMsgDispatcher)
		expErr  bool
		expData []byte
	}{
		"submessage overwrites result when set": {
			srcData: []byte("otherData"),
			setup: func(m *wasmtesting.MockMsgDispatcher) {
				m.DispatchSubmessagesFn = func(ctx sdk.Context, contractAddr sdk.AccAddress, ibcPort string, msgs []wasmvmtypes.SubMsg) ([]byte, error) {
					return []byte("mySubMsgData"), nil
				}
			},
			expErr:  false,
			expData: []byte("mySubMsgData"),
		},
		"submessage overwrites result when empty": {
			srcData: []byte("otherData"),
			setup: func(m *wasmtesting.MockMsgDispatcher) {
				m.DispatchSubmessagesFn = func(ctx sdk.Context, contractAddr sdk.AccAddress, ibcPort string, msgs []wasmvmtypes.SubMsg) ([]byte, error) {
					return []byte(""), nil
				}
			},
			expErr:  false,
			expData: []byte(""),
		},
		"submessage do not overwrite result when nil": {
			srcData: []byte("otherData"),
			setup: func(m *wasmtesting.MockMsgDispatcher) {
				m.DispatchSubmessagesFn = func(ctx sdk.Context, contractAddr sdk.AccAddress, ibcPort string, msgs []wasmvmtypes.SubMsg) ([]byte, error) {
					return nil, nil
				}
			},
			expErr:  false,
			expData: []byte("otherData"),
		},
		"submessage error aborts process": {
			setup: func(m *wasmtesting.MockMsgDispatcher) {
				m.DispatchSubmessagesFn = func(ctx sdk.Context, contractAddr sdk.AccAddress, ibcPort string, msgs []wasmvmtypes.SubMsg) ([]byte, error) {
					return nil, errors.New("test - ignore")
				}
			},
			expErr: true,
		},
	}
	for name, spec := range specs {
		t.Run(name, func(t *testing.T) {
			var (
				msgs []wasmvmtypes.SubMsg
			)
			var mock wasmtesting.MockMsgDispatcher
			spec.setup(&mock)
			d := NewDefaultWasmVMContractResponseHandler(&mock)
			// when

			gotData, gotErr := d.Handle(sdk.Context{}, RandomAccountAddress(t), "ibc-port", msgs, spec.srcData)
			if spec.expErr {
				require.Error(t, gotErr)
				return
			}
			require.NoError(t, gotErr)
			assert.Equal(t, spec.expData, gotData)
		})
	}
}

func TestBuildContractAddress(t *testing.T) {
	specs := map[string]struct {
		srcCodeID     uint64
		srcInstanceID uint64
		expPanic      bool
	}{
		"both empty": {},
		"both below max": {
			srcCodeID:     math.MaxUint32 - 1,
			srcInstanceID: math.MaxUint32 - 1,
		},
		"both at max": {
			srcCodeID:     math.MaxUint32,
			srcInstanceID: math.MaxUint32,
		},
		"codeID > max": {
			srcCodeID: math.MaxUint32 + 1,
			expPanic:  true,
		},
		"instanceID > max": {
			srcInstanceID: math.MaxUint32 + 1,
			expPanic:      true,
		},
	}
	for name, spec := range specs {
		t.Run(name, func(t *testing.T) {
			if spec.expPanic {
				require.Panics(t, func() {
					BuildContractAddress(spec.srcCodeID, spec.srcInstanceID)
				})
				return
			}
			gotAddr := BuildContractAddress(spec.srcCodeID, spec.srcInstanceID)
			require.NotNil(t, gotAddr)
			assert.Nil(t, sdk.VerifyAddressFormat(gotAddr))
		})
	}
}
