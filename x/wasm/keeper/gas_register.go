package keeper

import (
	"github.com/CosmWasm/wasmd/x/wasm/types"
	wasmvmtypes "github.com/CosmWasm/wasmvm/types"
	storetypes "github.com/cosmos/cosmos-sdk/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
)

const (
	// DefaultGasMultiplier is how many cosmwasm gas points = 1 sdk gas point
	// SDK reference costs can be found here: https://github.com/cosmos/cosmos-sdk/blob/02c6c9fafd58da88550ab4d7d494724a477c8a68/store/types/gas.go#L153-L164
	// A write at ~3000 gas and ~200us = 10 gas per us (microsecond) cpu/io
	// Rough timing have 88k gas at 90us, which is equal to 1k sdk gas... (one read)
	//
	// Please note that all gas prices returned to the wasmer engine should have this multiplied
	DefaultGasMultiplier uint64 = 100
	// DefaultInstanceCost is how much SDK gas we charge each time we load a WASM instance.
	// Creating a new instance is costly, and this helps put a recursion limit to contracts calling contracts.
	DefaultInstanceCost uint64 = 40_000
	// DefaultCompileCost is how much SDK gas is charged *per byte* for compiling WASM code.
	DefaultCompileCost uint64 = 2
	// DefaultEventAttributeDataCost is how much SDK gas is charged *per byte* for attribute data in events.
	// This is used with len(key) + len(value)
	DefaultEventAttributeDataCost uint64 = 1
	// DefaultContractMessageDataCost is how much SDK gas is charged *per byte* of the message that goes to the contract
	// This is used with len(msg)
	DefaultContractMessageDataCost uint64 = 1
	// DefaultPerAttributeCost is how much SDK gas we charge per attribute count.
	DefaultPerAttributeCost uint64 = 10
	// DefaultPerCustomEventCost is how much SDK gas we charge per event count.
	DefaultPerCustomEventCost uint64 = 20
	// DefaultEventAttributeDataFreeTier number of bytes of total attribute data we do not charge.
	DefaultEventAttributeDataFreeTier = 100
)

// GasRegister abstract source for gas costs
type GasRegister interface {
	// NewContractInstanceCosts costs to crate a new contract instance from code
	NewContractInstanceCosts(pinned bool, msgLen int) sdk.Gas
	// CompileCosts costs to persist and "compile" a new wasm contract
	CompileCosts(byteLength int) sdk.Gas
	// InstantiateContractCosts costs when interacting with a wasm contract
	InstantiateContractCosts(pinned bool, msgLen int) sdk.Gas
	// ReplyCosts costs to to handle a message reply
	ReplyCosts(pinned bool, reply wasmvmtypes.Reply) sdk.Gas
	// EventCosts costs to persist an event
	EventCosts(attrs []wasmvmtypes.EventAttribute, events wasmvmtypes.Events) sdk.Gas
	// ToWasmVMGas converts from sdk gas to wasmvm gas
	ToWasmVMGas(source sdk.Gas) uint64
	// FromWasmVMGas converts from wasmvm gas to sdk gas
	FromWasmVMGas(source uint64) sdk.Gas
}

// WasmGasRegisterConfig config type
type WasmGasRegisterConfig struct {
	// InstanceCost costs when interacting with a wasm contract
	InstanceCost sdk.Gas
	// CompileCosts costs to persist and "compile" a new wasm contract
	CompileCost sdk.Gas
	// GasMultiplier is how many cosmwasm gas points = 1 sdk gas point
	// SDK reference costs can be found here: https://github.com/cosmos/cosmos-sdk/blob/02c6c9fafd58da88550ab4d7d494724a477c8a68/store/types/gas.go#L153-L164
	GasMultiplier sdk.Gas
	// EventPerAttributeCost is how much SDK gas is charged *per byte* for attribute data in events.
	// This is used with len(key) + len(value)
	EventPerAttributeCost sdk.Gas
	// EventAttributeDataCost is how much SDK gas is charged *per byte* for attribute data in events.
	// This is used with len(key) + len(value)
	EventAttributeDataCost sdk.Gas
	// EventAttributeDataFreeTier number of bytes of total attribute data that is free of charge
	EventAttributeDataFreeTier uint64
	// ContractMessageDataCost SDK gas charged *per byte* of the message that goes to the contract
	// This is used with len(msg)
	ContractMessageDataCost sdk.Gas
	// CustomEventCost cost per custom event
	CustomEventCost uint64
}

// DefaultGasRegisterConfig default values
func DefaultGasRegisterConfig() WasmGasRegisterConfig {
	return WasmGasRegisterConfig{
		InstanceCost:               DefaultInstanceCost,
		CompileCost:                DefaultCompileCost,
		GasMultiplier:              DefaultGasMultiplier,
		EventPerAttributeCost:      DefaultPerAttributeCost,
		CustomEventCost:            DefaultPerCustomEventCost,
		EventAttributeDataCost:     DefaultEventAttributeDataCost,
		EventAttributeDataFreeTier: DefaultEventAttributeDataFreeTier,
		ContractMessageDataCost:    DefaultContractMessageDataCost,
	}
}

