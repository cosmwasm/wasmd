package wasm_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/CosmWasm/go-cosmwasm"
	wasmTypes "github.com/CosmWasm/go-cosmwasm/types"
	"github.com/CosmWasm/wasmd/x/wasm"
	"github.com/CosmWasm/wasmd/x/wasm/ibc_testing"
	wasmkeeper "github.com/CosmWasm/wasmd/x/wasm/internal/keeper"
	cosmwasmv2 "github.com/CosmWasm/wasmd/x/wasm/internal/keeper/cosmwasm"
	"github.com/cosmos/cosmos-sdk/store/prefix"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	clientexported "github.com/cosmos/cosmos-sdk/x/ibc/02-client/exported"
	channeltypes "github.com/cosmos/cosmos-sdk/x/ibc/04-channel/types"
	host "github.com/cosmos/cosmos-sdk/x/ibc/24-host"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	ping = "ping"
	pong = "pong"
)
const doNotTimeout uint64 = 110000

func TestPinPong(t *testing.T) {
	var (
		coordinator = ibc_testing.NewCoordinator(t, 2)
		chainA      = coordinator.GetChain(ibc_testing.GetChainID(0))
		chainB      = coordinator.GetChain(ibc_testing.GetChainID(1))
	)
	_ = chainB.NewRandomContractInstance() // skip 1 id
	var (
		pingContractAddr = chainA.NewRandomContractInstance()
		pongContractAddr = chainB.NewRandomContractInstance()
	)
	require.NotEqual(t, pingContractAddr, pongContractAddr)

	pingContract := &player{t: t, actor: ping, chain: chainA, contractAddr: pingContractAddr}
	pongContract := &player{t: t, actor: pong, chain: chainB, contractAddr: pongContractAddr}

	wasmkeeper.MockContracts[pingContractAddr.String()] = pingContract
	wasmkeeper.MockContracts[pongContractAddr.String()] = pongContract

	var (
		sourcePortID       = wasmkeeper.PortIDForContract(pingContractAddr)
		counterpartyPortID = wasmkeeper.PortIDForContract(pongContractAddr)
	)
	clientA, clientB, connA, connB := coordinator.SetupClientConnections(chainA, chainB, clientexported.Tendermint)
	connA.NextChannelVersion = ping
	connB.NextChannelVersion = pong

	channelA, channelB := coordinator.CreateChannel(chainA, chainB, connA, connB, sourcePortID, counterpartyPortID, channeltypes.UNORDERED)
	var err error

	const startValue uint64 = 100
	const rounds = 3
	s := startGame{
		ChannelID: channelA.ID,
		Value:     startValue,
	}
	startMsg := &wasm.MsgExecuteContract{
		Sender:   chainA.SenderAccount.GetAddress(),
		Contract: pingContractAddr,
		Msg:      s.GetBytes(),
	}
	// send from chainA to chainB
	err = coordinator.SendMsgs(chainA, chainB, clientB, startMsg)
	require.NoError(t, err)

	t.Log("Duplicate messages are due to check/deliver tx calls")

	var (
		activePlayer  = ping
		pingBallValue = startValue
	)
	for i := 1; i <= rounds; i++ {
		t.Logf("++ round: %d\n", i)
		ball := NewHit(activePlayer, pingBallValue)

		seq := uint64(i)
		pkg := channeltypes.NewPacket(ball.GetBytes(), seq, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, doNotTimeout, 0)
		ack := ball.BuildAck()

		err = coordinator.RelayPacket(chainA, chainB, clientA, clientB, pkg, ack.GetBytes())
		require.NoError(t, err)
		//coordinator.CommitBlock(chainA, chainB)
		err = coordinator.UpdateClient(chainA, chainB, clientA, clientexported.Tendermint)
		require.NoError(t, err)

		// switch side
		activePlayer = counterParty(activePlayer)
		ball = NewHit(activePlayer, uint64(i))
		pkg = channeltypes.NewPacket(ball.GetBytes(), seq, channelB.PortID, channelB.ID, channelA.PortID, channelA.ID, doNotTimeout, 0)
		ack = ball.BuildAck()

		err = coordinator.RelayPacket(chainB, chainA, clientB, clientA, pkg, ack.GetBytes())
		require.NoError(t, err)
		err = coordinator.UpdateClient(chainB, chainA, clientB, clientexported.Tendermint)
		require.NoError(t, err)

		// switch side for next round
		activePlayer = counterParty(activePlayer)
		pingBallValue++
	}
	assert.Equal(t, startValue+rounds, pingContract.QueryState(lastBallSentKey))
	assert.Equal(t, uint64(rounds), pingContract.QueryState(lastBallReceivedKey))
	assert.Equal(t, uint64(rounds+1), pingContract.QueryState(sentBallsCountKey))
	assert.Equal(t, uint64(rounds), pingContract.QueryState(receivedBallsCountKey))
	assert.Equal(t, uint64(rounds), pingContract.QueryState(confirmedBallsCountKey))

	assert.Equal(t, uint64(rounds), pongContract.QueryState(lastBallSentKey))
	assert.Equal(t, startValue+rounds-1, pongContract.QueryState(lastBallReceivedKey))
	assert.Equal(t, uint64(rounds), pongContract.QueryState(sentBallsCountKey))
	assert.Equal(t, uint64(rounds), pongContract.QueryState(receivedBallsCountKey))
	assert.Equal(t, uint64(rounds), pongContract.QueryState(confirmedBallsCountKey))

}

