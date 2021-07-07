package keeper

import (
	"fmt"
	"github.com/CosmWasm/wasmd/x/wasm/types"
	wasmvmtypes "github.com/CosmWasm/wasmvm/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// newWasmModuleEvent creates with wasm module event for interacting with the given contract. Adds custom attributes
// to this event.
func newWasmModuleEvent(customAttributes []wasmvmtypes.EventAttribute, contractAddr sdk.AccAddress) sdk.Events {
	attrs := contractSDKEventAttributes(customAttributes, contractAddr)

	// each wasm invocation always returns one sdk.Event
	return sdk.Events{sdk.NewEvent(types.WasmModuleEventType, attrs...)}
}

// returns true when a wasm module event was emitted for this contract already
func hasWasmModuleEvent(ctx sdk.Context, contractAddr sdk.AccAddress) bool {
	for _, e := range ctx.EventManager().Events() {
		if e.Type == types.WasmModuleEventType {
			for _, a := range e.Attributes {
				if string(a.Key) == types.AttributeKeyContractAddr && string(a.Value) == contractAddr.String() {
					return true
				}
			}
		}
	}
	return false
}

const eventTypeMinLength = 2

// newCustomEvents converts wasmvm events from a contract response to sdk type events
func newCustomEvents(evts wasmvmtypes.Events, contractAddr sdk.AccAddress) sdk.Events {
	events := make(sdk.Events, 0, len(evts))
	for _, e := range evts {
		if len(e.Type) <= eventTypeMinLength {
			continue
		}
		attributes := contractSDKEventAttributes(e.Attributes, contractAddr)
		events = append(events, sdk.NewEvent(fmt.Sprintf("%s%s", types.CustomContractEventPrefix, e.Type), attributes...))
	}
	return events
}

// convert and add contract address issuing this event
func contractSDKEventAttributes(customAttributes []wasmvmtypes.EventAttribute, contractAddr sdk.AccAddress) []sdk.Attribute {
	attrs := []sdk.Attribute{sdk.NewAttribute(types.AttributeKeyContractAddr, contractAddr.String())}
	// append attributes from wasm to the sdk.Event
	for _, l := range customAttributes {
		// and reserve the contract_address key for our use (not contract)
		if l.Key != types.AttributeKeyContractAddr {
			attrs = append(attrs, sdk.NewAttribute(l.Key, l.Value))
		}
	}
	return attrs
}
