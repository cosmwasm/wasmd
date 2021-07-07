package wasm

import (
	types "github.com/CosmWasm/wasmd/x/wasm/types"
	wasmvmtypes "github.com/CosmWasm/wasmvm/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	capabilitytypes "github.com/cosmos/cosmos-sdk/x/capability/types"
	channeltypes "github.com/cosmos/cosmos-sdk/x/ibc/core/04-channel/types"
	porttypes "github.com/cosmos/cosmos-sdk/x/ibc/core/05-port/types"
	host "github.com/cosmos/cosmos-sdk/x/ibc/core/24-host"
	"math"
)

var _ porttypes.IBCModule = IBCHandler{}

type IBCHandler struct {
	keeper        types.IBCContractKeeper
	channelKeeper types.ChannelKeeper
}

func NewIBCHandler(k types.IBCContractKeeper, ck types.ChannelKeeper) IBCHandler {
	return IBCHandler{keeper: k, channelKeeper: ck}
}

// OnChanOpenInit implements the IBCModule interface
func (i IBCHandler) OnChanOpenInit(
	ctx sdk.Context,
	order channeltypes.Order,
	connectionHops []string,
	portID string,
	channelID string,
	chanCap *capabilitytypes.Capability,
	counterParty channeltypes.Counterparty,
	version string,
) error {
	// ensure port, version, capability
	if err := ValidateChannelParams(channelID); err != nil {
		return err
	}
	contractAddr, err := ContractFromPortID(portID)
	if err != nil {
		return sdkerrors.Wrapf(err, "contract port id")
	}

	err = i.keeper.OnOpenChannel(ctx, contractAddr, wasmvmtypes.IBCChannel{
		Endpoint:             wasmvmtypes.IBCEndpoint{PortID: portID, ChannelID: channelID},
		CounterpartyEndpoint: wasmvmtypes.IBCEndpoint{PortID: counterParty.PortId, ChannelID: counterParty.ChannelId},
		Order:                order.String(),
		Version:              version,
		ConnectionID:         connectionHops[0], // At the moment this list must be of length 1. In the future multi-hop channels may be supported.
	})
	if err != nil {
		return err
	}
	// Claim channel capability passed back by IBC module
	if err := i.keeper.ClaimCapability(ctx, chanCap, host.ChannelCapabilityPath(portID, channelID)); err != nil {
		return sdkerrors.Wrap(err, "claim capability")
	}
	return nil
}

// OnChanOpenTry implements the IBCModule interface
func (i IBCHandler) OnChanOpenTry(
	ctx sdk.Context,
	order channeltypes.Order,
	connectionHops []string,
	portID, channelID string,
	chanCap *capabilitytypes.Capability,
	counterParty channeltypes.Counterparty,
	version, counterpartyVersion string,
) error {
	// ensure port, version, capability
	if err := ValidateChannelParams(channelID); err != nil {
		return err
	}

	contractAddr, err := ContractFromPortID(portID)
	if err != nil {
		return sdkerrors.Wrapf(err, "contract port id")
	}

	err = i.keeper.OnOpenChannel(ctx, contractAddr, wasmvmtypes.IBCChannel{
		Endpoint:             wasmvmtypes.IBCEndpoint{PortID: portID, ChannelID: channelID},
		CounterpartyEndpoint: wasmvmtypes.IBCEndpoint{PortID: counterParty.PortId, ChannelID: counterParty.ChannelId},
		Order:                order.String(),
		Version:              version,
		CounterpartyVersion:  counterpartyVersion,
		ConnectionID:         connectionHops[0], // At the moment this list must be of length 1. In the future multi-hop channels may be supported.
	})
	if err != nil {
		return err
	}
	// Module may have already claimed capability in OnChanOpenInit in the case of crossing hellos
	// (ie chainA and chainB both call ChanOpenInit before one of them calls ChanOpenTry)
	// If module can already authenticate the capability then module already owns it so we don't need to claim
	// Otherwise, module does not have channel capability and we must claim it from IBC
	if !i.keeper.AuthenticateCapability(ctx, chanCap, host.ChannelCapabilityPath(portID, channelID)) {
		// Only claim channel capability passed back by IBC module if we do not already own it
		if err := i.keeper.ClaimCapability(ctx, chanCap, host.ChannelCapabilityPath(portID, channelID)); err != nil {
			return sdkerrors.Wrap(err, "claim capability")
		}
	}
	return nil
}