// hit is ibc packet payload
type hit map[string]uint64

func NewHit(player string, count uint64) hit {
	return map[string]uint64{
		player: count,
	}
}
func (h hit) GetBytes() []byte {
	b, err := json.Marshal(h)
	if err != nil {
		panic(err)
	}
	return b
}
func (h hit) String() string {
	return fmt.Sprintf("Ball %s", string(h.GetBytes()))
}

func (h hit) BuildAck() hitAcknowledgement {
	return hitAcknowledgement{Success: &h}
}

func (h hit) BuildError(errMsg string) hitAcknowledgement {
	return hitAcknowledgement{Error: errMsg}
}

// hitAcknowledgement is ibc acknowledgment payload
type hitAcknowledgement struct {
	Error   string `json:"error,omitempty"`
	Success *hit   `json:"success,omitempty"`
}

func (a hitAcknowledgement) GetBytes() []byte {
	b, err := json.Marshal(a)
	if err != nil {
		panic(err)
	}
	return b
}

// startGame is an execute message payload
type startGame struct {
	ChannelID string
	Value     uint64
	// limit above the game is aborted
	MaxValue uint64 `json:"max_value,omitempty"`
}

func (g startGame) GetBytes() json.RawMessage {
	b, err := json.Marshal(g)
	if err != nil {
		panic(err)
	}
	return b
}

// player is a (mock) contract that sends and receives ibc packages
type player struct {
	t            *testing.T
	chain        *ibc_testing.TestChain
	contractAddr sdk.AccAddress
	actor        string // either ping or pong
	execCalls    int    // number of calls to Execute method (checkTx + deliverTx)
}

// Execute starts the ping pong game
func (p *player) Execute(hash []byte, params wasmTypes.Env, data []byte, store prefix.Store, api cosmwasm.GoAPI, querier wasmkeeper.QueryHandler, meter sdk.GasMeter, gas uint64) (*cosmwasmv2.HandleResponse, uint64, error) {
	p.execCalls++
	if p.execCalls%2 == 1 { // skip checkTx step because of no rollback with `chain.GetContext()`
		return &cosmwasmv2.HandleResponse{}, 0, nil
	}
	// start game
	var start startGame
	if err := json.Unmarshal(data, &start); err != nil {
		return nil, 0, err
	}

	if start.MaxValue != 0 {
		store.Set(maxValueKey, sdk.Uint64ToBigEndian(start.MaxValue))
	}
	endpoints := p.loadEndpoints(store, start.ChannelID)
	ctx := p.chain.GetContext()
	channelCap, ok := p.chain.App.WasmKeeper.ScopedKeeper.GetCapability(ctx, host.ChannelCapabilityPath(endpoints.Our.Port, endpoints.Our.Channel))
	if !ok {
		return nil, 0, sdkerrors.Wrap(channeltypes.ErrChannelCapabilityNotFound, "module does not own channel capability")
	}

	service := NewHit(p.actor, start.Value)
	p.t.Logf("[%s] starting game with: %d: %v\n", p.actor, start.Value, service)

	var seq uint64 = 1
	packet := channeltypes.NewPacket(service.GetBytes(), seq, endpoints.Our.Port, endpoints.Our.Channel, endpoints.Their.Port, endpoints.Their.Channel, doNotTimeout, 0)
	err := p.chain.App.WasmKeeper.ChannelKeeper.SendPacket(ctx, channelCap, packet)
	if err != nil {
		return nil, 0, err
	}

	p.incrementCounter(sentBallsCountKey, store)
	store.Set(lastBallSentKey, sdk.Uint64ToBigEndian(start.Value))
	return &cosmwasmv2.HandleResponse{}, 0, nil
}

