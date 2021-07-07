package wasm_test

import (
	"encoding/json"
	wasmd "github.com/CosmWasm/wasmd/app"
	"github.com/CosmWasm/wasmd/x/wasm/ibctesting"
	wasmkeeper "github.com/CosmWasm/wasmd/x/wasm/keeper"
	wasmtesting "github.com/CosmWasm/wasmd/x/wasm/keeper/wasmtesting"
	"github.com/CosmWasm/wasmd/x/wasm/types"
	wasmvm "github.com/CosmWasm/wasmvm"
	wasmvmtypes "github.com/CosmWasm/wasmvm/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	ibctransfertypes "github.com/cosmos/cosmos-sdk/x/ibc/applications/transfer/types"
	clienttypes "github.com/cosmos/cosmos-sdk/x/ibc/core/02-client/types"
	channeltypes "github.com/cosmos/cosmos-sdk/x/ibc/core/04-channel/types"
	ibcexported "github.com/cosmos/cosmos-sdk/x/ibc/core/exported"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestFromIBCTransferToContract(t *testing.T) {
	// scenario: a contract can handle the receiving side of an ics20 transfer
	myContract := receiverContract{t: t}
	var (
		chainAOpts = []wasmkeeper.Option{wasmkeeper.WithWasmEngine(
			wasmtesting.NewIBCContractMockWasmer(&myContract),
		)}
		coordinator = ibctesting.NewCoordinator(t, 2, nil, chainAOpts)
		chainA      = coordinator.GetChain(ibctesting.GetChainID(0))
		chainB      = coordinator.GetChain(ibctesting.GetChainID(1))
	)
	coordinator.CommitBlock(chainA, chainB)
	myContractAddr := chainB.SeedNewContractInstance()
	contractAPortID := chainB.ContractInfo(myContractAddr).IBCPortID

	myContract.contractAddr = myContractAddr
	myContract.chain = chainB

	var (
		sourcePortID      = "transfer"
		counterpartPortID = contractAPortID
	)
	clientA, clientB, connA, connB := coordinator.SetupClientConnections(chainA, chainB, ibcexported.Tendermint)
	channelA, channelB := coordinator.CreateChannel(chainA, chainB, connA, connB, sourcePortID, counterpartPortID, channeltypes.UNORDERED)

	originalBalance := wasmd.NewTestSupport(t, chainA.App).BankKeeper().GetBalance(chainA.GetContext(), chainA.SenderAccount.GetAddress(), sdk.DefaultBondDenom)

	// with the channels established, let's do a transfer via sdk transfer
	coinToSendToB := sdk.NewCoin(sdk.DefaultBondDenom, sdk.NewInt(1))
	timeoutHeight := clienttypes.NewHeight(1, 110)
	msg := ibctransfertypes.NewMsgTransfer(channelA.PortID, channelA.ID, coinToSendToB, chainA.SenderAccount.GetAddress(), chainB.SenderAccount.GetAddress().String(), timeoutHeight, 0)
	err := coordinator.SendMsg(chainA, chainB, clientB, msg)
	require.NoError(t, err)

	// when relay to chain B and handle Ack on chain A
	fungibleTokenPacket := ibctransfertypes.NewFungibleTokenPacketData(coinToSendToB.Denom, coinToSendToB.Amount.Uint64(), chainA.SenderAccount.GetAddress().String(), chainB.SenderAccount.GetAddress().String())
	packet := channeltypes.NewPacket(fungibleTokenPacket.GetBytes(), 1, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, timeoutHeight, 0)
	ack := channeltypes.NewResultAcknowledgement([]byte{byte(1)}).GetBytes()
	err = coordinator.RelayPacket(chainA, chainB, clientA, clientB, packet, ack)
	require.NoError(t, err)

	// then
	newBalance := wasmd.NewTestSupport(t, chainA.App).BankKeeper().GetBalance(chainA.GetContext(), chainA.SenderAccount.GetAddress(), sdk.DefaultBondDenom)
	assert.Equal(t, originalBalance.Sub(coinToSendToB), newBalance)

	voucherDenom := ibctransfertypes.ParseDenomTrace(ibctransfertypes.GetPrefixedDenom(channelB.PortID, channelB.ID, coinToSendToB.Denom)).IBCDenom()
	bankKeeperB := wasmd.NewTestSupport(t, chainB.App).BankKeeper()
	chainBBalance := bankKeeperB.GetBalance(chainB.GetContext(), chainB.SenderAccount.GetAddress(), voucherDenom)
	// note: the contract is called during check and deliverTX but the context used in the contract does not rollback
	// so that we got twice the amount
	assert.Equal(t, sdk.Coin{Denom: voucherDenom, Amount: coinToSendToB.Amount.Mul(sdk.NewInt(2))}.String(), chainBBalance.String(), bankKeeperB.GetAllBalances(chainB.GetContext(), chainB.SenderAccount.GetAddress()))
}

func TestContractCanUseIBCTransferMsg(t *testing.T) {
	// scenario: a contract can start an ibc transfer via ibctransfertypes.NewMsgTransfer
	// on an existing connection
	myContract := &sendViaIBCTransferContract{t: t}

	var (
		chainAOpts = []wasmkeeper.Option{wasmkeeper.WithWasmEngine(
			wasmtesting.NewIBCContractMockWasmer(myContract)),
		}
		coordinator = ibctesting.NewCoordinator(t, 2, chainAOpts, nil)
		chainA      = coordinator.GetChain(ibctesting.GetChainID(0))
		chainB      = coordinator.GetChain(ibctesting.GetChainID(1))
	)
	coordinator.CommitBlock(chainA, chainB)
	myContractAddr := chainA.SeedNewContractInstance()
	coordinator.CommitBlock(chainA, chainB)

	var (
		sourcePortID      = ibctransfertypes.ModuleName
		counterpartPortID = ibctransfertypes.ModuleName
	)

	clientA, clientB, connA, connB := coordinator.SetupClientConnections(chainA, chainB, ibcexported.Tendermint)
	channelA, channelB := coordinator.CreateChannel(chainA, chainB, connA, connB, sourcePortID, counterpartPortID, channeltypes.UNORDERED)

	// send message to chainA
	receiverAddress := chainB.SenderAccount.GetAddress()

	timeoutHeight := clienttypes.NewHeight(0, 110)
	coinToSendToB := sdk.NewCoin(sdk.DefaultBondDenom, sdk.NewInt(100))

	// start transfer from chainA to chainB
	startMsg := &types.MsgExecuteContract{
		Sender:   chainA.SenderAccount.GetAddress().String(),
		Contract: myContractAddr.String(),
		Msg: startTransfer{
			ChannelID:    channelA.ID,
			CoinsToSend:  coinToSendToB,
			ReceiverAddr: receiverAddress.String(),
		}.GetBytes(),
	}
	err := coordinator.SendMsg(chainA, chainB, clientB, startMsg)
	require.NoError(t, err)

	// relay send
	fungibleTokenPacket := ibctransfertypes.NewFungibleTokenPacketData(coinToSendToB.Denom, coinToSendToB.Amount.Uint64(), myContractAddr.String(), receiverAddress.String())
	packet := channeltypes.NewPacket(fungibleTokenPacket.GetBytes(), 1, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, timeoutHeight, 0)
	ack := channeltypes.NewResultAcknowledgement([]byte{byte(1)})
	err = coordinator.RelayPacket(chainA, chainB, clientA, clientB, packet, ack.GetBytes())
	require.NoError(t, err) // relay committed

	bankKeeperB := wasmd.NewTestSupport(t, chainB.App).BankKeeper()
	voucherDenom := ibctransfertypes.ParseDenomTrace(ibctransfertypes.GetPrefixedDenom(channelB.PortID, channelB.ID, coinToSendToB.Denom)).IBCDenom()
	newBalance := bankKeeperB.GetBalance(chainB.GetContext(), receiverAddress, voucherDenom)
	assert.Equal(t, sdk.NewCoin(voucherDenom, coinToSendToB.Amount).String(), newBalance.String(), bankKeeperB.GetAllBalances(chainB.GetContext(), chainB.SenderAccount.GetAddress()))
}

func TestContractCanEmulateIBCTransferMessage(t *testing.T) {
	// scenario: a contract can be the sending side of an ics20 transfer
	// on an existing connection
	myContract := &sendEmulatedIBCTransferContract{t: t}

	var (
		chainAOpts = []wasmkeeper.Option{wasmkeeper.WithWasmEngine(
			wasmtesting.NewIBCContractMockWasmer(myContract)),
		}
		coordinator = ibctesting.NewCoordinator(t, 2, chainAOpts, nil)

		chainA = coordinator.GetChain(ibctesting.GetChainID(0))
		chainB = coordinator.GetChain(ibctesting.GetChainID(1))
	)
	coordinator.CommitBlock(chainA, chainB)
	myContractAddr := chainA.SeedNewContractInstance()
	myContract.contractAddr = myContractAddr.String()
	var (
		sourcePortID      = chainA.ContractInfo(myContractAddr).IBCPortID
		counterpartPortID = ibctransfertypes.ModuleName
	)

	clientA, clientB, connA, connB := coordinator.SetupClientConnections(chainA, chainB, ibcexported.Tendermint)
	channelA, channelB := coordinator.CreateChannel(chainA, chainB, connA, connB, sourcePortID, counterpartPortID, channeltypes.UNORDERED)

	// send message to chainA
	receiverAddress := chainB.SenderAccount.GetAddress()

	var timeoutHeight clienttypes.Height
	timeout := uint64(chainB.LastHeader.Header.Time.Add(time.Hour).UnixNano()) // enough time to not timeout
	coinToSendToB := sdk.NewCoin(sdk.DefaultBondDenom, sdk.NewInt(100))

	// start transfer from chainA to chainB
	startMsg := &types.MsgExecuteContract{
		Sender:   chainA.SenderAccount.GetAddress().String(),
		Contract: myContractAddr.String(),
		Msg: startTransfer{
			ChannelID:       channelA.ID,
			CoinsToSend:     coinToSendToB,
			ReceiverAddr:    receiverAddress.String(),
			ContractIBCPort: chainA.ContractInfo(myContractAddr).IBCPortID,
			Timeout:         timeout,
		}.GetBytes(),
	}
	err := coordinator.SendMsg(chainA, chainB, clientB, startMsg)
	require.NoError(t, err)

	// relay send
	fungibleTokenPacket := ibctransfertypes.NewFungibleTokenPacketData(coinToSendToB.Denom, coinToSendToB.Amount.Uint64(), myContractAddr.String(), receiverAddress.String())
	packet := channeltypes.NewPacket(fungibleTokenPacket.GetBytes(), 1, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, timeoutHeight, timeout)
	ack := channeltypes.NewResultAcknowledgement([]byte{byte(1)})
	err = coordinator.RelayPacket(chainA, chainB, clientA, clientB, packet, ack.GetBytes())
	require.NoError(t, err) // relay committed

	bankKeeperB := wasmd.NewTestSupport(t, chainB.App).BankKeeper()
	voucherDenom := ibctransfertypes.ParseDenomTrace(ibctransfertypes.GetPrefixedDenom(channelB.PortID, channelB.ID, coinToSendToB.Denom)).IBCDenom()
	newBalance := bankKeeperB.GetBalance(chainB.GetContext(), receiverAddress, voucherDenom)
	assert.Equal(t, sdk.NewCoin(voucherDenom, coinToSendToB.Amount).String(), newBalance.String(), bankKeeperB.GetAllBalances(chainB.GetContext(), chainB.SenderAccount.GetAddress()))
}

func TestContractCanEmulateIBCTransferMessageWithTimeout(t *testing.T) {
	// scenario: a contract is the sending side of an ics20 transfer but the packet was not received
	// on the destination chain within the timeout boundaries
	myContract := &sendEmulatedIBCTransferContract{t: t}

	var (
		chainAOpts = []wasmkeeper.Option{wasmkeeper.WithWasmEngine(
			wasmtesting.NewIBCContractMockWasmer(myContract)),
		}
		coordinator = ibctesting.NewCoordinator(t, 2, chainAOpts, nil)

		chainA = coordinator.GetChain(ibctesting.GetChainID(0))
		chainB = coordinator.GetChain(ibctesting.GetChainID(1))
	)
	coordinator.CommitBlock(chainA, chainB)
	myContractAddr := chainA.SeedNewContractInstance()
	myContract.contractAddr = myContractAddr.String()
	var (
		sourcePortID      = chainA.ContractInfo(myContractAddr).IBCPortID
		counterpartPortID = ibctransfertypes.ModuleName
	)

	clientA, clientB, connA, connB := coordinator.SetupClientConnections(chainA, chainB, ibcexported.Tendermint)
	channelA, channelB := coordinator.CreateChannel(chainA, chainB, connA, connB, sourcePortID, counterpartPortID, channeltypes.UNORDERED)

	// start process by sending message to the contract on chainA
	receiverAddress := chainB.SenderAccount.GetAddress()
	coinToSendToB := sdk.NewCoin(sdk.DefaultBondDenom, sdk.NewInt(100))
	timeout := uint64(chainB.LastHeader.Header.Time.Add(time.Nanosecond).UnixNano()) // not enough time

	// custom payload data to be transfered into a proper ICS20 ibc packet
	startMsg := &types.MsgExecuteContract{
		Sender:   chainA.SenderAccount.GetAddress().String(),
		Contract: myContractAddr.String(),
		Msg: startTransfer{
			ChannelID:       channelA.ID,
			CoinsToSend:     coinToSendToB,
			ReceiverAddr:    receiverAddress.String(),
			ContractIBCPort: chainA.ContractInfo(myContractAddr).IBCPortID,
			Timeout:         timeout,
		}.GetBytes(),
	}
	err := coordinator.SendMsg(chainA, chainB, clientB, startMsg)
	require.NoError(t, err)
	err = coordinator.UpdateClient(chainA, chainB, clientA, ibcexported.Tendermint)
	require.NoError(t, err)

	// timeout packet send (by the relayer)
	fungibleTokenPacketData := ibctransfertypes.NewFungibleTokenPacketData(coinToSendToB.Denom, coinToSendToB.Amount.Uint64(), myContractAddr.String(), receiverAddress.String())
	var timeoutHeight clienttypes.Height
	packet := channeltypes.NewPacket(fungibleTokenPacketData.GetBytes(), 1, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, timeoutHeight, timeout)

	err = coordinator.TimeoutPacket(chainA, chainB, clientB, packet)
	require.NoError(t, err)

	// then verify account has no vouchers
	bankKeeperB := wasmd.NewTestSupport(t, chainB.App).BankKeeper()
	voucherDenom := ibctransfertypes.ParseDenomTrace(ibctransfertypes.GetPrefixedDenom(channelB.PortID, channelB.ID, coinToSendToB.Denom)).IBCDenom()
	newBalance := bankKeeperB.GetBalance(chainB.GetContext(), receiverAddress, voucherDenom)
	assert.Equal(t, sdk.NewInt64Coin(voucherDenom, 0).String(), newBalance.String(), bankKeeperB.GetAllBalances(chainB.GetContext(), chainB.SenderAccount.GetAddress()))
}

func TestContractHandlesChannelClose(t *testing.T) {
	// scenario: a contract is the sending side of an ics20 transfer but the packet was not received
	// on the destination chain within the timeout boundaries
	myContractA := &captureCloseContract{}
	myContractB := &captureCloseContract{}

	var (
		chainAOpts = []wasmkeeper.Option{wasmkeeper.WithWasmEngine(
			wasmtesting.NewIBCContractMockWasmer(myContractA)),
		}
		chainBOpts = []wasmkeeper.Option{wasmkeeper.WithWasmEngine(
			wasmtesting.NewIBCContractMockWasmer(myContractB)),
		}
		coordinator = ibctesting.NewCoordinator(t, 2, chainAOpts, chainBOpts)

		chainA = coordinator.GetChain(ibctesting.GetChainID(0))
		chainB = coordinator.GetChain(ibctesting.GetChainID(1))
	)
	coordinator.CommitBlock(chainA, chainB)
	myContractAddrA := chainA.SeedNewContractInstance()
	_ = chainB.SeedNewContractInstance() // skip one instance
	myContractAddrB := chainB.SeedNewContractInstance()
	var (
		sourcePortID      = chainA.ContractInfo(myContractAddrA).IBCPortID
		counterpartPortID = chainB.ContractInfo(myContractAddrB).IBCPortID
	)

	_, _, connA, connB := coordinator.SetupClientConnections(chainA, chainB, ibcexported.Tendermint)
	channelA, channelB := coordinator.CreateChannel(chainA, chainB, connA, connB, sourcePortID, counterpartPortID, channeltypes.UNORDERED)

	err := coordinator.ChanCloseInit(chainA, chainB, channelA)
	require.NoError(t, err)
	assert.True(t, myContractA.closeCalled)

	err = coordinator.UpdateClient(chainB, chainA, channelB.ClientID, ibcexported.Tendermint)
	require.NoError(t, err)
	err = coordinator.ChanCloseConfirm(chainA, chainB, channelA, channelB)
	require.NoError(t, err)
	assert.True(t, myContractB.closeCalled)
}

var _ wasmtesting.IBCContractCallbacks = &captureCloseContract{}

// contract that sets a flag on IBC channel close only.
type captureCloseContract struct {
	contractStub
	closeCalled bool
}

func (c *captureCloseContract) IBCChannelClose(codeID wasmvm.Checksum, env wasmvmtypes.Env, channel wasmvmtypes.IBCChannel, store wasmvm.KVStore, goapi wasmvm.GoAPI, querier wasmvm.Querier, gasMeter wasmvm.GasMeter, gasLimit uint64, deserCost wasmvmtypes.UFraction) (*wasmvmtypes.IBCBasicResponse, uint64, error) {
	c.closeCalled = true
	return &wasmvmtypes.IBCBasicResponse{}, 1, nil
}

var _ wasmtesting.IBCContractCallbacks = &sendViaIBCTransferContract{}

// contract that initiates an ics-20 transfer on execute via sdk message
type sendViaIBCTransferContract struct {
	contractStub
	t *testing.T
}

func (s *sendViaIBCTransferContract) Execute(code wasmvm.Checksum, env wasmvmtypes.Env, info wasmvmtypes.MessageInfo, executeMsg []byte, store wasmvm.KVStore, goapi wasmvm.GoAPI, querier wasmvm.Querier, gasMeter wasmvm.GasMeter, gasLimit uint64, deserCost wasmvmtypes.UFraction) (*wasmvmtypes.Response, uint64, error) {
	var in startTransfer
	if err := json.Unmarshal(executeMsg, &in); err != nil {
		return nil, 0, err
	}
	ibcMsg := &wasmvmtypes.IBCMsg{
		Transfer: &wasmvmtypes.TransferMsg{
			ToAddress: in.ReceiverAddr,
			Amount:    wasmvmtypes.NewCoin(in.CoinsToSend.Amount.Uint64(), in.CoinsToSend.Denom),
			ChannelID: in.ChannelID,
			Timeout: wasmvmtypes.IBCTimeout{Block: &wasmvmtypes.IBCTimeoutBlock{
				Revision: 0,
				Height:   110,
			}},
		},
	}

	return &wasmvmtypes.Response{Messages: []wasmvmtypes.SubMsg{{ReplyOn: wasmvmtypes.ReplyNever, Msg: wasmvmtypes.CosmosMsg{IBC: ibcMsg}}}}, 0, nil
	return &wasmvmtypes.Response{Messages: []wasmvmtypes.SubMsg{{ReplyOn: wasmvmtypes.ReplyNever, Msg: wasmvmtypes.CosmosMsg{IBC: ibcMsg}}}}, 0, nil
}

var _ wasmtesting.IBCContractCallbacks = &sendEmulatedIBCTransferContract{}

// contract that interacts as an ics20 sending side via IBC packets
// It can also handle the timeout.
type sendEmulatedIBCTransferContract struct {
	contractStub
	t            *testing.T
	contractAddr string
}

func (s *sendEmulatedIBCTransferContract) Execute(code wasmvm.Checksum, env wasmvmtypes.Env, info wasmvmtypes.MessageInfo, executeMsg []byte, store wasmvm.KVStore, goapi wasmvm.GoAPI, querier wasmvm.Querier, gasMeter wasmvm.GasMeter, gasLimit uint64, deserCost wasmvmtypes.UFraction) (*wasmvmtypes.Response, uint64, error) {
	var in startTransfer
	if err := json.Unmarshal(executeMsg, &in); err != nil {
		return nil, 0, err
	}

	dataPacket := ibctransfertypes.NewFungibleTokenPacketData(
		in.CoinsToSend.Denom, in.CoinsToSend.Amount.Uint64(), s.contractAddr, in.ReceiverAddr,
	)
	if err := dataPacket.ValidateBasic(); err != nil {
		return nil, 0, err
	}

	ibcMsg := &wasmvmtypes.IBCMsg{
		SendPacket: &wasmvmtypes.SendPacketMsg{
			ChannelID: in.ChannelID,
			Data:      dataPacket.GetBytes(),
			Timeout:   wasmvmtypes.IBCTimeout{Timestamp: in.Timeout},
		},
	}
	return &wasmvmtypes.Response{Messages: []wasmvmtypes.SubMsg{{ReplyOn: wasmvmtypes.ReplyNever, Msg: wasmvmtypes.CosmosMsg{IBC: ibcMsg}}}}, 0, nil
}

func (c *sendEmulatedIBCTransferContract) IBCPacketTimeout(codeID wasmvm.Checksum, env wasmvmtypes.Env, packet wasmvmtypes.IBCPacket, store wasmvm.KVStore, goapi wasmvm.GoAPI, querier wasmvm.Querier, gasMeter wasmvm.GasMeter, gasLimit uint64, deserCost wasmvmtypes.UFraction) (*wasmvmtypes.IBCBasicResponse, uint64, error) {
	var data ibctransfertypes.FungibleTokenPacketData
	if err := ibctransfertypes.ModuleCdc.UnmarshalJSON(packet.Data, &data); err != nil {
		return nil, 0, err
	}

	returnTokens := &wasmvmtypes.BankMsg{
		Send: &wasmvmtypes.SendMsg{
			ToAddress: data.Sender,
			Amount:    wasmvmtypes.Coins{wasmvmtypes.NewCoin(data.Amount, data.Denom)},
		}}

	return &wasmvmtypes.IBCBasicResponse{Messages: []wasmvmtypes.SubMsg{{ReplyOn: wasmvmtypes.ReplyNever, Msg: wasmvmtypes.CosmosMsg{Bank: returnTokens}}}}, 0, nil
}

// custom contract execute payload
type startTransfer struct {
	ChannelID       string
	CoinsToSend     sdk.Coin
	ReceiverAddr    string
	ContractIBCPort string
	Timeout         uint64
}

func (g startTransfer) GetBytes() json.RawMessage {
	b, err := json.Marshal(g)
	if err != nil {
		panic(err)
	}
	return b
}

var _ wasmtesting.IBCContractCallbacks = &receiverContract{}

// contract that acts as the receiving side for an ics-20 transfer.
type receiverContract struct {
	contractStub
	t            *testing.T
	contractAddr sdk.AccAddress
	chain        *ibctesting.TestChain
}

func (c *receiverContract) IBCPacketReceive(codeID wasmvm.Checksum, env wasmvmtypes.Env, packet wasmvmtypes.IBCPacket, store wasmvm.KVStore, goapi wasmvm.GoAPI, querier wasmvm.Querier, gasMeter wasmvm.GasMeter, gasLimit uint64, deserCost wasmvmtypes.UFraction) (*wasmvmtypes.IBCReceiveResponse, uint64, error) {
	var src ibctransfertypes.FungibleTokenPacketData
	if err := ibctransfertypes.ModuleCdc.UnmarshalJSON(packet.Data, &src); err != nil {
		return nil, 0, err
	}
	// call original ibctransfer keeper to not copy all code into this
	ibcPacket := toIBCPacket(packet)
	ctx := c.chain.GetContext() // HACK: please note that this is not reverted after checkTX
	err := c.chain.TestSupport().TransferKeeper().OnRecvPacket(ctx, ibcPacket, src)
	if err != nil {
		return nil, 0, sdkerrors.Wrap(err, "within our smart contract")
	}

	var log []wasmvmtypes.EventAttribute // note: all events are under `wasm` event type
	ack := channeltypes.NewResultAcknowledgement([]byte{byte(1)}).GetBytes()
	return &wasmvmtypes.IBCReceiveResponse{Acknowledgement: ack, Attributes: log}, 0, nil
}

func (c *receiverContract) IBCPacketAck(codeID wasmvm.Checksum, env wasmvmtypes.Env, packetAck wasmvmtypes.IBCAcknowledgementWithPacket, store wasmvm.KVStore, goapi wasmvm.GoAPI, querier wasmvm.Querier, gasMeter wasmvm.GasMeter, gasLimit uint64, deserCost wasmvmtypes.UFraction) (*wasmvmtypes.IBCBasicResponse, uint64, error) {
	var data ibctransfertypes.FungibleTokenPacketData
	if err := ibctransfertypes.ModuleCdc.UnmarshalJSON(packetAck.OriginalPacket.Data, &data); err != nil {
		return nil, 0, err
	}
	// call original ibctransfer keeper to not copy all code into this

	var ack channeltypes.Acknowledgement
	if err := ibctransfertypes.ModuleCdc.UnmarshalJSON(packetAck.Acknowledgement.Data, &ack); err != nil {
		return nil, 0, err
	}

	// call original ibctransfer keeper to not copy all code into this
	ctx := c.chain.GetContext() // HACK: please note that this is not reverted after checkTX
	ibcPacket := toIBCPacket(packetAck.OriginalPacket)
	err := c.chain.TestSupport().TransferKeeper().OnAcknowledgementPacket(ctx, ibcPacket, data, ack)
	if err != nil {
		return nil, 0, sdkerrors.Wrap(err, "within our smart contract")
	}

	return &wasmvmtypes.IBCBasicResponse{}, 0, nil
}

// simple helper struct that implements connection setup methods.
type contractStub struct{}

func (s *contractStub) IBCChannelOpen(codeID wasmvm.Checksum, env wasmvmtypes.Env, channel wasmvmtypes.IBCChannel, store wasmvm.KVStore, goapi wasmvm.GoAPI, querier wasmvm.Querier, gasMeter wasmvm.GasMeter, gasLimit uint64, deserCost wasmvmtypes.UFraction) (uint64, error) {
	return 0, nil
}

func (s *contractStub) IBCChannelConnect(codeID wasmvm.Checksum, env wasmvmtypes.Env, channel wasmvmtypes.IBCChannel, store wasmvm.KVStore, goapi wasmvm.GoAPI, querier wasmvm.Querier, gasMeter wasmvm.GasMeter, gasLimit uint64, deserCost wasmvmtypes.UFraction) (*wasmvmtypes.IBCBasicResponse, uint64, error) {
	return &wasmvmtypes.IBCBasicResponse{}, 0, nil
}

func (s *contractStub) IBCChannelClose(codeID wasmvm.Checksum, env wasmvmtypes.Env, channel wasmvmtypes.IBCChannel, store wasmvm.KVStore, goapi wasmvm.GoAPI, querier wasmvm.Querier, gasMeter wasmvm.GasMeter, gasLimit uint64, deserCost wasmvmtypes.UFraction) (*wasmvmtypes.IBCBasicResponse, uint64, error) {
	panic("implement me")
}

func (s *contractStub) IBCPacketReceive(codeID wasmvm.Checksum, env wasmvmtypes.Env, packet wasmvmtypes.IBCPacket, store wasmvm.KVStore, goapi wasmvm.GoAPI, querier wasmvm.Querier, gasMeter wasmvm.GasMeter, gasLimit uint64, deserCost wasmvmtypes.UFraction) (*wasmvmtypes.IBCReceiveResponse, uint64, error) {
	panic("implement me")
}

func (s *contractStub) IBCPacketAck(codeID wasmvm.Checksum, env wasmvmtypes.Env, ack wasmvmtypes.IBCAcknowledgementWithPacket, store wasmvm.KVStore, goapi wasmvm.GoAPI, querier wasmvm.Querier, gasMeter wasmvm.GasMeter, gasLimit uint64, deserCost wasmvmtypes.UFraction) (*wasmvmtypes.IBCBasicResponse, uint64, error) {
	return &wasmvmtypes.IBCBasicResponse{}, 0, nil
}

func (s *contractStub) IBCPacketTimeout(codeID wasmvm.Checksum, env wasmvmtypes.Env, packet wasmvmtypes.IBCPacket, store wasmvm.KVStore, goapi wasmvm.GoAPI, querier wasmvm.Querier, gasMeter wasmvm.GasMeter, gasLimit uint64, deserCost wasmvmtypes.UFraction) (*wasmvmtypes.IBCBasicResponse, uint64, error) {
	panic("implement me")
}

func toIBCPacket(p wasmvmtypes.IBCPacket) channeltypes.Packet {
	var height clienttypes.Height
	if p.Timeout.Block != nil {
		height = clienttypes.NewHeight(p.Timeout.Block.Revision, p.Timeout.Block.Height)
	}
	return channeltypes.Packet{
		Sequence:           p.Sequence,
		SourcePort:         p.Src.PortID,
		SourceChannel:      p.Src.ChannelID,
		DestinationPort:    p.Dest.PortID,
		DestinationChannel: p.Dest.ChannelID,
		Data:               p.Data,
		TimeoutHeight:      height,
		TimeoutTimestamp:   p.Timeout.Timestamp,
	}
}
