package keeper

import (
	"context"
	"github.com/CosmWasm/wasmd/x/wasm/types"
	wasmvmtypes "github.com/CosmWasm/wasmvm/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestHasWasmModuleEvent(t *testing.T) {
	myContractAddr := RandomAccountAddress(t)
	specs := map[string]struct {
		srcEvents []sdk.Event
		exp       bool
	}{
		"event found": {
			srcEvents: []sdk.Event{
				sdk.NewEvent(types.WasmModuleEventType, sdk.NewAttribute("contract_address", myContractAddr.String())),
			},
			exp: true,
		},
		"different event: not found": {
			srcEvents: []sdk.Event{
				sdk.NewEvent(types.CustomContractEventPrefix, sdk.NewAttribute("contract_address", myContractAddr.String())),
			},
			exp: false,
		},
		"event with different address: not found": {
			srcEvents: []sdk.Event{
				sdk.NewEvent(types.WasmModuleEventType, sdk.NewAttribute("contract_address", RandomBech32AccountAddress(t))),
			},
			exp: false,
		},
		"no event": {
			srcEvents: []sdk.Event{},
			exp:       false,
		},
	}
	for name, spec := range specs {
		t.Run(name, func(t *testing.T) {
			em := sdk.NewEventManager()
			em.EmitEvents(spec.srcEvents)
			ctx := sdk.Context{}.WithContext(context.Background()).WithEventManager(em)

			got := hasWasmModuleEvent(ctx, myContractAddr)
			assert.Equal(t, spec.exp, got)
		})
	}
}

func TestNewCustomEvents(t *testing.T) {
	myContract := RandomAccountAddress(t)
	specs := map[string]struct {
		src wasmvmtypes.Events
		exp sdk.Events
	}{
		"all good": {
			src: wasmvmtypes.Events{{
				Type:       "foo",
				Attributes: []wasmvmtypes.EventAttribute{{Key: "myKey", Value: "myVal"}},
			}},
			exp: sdk.Events{sdk.NewEvent("wasm-foo",
				sdk.NewAttribute("contract_address", myContract.String()),
				sdk.NewAttribute("myKey", "myVal"))},
		},
		"multiple attributes": {
			src: wasmvmtypes.Events{{
				Type: "foo",
				Attributes: []wasmvmtypes.EventAttribute{{Key: "myKey", Value: "myVal"},
					{Key: "myOtherKey", Value: "myOtherVal"}},
			}},
			exp: sdk.Events{sdk.NewEvent("wasm-foo",
				sdk.NewAttribute("contract_address", myContract.String()),
				sdk.NewAttribute("myKey", "myVal"),
				sdk.NewAttribute("myOtherKey", "myOtherVal"))},
		},
		"multiple events": {
			src: wasmvmtypes.Events{{
				Type:       "foo",
				Attributes: []wasmvmtypes.EventAttribute{{Key: "myKey", Value: "myVal"}},
			}, {
				Type:       "bar",
				Attributes: []wasmvmtypes.EventAttribute{{Key: "otherKey", Value: "otherVal"}},
			}},
			exp: sdk.Events{sdk.NewEvent("wasm-foo",
				sdk.NewAttribute("contract_address", myContract.String()),
				sdk.NewAttribute("myKey", "myVal")),
				sdk.NewEvent("wasm-bar",
					sdk.NewAttribute("contract_address", myContract.String()),
					sdk.NewAttribute("otherKey", "otherVal"))},
		},
		"without attributes": {
			src: wasmvmtypes.Events{{
				Type: "foo",
			}},
			exp: sdk.Events{sdk.NewEvent("wasm-foo",
				sdk.NewAttribute("contract_address", myContract.String()))},
		},
		"min length not reached": {
			src: wasmvmtypes.Events{{
				Type: "f",
			}},
			exp: sdk.Events{},
		},
		"overwrite contract_address": {
			src: wasmvmtypes.Events{{
				Type:       "foo",
				Attributes: []wasmvmtypes.EventAttribute{{Key: "contract_address", Value: RandomBech32AccountAddress(t)}},
			}},
			exp: sdk.Events{sdk.NewEvent("wasm-foo",
				sdk.NewAttribute("contract_address", myContract.String()))},
		},
	}
	for name, spec := range specs {
		t.Run(name, func(t *testing.T) {
			gotEvent := newCustomEvents(spec.src, myContract)
			assert.Equal(t, spec.exp, gotEvent)
		})
	}
}

func TestNewWasmModuleEvent(t *testing.T) {
	myContract := RandomAccountAddress(t)
	specs := map[string]struct {
		src []wasmvmtypes.EventAttribute
		exp sdk.Events
	}{
		"all good": {
			src: []wasmvmtypes.EventAttribute{{Key: "myKey", Value: "myVal"}},
			exp: sdk.Events{sdk.NewEvent("wasm",
				sdk.NewAttribute("contract_address", myContract.String()),
				sdk.NewAttribute("myKey", "myVal"))},
		},
		"multiple attributes": {
			src: []wasmvmtypes.EventAttribute{{Key: "myKey", Value: "myVal"},
				{Key: "myOtherKey", Value: "myOtherVal"}},
			exp: sdk.Events{sdk.NewEvent("wasm",
				sdk.NewAttribute("contract_address", myContract.String()),
				sdk.NewAttribute("myKey", "myVal"),
				sdk.NewAttribute("myOtherKey", "myOtherVal"))},
		},
		"without attributes": {
			exp: sdk.Events{sdk.NewEvent("wasm",
				sdk.NewAttribute("contract_address", myContract.String()))},
		},
		"overwrite contract_address": {
			src: []wasmvmtypes.EventAttribute{{Key: "contract_address", Value: RandomBech32AccountAddress(t)}},
			exp: sdk.Events{sdk.NewEvent("wasm",
				sdk.NewAttribute("contract_address", myContract.String()))},
		},
	}
	for name, spec := range specs {
		t.Run(name, func(t *testing.T) {
			gotEvent := newWasmModuleEvent(spec.src, myContract)
			assert.Equal(t, spec.exp, gotEvent)
		})
	}
}