// OnIBCChannelOpen ensures to accept only configured version
func (p player) OnIBCChannelOpen(hash []byte, params cosmwasmv2.Env, channel cosmwasmv2.IBCChannel, store prefix.Store, api cosmwasm.GoAPI, querier wasmkeeper.QueryHandler, meter sdk.GasMeter, gas uint64) (*cosmwasmv2.IBCChannelOpenResponse, uint64, error) {
	if channel.Version != p.actor {
		return &cosmwasmv2.IBCChannelOpenResponse{Success: false, Reason: fmt.Sprintf("expected %q but got %q", p.actor, channel.Version)}, 0, nil
	}
	return &cosmwasmv2.IBCChannelOpenResponse{Success: true}, 0, nil
}

// OnIBCChannelConnect persists connection endpoints
func (p player) OnIBCChannelConnect(hash []byte, params cosmwasmv2.Env, channel cosmwasmv2.IBCChannel, store prefix.Store, api cosmwasm.GoAPI, querier wasmkeeper.QueryHandler, meter sdk.GasMeter, gas uint64) (*cosmwasmv2.IBCChannelConnectResponse, uint64, error) {
	p.storeEndpoint(store, channel)
	return &cosmwasmv2.IBCChannelConnectResponse{}, 0, nil
}

// connectedChannelsModel is a simple persistence model to store endpoint addresses within the contract's store
type connectedChannelsModel struct {
	Our   cosmwasmv2.IBCEndpoint
	Their cosmwasmv2.IBCEndpoint
}

var ( // store keys
	ibcEndpointsKey = []byte("ibc-endpoints")
	maxValueKey     = []byte("max-value")
)

func (p player) loadEndpoints(store prefix.Store, channelID string) *connectedChannelsModel {
	var counterparties []connectedChannelsModel
	if bz := store.Get(ibcEndpointsKey); bz != nil {
		require.NoError(p.t, json.Unmarshal(bz, &counterparties))
	}
	for _, v := range counterparties {
		if v.Our.Channel == channelID {
			return &v
		}
	}
	p.t.Fatalf("no counterparty found for channel %q", channelID)
	return nil
}

func (p player) storeEndpoint(store prefix.Store, channel cosmwasmv2.IBCChannel) {
	var counterparties []connectedChannelsModel
	if b := store.Get(ibcEndpointsKey); b != nil {
		require.NoError(p.t, json.Unmarshal(b, &counterparties))
	}
	counterparties = append(counterparties, connectedChannelsModel{Our: channel.Endpoint, Their: channel.CounterpartyEndpoint})
	bz, err := json.Marshal(&counterparties)
	require.NoError(p.t, err)
	store.Set(ibcEndpointsKey, bz)
}

func (p player) OnIBCChannelClose(ctx sdk.Context, hash []byte, params cosmwasmv2.Env, channel cosmwasmv2.IBCChannel, meter sdk.GasMeter, gas uint64) (*cosmwasmv2.IBCChannelCloseResponse, uint64, error) {
	panic("implement me")
}

var ( // store keys
	lastBallSentKey        = []byte("lastBallSent")
	lastBallReceivedKey    = []byte("lastBallReceived")
	sentBallsCountKey      = []byte("sentBalls")
	receivedBallsCountKey  = []byte("recvBalls")
	confirmedBallsCountKey = []byte("confBalls")
)

