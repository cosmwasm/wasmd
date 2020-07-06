package types

import (
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
)

const (
	ProposalTypeStoreCode                = "StoreCode"
	ProposalTypeStoreInstantiateContract = "InstantiateContract"
	ProposalTypeMigrateContract          = "MigrateContract"
	ProposalTypeUpdateAdmin              = "UpdateAdmin"
	ProposalTypeClearAdmin               = "ClearAdmin"
)

var DefaultEnabledProposals = map[string]struct{}{
	ProposalTypeStoreCode:                {},
	ProposalTypeStoreInstantiateContract: {},
	ProposalTypeMigrateContract:          {},
	ProposalTypeUpdateAdmin:              {},
	ProposalTypeClearAdmin:               {},
}

type WasmProposal struct {
	Title       string `json:"title" yaml:"title"`
	Description string `json:"description" yaml:"description"`
}

// GetTitle returns the title of a parameter change proposal.
func (p WasmProposal) GetTitle() string { return p.Title }

// GetDescription returns the description of a parameter change proposal.
func (p WasmProposal) GetDescription() string { return p.Description }

// ProposalRoute returns the routing key of a parameter change proposal.
func (p WasmProposal) ProposalRoute() string { return RouterKey }

// ValidateBasic validates the proposal
func (p WasmProposal) ValidateBasic() error {
	if len(strings.TrimSpace(p.Title)) == 0 {
		return sdkerrors.Wrap(govtypes.ErrInvalidProposalContent, "proposal title cannot be blank")
	}
	if len(p.Title) > govtypes.MaxTitleLength {
		return sdkerrors.Wrapf(govtypes.ErrInvalidProposalContent, "proposal title is longer than max length of %d", govtypes.MaxTitleLength)
	}

	if len(p.Description) == 0 {
		return sdkerrors.Wrap(govtypes.ErrInvalidProposalContent, "proposal description cannot be blank")
	}
	if len(p.Description) > govtypes.MaxDescriptionLength {
		return sdkerrors.Wrapf(govtypes.ErrInvalidProposalContent, "proposal description is longer than max length of %d", govtypes.MaxDescriptionLength)
	}
	return nil
}

type StoreCodeProposal struct {
	WasmProposal
	// Creator is the address that "owns" the code object
	Creator sdk.AccAddress `json:"creator" yaml:"creator"`
	// WASMByteCode can be raw or gzip compressed
	WASMByteCode []byte `json:"wasm_byte_code" yaml:"wasm_byte_code"`
	// Source is a valid absolute HTTPS URI to the contract's source code, optional
	Source string `json:"source" yaml:"source"`
	// Builder is a valid docker image name with tag, optional
	Builder string `json:"builder" yaml:"builder"`
}

// ProposalType returns the type
func (p StoreCodeProposal) ProposalType() string { return ProposalTypeStoreCode }

// ValidateBasic validates the proposal
func (p StoreCodeProposal) ValidateBasic() error {
	if err := p.WasmProposal.ValidateBasic(); err != nil {
		return err
	}
	if err := sdk.VerifyAddressFormat(p.Creator); err != nil {
		return err
	}

	if err := validateWasmCode(p.WASMByteCode); err != nil {
		return sdkerrors.Wrapf(sdkerrors.ErrInvalidRequest, "code bytes %s", err.Error())
	}

	if err := validateSourceURL(p.Source); err != nil {
		return sdkerrors.Wrapf(sdkerrors.ErrInvalidRequest, "source %s", err.Error())
	}

	if err := validateBuilder(p.Builder); err != nil {
		return sdkerrors.Wrapf(sdkerrors.ErrInvalidRequest, "builder %s", err.Error())
	}

	return nil
}

// String implements the Stringer interface.
func (p StoreCodeProposal) String() string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf(`Store Code Proposal:
  Title:       %s
  Description: %s
  Changes:
`, p.Title, p.Description))
	// todo: print all data
	return b.String()
}

type InstantiateContractProposal struct {
	WasmProposal
	// Creator is the address that pays the init funds
	Creator sdk.AccAddress `json:"sender" yaml:"sender"`
	// Admin is an optional address that can execute migrations
	Admin     sdk.AccAddress  `json:"admin,omitempty" yaml:"admin"`
	Code      uint64          `json:"code_id" yaml:"code_id"`
	Label     string          `json:"label" yaml:"label"`
	InitMsg   json.RawMessage `json:"init_msg" yaml:"init_msg"`
	InitFunds sdk.Coins       `json:"init_funds" yaml:"init_funds"`
}

