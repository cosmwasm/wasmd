package types

const (
	// WasmModuleEventType is stored with any contract TX
	WasmModuleEventType = "wasm"
	// CustomContractEventPrefix contracts can create custom events. To not mix them with other system events they got the `wasm-` prefix.
	CustomContractEventPrefix = "wasm-"
	EventTypePinCode          = "pin_code"
	EventTypeUnpinCode        = "unpin_code"
)
const ( // event attributes
	AttributeKeyContractAddr = "contract_address"
	AttributeKeyCodeID       = "code_id"
	AttributeKeySigner       = "signer"
	AttributeResultDataHex   = "result"
)