// OnChanOpenAck implements the IBCModule interface
func (i IBCHandler) OnChanOpenAck(
	ctx sdk.Context,
	portID, channelID string,
	counterpartyVersion string,
) error {
	contractAddr, err := ContractFromPortID(portID)
	if err != nil {
		return sdkerrors.Wrapf(err, "contract port id")
	}
	channelInfo, ok := i.channelKeeper.GetChannel(ctx, portID, channelID)
	if !ok {
		return sdkerrors.Wrapf(channeltypes.ErrChannelNotFound, "port ID (%s) channel ID (%s)", portID, channelID)
	}
	return i.keeper.OnConnectChannel(ctx, contractAddr, toWasmVMChannel(portID, channelID, channelInfo, counterpartyVersion))
}

// OnChanOpenConfirm implements the IBCModule interface
func (i IBCHandler) OnChanOpenConfirm(ctx sdk.Context, portID, channelID string) error {
	contractAddr, err := ContractFromPortID(portID)
	if err != nil {
		return sdkerrors.Wrapf(err, "contract port id")
	}
	channelInfo, ok := i.channelKeeper.GetChannel(ctx, portID, channelID)
	if !ok {
		return sdkerrors.Wrapf(channeltypes.ErrChannelNotFound, "port ID (%s) channel ID (%s)", portID, channelID)
	}
	return i.keeper.OnConnectChannel(ctx, contractAddr, toWasmVMChannel(portID, channelID, channelInfo, ""))
}

// OnChanCloseInit implements the IBCModule interface
func (i IBCHandler) OnChanCloseInit(ctx sdk.Context, portID, channelID string) error {
	contractAddr, err := ContractFromPortID(portID)
	if err != nil {
		return sdkerrors.Wrapf(err, "contract port id")
	}
	channelInfo, ok := i.channelKeeper.GetChannel(ctx, portID, channelID)
	if !ok {
		return sdkerrors.Wrapf(channeltypes.ErrChannelNotFound, "port ID (%s) channel ID (%s)", portID, channelID)
	}

	err = i.keeper.OnCloseChannel(ctx, contractAddr, toWasmVMChannel(portID, channelID, channelInfo, ""))
	if err != nil {
		return err
	}
	// emit events?

	return err
}

// OnChanCloseConfirm implements the IBCModule interface
func (i IBCHandler) OnChanCloseConfirm(ctx sdk.Context, portID, channelID string) error {
	// counterparty has closed the channel
	contractAddr, err := ContractFromPortID(portID)
	if err != nil {
		return sdkerrors.Wrapf(err, "contract port id")
	}
	channelInfo, ok := i.channelKeeper.GetChannel(ctx, portID, channelID)
	if !ok {
		return sdkerrors.Wrapf(channeltypes.ErrChannelNotFound, "port ID (%s) channel ID (%s)", portID, channelID)
	}

	err = i.keeper.OnCloseChannel(ctx, contractAddr, toWasmVMChannel(portID, channelID, channelInfo, ""))
	if err != nil {
		return err
	}
	// emit events?

	return err
}

func toWasmVMChannel(portID, channelID string, channelInfo channeltypes.Channel, counterpartyVersion string) wasmvmtypes.IBCChannel {
	return wasmvmtypes.IBCChannel{
		Endpoint:             wasmvmtypes.IBCEndpoint{PortID: portID, ChannelID: channelID},
		CounterpartyEndpoint: wasmvmtypes.IBCEndpoint{PortID: channelInfo.Counterparty.PortId, ChannelID: channelInfo.Counterparty.ChannelId},
		Order:                channelInfo.Ordering.String(),
		Version:              channelInfo.Version,
		ConnectionID:         channelInfo.ConnectionHops[0], // At the moment this list must be of length 1. In the future multi-hop channels may be supported.
		CounterpartyVersion:  counterpartyVersion,
	}
}