// ProposalType returns the type
func (p InstantiateContractProposal) ProposalType() string {
	return ProposalTypeStoreInstantiateContract
}

// ValidateBasic validates the proposal
func (p InstantiateContractProposal) ValidateBasic() error {
	if err := p.WasmProposal.ValidateBasic(); err != nil {
		return err
	}
	if err := sdk.VerifyAddressFormat(p.Creator); err != nil {
		return sdkerrors.Wrap(sdkerrors.ErrInvalidRequest, "creator is required")
	}

	if p.Code == 0 {
		return sdkerrors.Wrap(sdkerrors.ErrInvalidRequest, "code_id is required")
	}

	if err := validateLabel(p.Label); err != nil {
		return err
	}

	if !p.InitFunds.IsValid() {
		return sdkerrors.ErrInvalidCoins
	}

	if len(p.Admin) != 0 {
		if err := sdk.VerifyAddressFormat(p.Admin); err != nil {
			return err
		}
	}
	return nil

}

// String implements the Stringer interface.
func (p InstantiateContractProposal) String() string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf(`Instantiate Code Proposal:
  Title:       %s
  Description: %s
  Changes:
`, p.Title, p.Description))
	// todo: print all data
	return b.String()
}

type MigrateContractProposal struct {
	WasmProposal
	Contract   sdk.AccAddress  `json:"contract" yaml:"contract"`
	Code       uint64          `json:"code_id" yaml:"code_id"`
	MigrateMsg json.RawMessage `json:"msg" yaml:"msg"`
}

// ProposalType returns the type
func (p MigrateContractProposal) ProposalType() string { return ProposalTypeMigrateContract }

// ValidateBasic validates the proposal
func (p MigrateContractProposal) ValidateBasic() error {
	if err := p.WasmProposal.ValidateBasic(); err != nil {
		return err
	}
	if p.Code == 0 {
		return sdkerrors.Wrap(sdkerrors.ErrInvalidRequest, "code_id is required")
	}
	if err := sdk.VerifyAddressFormat(p.Contract); err != nil {
		return sdkerrors.Wrap(err, "contract")
	}
	return nil
}

// String implements the Stringer interface.
func (p MigrateContractProposal) String() string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf(`Migrate Code Proposal:
  Title:       %s
  Description: %s
  Changes:
`, p.Title, p.Description))
	// todo: print all data
	return b.String()
}

type UpdateAdminContractProposal struct {
	WasmProposal
	NewAdmin sdk.AccAddress `json:"new_admin" yaml:"new_admin"`
	Contract sdk.AccAddress `json:"contract" yaml:"contract"`
}

// ProposalType returns the type
func (p UpdateAdminContractProposal) ProposalType() string { return ProposalTypeUpdateAdmin }

// ValidateBasic validates the proposal
func (p UpdateAdminContractProposal) ValidateBasic() error {
	if err := p.WasmProposal.ValidateBasic(); err != nil {
		return err
	}
	if err := sdk.VerifyAddressFormat(p.Contract); err != nil {
		return sdkerrors.Wrap(err, "contract")
	}
	if err := sdk.VerifyAddressFormat(p.NewAdmin); err != nil {
		return sdkerrors.Wrap(err, "new admin")
	}

	return nil
}

// String implements the Stringer interface.
func (p UpdateAdminContractProposal) String() string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf(`Update Code Admin Proposal:
  Title:       %s
  Description: %s
  Changes:
`, p.Title, p.Description))
	// todo: print all data
	return b.String()
}

type ClearAdminContractProposal struct {
	WasmProposal

	Contract sdk.AccAddress `json:"contract" yaml:"contract"`
}

// ProposalType returns the type
func (p ClearAdminContractProposal) ProposalType() string { return ProposalTypeClearAdmin }

// ValidateBasic validates the proposal
func (p ClearAdminContractProposal) ValidateBasic() error {
	if err := p.WasmProposal.ValidateBasic(); err != nil {
		return err
	}
	if err := sdk.VerifyAddressFormat(p.Contract); err != nil {
		return sdkerrors.Wrap(err, "contract")
	}
	return nil
}

// String implements the Stringer interface.
func (p ClearAdminContractProposal) String() string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf(`Clear Code Admin Proposal:
  Title:       %s
  Description: %s
  Changes:
`, p.Title, p.Description))
	// todo: print all data
	return b.String()
}