// OnIBCPacketReceive receives the hit and serves a response hit via `cosmwasmv2.IBCMsg`
func (p player) OnIBCPacketReceive(hash []byte, params cosmwasmv2.Env, packet cosmwasmv2.IBCPacket, store prefix.Store, api cosmwasm.GoAPI, querier wasmkeeper.QueryHandler, meter sdk.GasMeter, gas uint64) (*cosmwasmv2.IBCPacketReceiveResponse, uint64, error) {
	// parse received data and store
	var receivedBall hit
	if err := json.Unmarshal(packet.Data, &receivedBall); err != nil {
		return &cosmwasmv2.IBCPacketReceiveResponse{
			Acknowledgement: hitAcknowledgement{Error: err.Error()}.GetBytes(),
			// no hit msg, we stop the game
		}, 0, nil
	}
	p.incrementCounter(receivedBallsCountKey, store)

	otherCount := receivedBall[counterParty(p.actor)]
	store.Set(lastBallReceivedKey, sdk.Uint64ToBigEndian(otherCount))

	if maxVal := store.Get(maxValueKey); maxVal != nil && otherCount > sdk.BigEndianToUint64(maxVal) {
		errMsg := fmt.Sprintf("max value exceeded: %d got %d", sdk.BigEndianToUint64(maxVal), otherCount)
		return &cosmwasmv2.IBCPacketReceiveResponse{
			Acknowledgement: receivedBall.BuildError(errMsg).GetBytes(),
		}, 0, nil
	}

	nextValue := p.incrementCounter(lastBallSentKey, store)
	newHit := NewHit(p.actor, nextValue)
	respHit := &cosmwasmv2.IBCMsg{SendPacket: &cosmwasmv2.IBCSendMsg{
		ChannelID:     packet.Source.Channel,
		Data:          newHit.GetBytes(),
		TimeoutHeight: doNotTimeout,
	}}
	p.incrementCounter(sentBallsCountKey, store)
	p.t.Logf("[%s] received %d, returning %d: %v\n", p.actor, otherCount, nextValue, newHit)

	return &cosmwasmv2.IBCPacketReceiveResponse{
		Acknowledgement: receivedBall.BuildAck().GetBytes(),
		Messages:        []cosmwasmv2.CosmosMsg{{IBC: respHit}},
	}, 0, nil
}

// OnIBCPacketAcknowledgement handles the packet acknowledgment frame. Stops the game on an any error
func (p player) OnIBCPacketAcknowledgement(hash []byte, params cosmwasmv2.Env, packetAck cosmwasmv2.IBCAcknowledgement, store prefix.Store, api cosmwasm.GoAPI, querier wasmkeeper.QueryHandler, meter sdk.GasMeter, gas uint64) (*cosmwasmv2.IBCPacketAcknowledgementResponse, uint64, error) {
	// parse received data and store
	var sentBall hit
	if err := json.Unmarshal(packetAck.OriginalPacket.Data, &sentBall); err != nil {
		return nil, 0, err
	}

	var ack hitAcknowledgement
	if err := json.Unmarshal(packetAck.Acknowledgement, &ack); err != nil {
		return nil, 0, err
	}
	if ack.Success != nil {
		confirmedCount := sentBall[p.actor]
		p.t.Logf("[%s] acknowledged %d: %v\n", p.actor, confirmedCount, sentBall)
	} else {
		p.t.Logf("[%s] received app layer error: %s\n", p.actor, ack.Error)

	}

	p.incrementCounter(confirmedBallsCountKey, store)
	return &cosmwasmv2.IBCPacketAcknowledgementResponse{}, 0, nil
}

func (p player) OnIBCPacketTimeout(hash []byte, params cosmwasmv2.Env, packet cosmwasmv2.IBCPacket, store prefix.Store, api cosmwasm.GoAPI, querier wasmkeeper.QueryHandler, meter sdk.GasMeter, gas uint64) (*cosmwasmv2.IBCPacketTimeoutResponse, uint64, error) {
	panic("implement me")
}

func (p player) incrementCounter(key []byte, store prefix.Store) uint64 {
	var count uint64
	bz := store.Get(key)
	if bz != nil {
		count = sdk.BigEndianToUint64(bz)
	}
	count++
	store.Set(key, sdk.Uint64ToBigEndian(count))
	return count
}

func (p player) QueryState(key []byte) uint64 {
	models := p.chain.App.WasmKeeper.QueryRaw(p.chain.GetContext(), p.contractAddr, key)
	require.Len(p.t, models, 1)
	return sdk.BigEndianToUint64(models[0].Value)
}

func counterParty(s string) string {
	switch s {
	case ping:
		return pong
	case pong:
		return ping
	default:
		panic(fmt.Sprintf("unsupported: %q", s))
	}
}