// WasmGasRegister implements GasRegister interface
type WasmGasRegister struct {
	c WasmGasRegisterConfig
}

// NewDefaultWasmGasRegister creates instance with default values
func NewDefaultWasmGasRegister() WasmGasRegister {
	return NewWasmGasRegister(DefaultGasRegisterConfig())
}

// NewWasmGasRegister constructor
func NewWasmGasRegister(c WasmGasRegisterConfig) WasmGasRegister {
	if c.GasMultiplier == 0 {
		panic(sdkerrors.Wrap(sdkerrors.ErrLogic, "GasMultiplier can not be 0"))
	}
	return WasmGasRegister{
		c: c,
	}
}

// NewContractInstanceCosts costs to crate a new contract instance from code
func (g WasmGasRegister) NewContractInstanceCosts(pinned bool, msgLen int) storetypes.Gas {
	return g.InstantiateContractCosts(pinned, msgLen)
}

// CompileCosts costs to persist and "compile" a new wasm contract
func (g WasmGasRegister) CompileCosts(byteLength int) storetypes.Gas {
	if byteLength < 0 {
		panic(sdkerrors.Wrap(types.ErrInvalid, "negative length"))
	}
	return g.c.CompileCost * uint64(byteLength)
}

// InstantiateContractCosts costs when interacting with a wasm contract
func (g WasmGasRegister) InstantiateContractCosts(pinned bool, msgLen int) sdk.Gas {
	if msgLen < 0 {
		panic(sdkerrors.Wrap(types.ErrInvalid, "negative length"))
	}
	dataCosts := sdk.Gas(msgLen) * g.c.ContractMessageDataCost
	if pinned {
		return dataCosts
	}
	return g.c.InstanceCost + dataCosts
}

// ReplyCosts costs to to handle a message reply
func (g WasmGasRegister) ReplyCosts(pinned bool, reply wasmvmtypes.Reply) sdk.Gas {
	var eventGas sdk.Gas
	msgLen := len(reply.Result.Err)
	if reply.Result.Ok != nil {
		msgLen += len(reply.Result.Ok.Data)
		var attrs []wasmvmtypes.EventAttribute
		for _, e := range reply.Result.Ok.Events {
			msgLen += len(e.Type)
			attrs = append(e.Attributes)
		}
		// apply free tier on the whole set not per event
		eventGas += g.EventCosts(attrs, nil)
	}
	return eventGas + g.InstantiateContractCosts(pinned, msgLen)
}

// EventCosts costs to persist an event
func (g WasmGasRegister) EventCosts(attrs []wasmvmtypes.EventAttribute, events wasmvmtypes.Events) sdk.Gas {
	gas, remainingFreeTier := g.eventAttributeCosts(attrs, g.c.EventAttributeDataFreeTier)
	for _, e := range events {
		gas += g.c.CustomEventCost
		gas += sdk.Gas(len(e.Type)) * g.c.EventAttributeDataCost // no free tier with event type
		var attrCost sdk.Gas
		attrCost, remainingFreeTier = g.eventAttributeCosts(e.Attributes, remainingFreeTier)
		gas += attrCost
	}
	return gas
}

func (g WasmGasRegister) eventAttributeCosts(attrs []wasmvmtypes.EventAttribute, freeTier uint64) (sdk.Gas, uint64) {
	if len(attrs) == 0 {
		return 0, freeTier
	}
	var storedBytes uint64
	for _, l := range attrs {
		storedBytes += uint64(len(l.Key)) + uint64(len(l.Value))
	}
	storedBytes, freeTier = calcWithFreeTier(storedBytes, freeTier)
	// total Length * costs + attribute count * costs
	r := sdk.NewIntFromUint64(g.c.EventAttributeDataCost).Mul(sdk.NewIntFromUint64(storedBytes)).
		Add(sdk.NewIntFromUint64(g.c.EventPerAttributeCost).Mul(sdk.NewIntFromUint64(uint64(len(attrs)))))
	if !r.IsUint64() {
		panic(sdk.ErrorOutOfGas{Descriptor: "overflow"})
	}
	return r.Uint64(), freeTier
}

// apply free tier
func calcWithFreeTier(storedBytes uint64, freeTier uint64) (uint64, uint64) {
	if storedBytes <= freeTier {
		return 0, freeTier - storedBytes
	}
	storedBytes -= freeTier
	return storedBytes, 0
}

// ToWasmVMGas convert to wasmVM contract runtime gas unit
func (g WasmGasRegister) ToWasmVMGas(source storetypes.Gas) uint64 {
	x := source * g.c.GasMultiplier
	if x < source {
		panic(sdk.ErrorOutOfGas{Descriptor: "overflow"})
	}
	return x
}

// FromWasmVMGas converts to SDK gas unit
func (g WasmGasRegister) FromWasmVMGas(source uint64) sdk.Gas {
	return source / g.c.GasMultiplier
}