// OnRecvPacket implements the IBCModule interface
func (i IBCHandler) OnRecvPacket(
	ctx sdk.Context,
	packet channeltypes.Packet,
) (*sdk.Result, []byte, error) {
	contractAddr, err := ContractFromPortID(packet.DestinationPort)
	if err != nil {
		return nil, nil, sdkerrors.Wrapf(err, "contract port id")
	}
	ack, err := i.keeper.OnRecvPacket(ctx, contractAddr, newIBCPacket(packet))
	if err != nil {
		return nil, nil, err
	}

	return &sdk.Result{ // the response is ignored
		Events: ctx.EventManager().Events().ToABCIEvents(),
	}, ack, nil
}

// OnAcknowledgementPacket implements the IBCModule interface
func (i IBCHandler) OnAcknowledgementPacket(ctx sdk.Context, packet channeltypes.Packet, acknowledgement []byte) (*sdk.Result, error) {
	contractAddr, err := ContractFromPortID(packet.SourcePort)
	if err != nil {
		return nil, sdkerrors.Wrapf(err, "contract port id")
	}

	err = i.keeper.OnAckPacket(ctx, contractAddr, wasmvmtypes.IBCAcknowledgementWithPacket{
		Acknowledgement: wasmvmtypes.IBCAcknowledgement{Data: acknowledgement},
		OriginalPacket:  newIBCPacket(packet),
	})
	if err != nil {
		return nil, err
	}

	return &sdk.Result{
		Events: ctx.EventManager().Events().ToABCIEvents(),
	}, nil

}

// OnTimeoutPacket implements the IBCModule interface
func (i IBCHandler) OnTimeoutPacket(ctx sdk.Context, packet channeltypes.Packet) (*sdk.Result, error) {
	contractAddr, err := ContractFromPortID(packet.SourcePort)
	if err != nil {
		return nil, sdkerrors.Wrapf(err, "contract port id")
	}
	err = i.keeper.OnTimeoutPacket(ctx, contractAddr, newIBCPacket(packet))
	if err != nil {
		return nil, err
	}

	return &sdk.Result{
		Events: ctx.EventManager().Events().ToABCIEvents(),
	}, nil

}

func newIBCPacket(packet channeltypes.Packet) wasmvmtypes.IBCPacket {
	timeout := wasmvmtypes.IBCTimeout{
		Timestamp: packet.TimeoutTimestamp,
	}
	if !packet.TimeoutHeight.IsZero() {
		timeout.Block = &wasmvmtypes.IBCTimeoutBlock{
			Height:   packet.TimeoutHeight.RevisionHeight,
			Revision: packet.TimeoutHeight.RevisionNumber,
		}
	}

	return wasmvmtypes.IBCPacket{
		Data:     packet.Data,
		Src:      wasmvmtypes.IBCEndpoint{ChannelID: packet.SourceChannel, PortID: packet.SourcePort},
		Dest:     wasmvmtypes.IBCEndpoint{ChannelID: packet.DestinationChannel, PortID: packet.DestinationPort},
		Sequence: packet.Sequence,
		Timeout:  timeout,
	}
}

func ValidateChannelParams(channelID string) error {
	// NOTE: for escrow address security only 2^32 channels are allowed to be created
	// Issue: https://github.com/cosmos/cosmos-sdk/issues/7737
	channelSequence, err := channeltypes.ParseChannelSequence(channelID)
	if err != nil {
		return err
	}
	if channelSequence > math.MaxUint32 {
		return sdkerrors.Wrapf(types.ErrMaxIBCChannels, "channel sequence %d is greater than max allowed transfer channels %d", channelSequence, math.MaxUint32)
	}
	return nil
}
