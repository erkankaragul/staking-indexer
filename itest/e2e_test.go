//go:build e2e
// +build e2e

package e2etest

import (
	"encoding/hex"
	"encoding/json"
	"math/rand"
	"testing"
	"time"

	"github.com/babylonchain/babylon/btcstaking"
	bbndatagen "github.com/babylonchain/babylon/testutil/datagen"
	bbnbtclightclienttypes "github.com/babylonchain/babylon/x/btclightclient/types"
	queuecli "github.com/babylonchain/staking-queue-client/client"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/stretchr/testify/require"

	"github.com/babylonchain/staking-indexer/cmd/sid/cli"
	"github.com/babylonchain/staking-indexer/config"
	"github.com/babylonchain/staking-indexer/testutils"
	"github.com/babylonchain/staking-indexer/testutils/datagen"
	"github.com/babylonchain/staking-indexer/types"
)

func TestBTCScanner(t *testing.T) {
	n := 100
	tm := StartManagerWithNBlocks(t, n, uint64(n))
	defer tm.Stop()

	count, err := tm.BitcoindHandler.GetBlockCount()
	require.NoError(t, err)
	require.Equal(t, n, count)

	k := int(tm.VersionedParams.ParamsVersions[0].ConfirmationDepth)

	_ = tm.BitcoindHandler.GenerateBlocks(10)

	tm.WaitForNConfirmations(t, k)
}

func TestQueueConsumer(t *testing.T) {
	// create event consumer
	queueCfg := config.DefaultQueueConfig()
	queueConsumer, err := setupTestQueueConsumer(t, queueCfg)
	require.NoError(t, err)
	stakingChan, err := queueConsumer.StakingQueue.ReceiveMessages()
	require.NoError(t, err)

	defer queueConsumer.Stop()

	n := 1
	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	stakingEventList := make([]*queuecli.ActiveStakingEvent, 0)
	for i := 0; i < n; i++ {
		stakingEvent := &queuecli.ActiveStakingEvent{
			EventType:        queuecli.ActiveStakingEventType,
			StakingTxHashHex: hex.EncodeToString(bbndatagen.GenRandomByteArray(r, 10)),
		}
		err = queueConsumer.PushStakingEvent(stakingEvent)
		require.NoError(t, err)
		stakingEventList = append(stakingEventList, stakingEvent)
	}

	for i := 0; i < n; i++ {
		stakingEventBytes := <-stakingChan
		var receivedStakingEvent queuecli.ActiveStakingEvent
		err = json.Unmarshal([]byte(stakingEventBytes.Body), &receivedStakingEvent)
		require.NoError(t, err)
		require.Equal(t, stakingEventList[i].StakingTxHashHex, receivedStakingEvent.StakingTxHashHex)
		err = queueConsumer.StakingQueue.DeleteMessage(stakingEventBytes.Receipt)
		require.NoError(t, err)
	}
}

// TestStakingLifeCycle covers the following life cycle
// 1. the staking tx is sent to BTC
// 2. the staking tx is parsed by the indexer
// 3. wait until the staking tx expires
// 4. the subsequent withdraw tx is sent to BTC
// 5. the withdraw tx is identified by the indexer and consumed by the queue
func TestStakingLifeCycle(t *testing.T) {
	// ensure we have UTXOs
	n := 101
	tm := StartManagerWithNBlocks(t, n, 100)
	defer tm.Stop()

	// generate valid staking tx data
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	// TODO: test with multiple system parameters
	sysParams := tm.VersionedParams.ParamsVersions[0]
	k := uint64(sysParams.ConfirmationDepth)
	testStakingData := datagen.GenerateTestStakingData(t, r, sysParams)
	stakingInfo, err := btcstaking.BuildV0IdentifiableStakingOutputs(
		sysParams.Tag,
		tm.WalletPrivKey.PubKey(),
		testStakingData.FinalityProviderKey,
		sysParams.CovenantPks,
		sysParams.CovenantQuorum,
		testStakingData.StakingTime,
		testStakingData.StakingAmount,
		regtestParams,
	)
	require.NoError(t, err)

	// send the staking tx and mine blocks
	require.NoError(t, err)
	stakingTx, err := testutils.CreateTxFromOutputsAndSign(
		tm.WalletClient,
		[]*wire.TxOut{stakingInfo.OpReturnOutput, stakingInfo.StakingOutput},
		1000,
		tm.MinerAddr,
	)
	require.NoError(t, err)
	stakingTxHash := stakingTx.TxHash()
	tm.SendTxWithNConfirmations(t, stakingTx, int(k))

	// check that the staking tx is already stored
	tm.WaitForStakingTxStored(t, stakingTxHash)

	// check the staking event is received by the queue
	tm.CheckNextStakingEvent(t, stakingTxHash)

	// wait for the staking tx expires
	if uint64(testStakingData.StakingTime) > k {
		tm.BitcoindHandler.GenerateBlocks(int(uint64(testStakingData.StakingTime) - k))
	}

	// build and send withdraw tx and mine blocks
	withdrawSpendInfo, err := stakingInfo.TimeLockPathSpendInfo()
	require.NoError(t, err)

	storedStakingTx, err := tm.Si.GetStakingTxByHash(&stakingTxHash)
	require.NoError(t, err)
	require.NotNil(t, storedStakingTx)
	withdrawTx := testutils.BuildWithdrawTx(
		t,
		tm.WalletPrivKey,
		stakingTx.TxOut[storedStakingTx.StakingOutputIdx],
		stakingTx.TxHash(),
		storedStakingTx.StakingOutputIdx,
		withdrawSpendInfo,
		testStakingData.StakingTime,
		testStakingData.StakingAmount,
		regtestParams,
	)
	tm.SendTxWithNConfirmations(t, withdrawTx, int(k))

	// check the withdraw event is received
	tm.CheckNextWithdrawEvent(t, stakingTx.TxHash())
}

func TestUnconfirmedTVL(t *testing.T) {
	// ensure we have UTXOs
	n := 101
	tm := StartManagerWithNBlocks(t, n, 100)
	defer tm.Stop()

	tm.CheckNextUnconfirmedEvent(t, 0, 0)

	// generate valid staking tx data
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	// TODO: test with multiple system parameters
	sysParams := tm.VersionedParams.ParamsVersions[0]
	k := sysParams.ConfirmationDepth

	// build staking tx
	testStakingData := datagen.GenerateTestStakingData(t, r, sysParams)
	stakingInfo, err := btcstaking.BuildV0IdentifiableStakingOutputs(
		sysParams.Tag,
		tm.WalletPrivKey.PubKey(),
		testStakingData.FinalityProviderKey,
		sysParams.CovenantPks,
		sysParams.CovenantQuorum,
		testStakingData.StakingTime,
		testStakingData.StakingAmount,
		regtestParams,
	)
	require.NoError(t, err)

	// send the staking tx and mine 1 block to trigger
	// unconfirmed calculation
	require.NoError(t, err)
	stakingTx, err := testutils.CreateTxFromOutputsAndSign(
		tm.WalletClient,
		[]*wire.TxOut{stakingInfo.OpReturnOutput, stakingInfo.StakingOutput},
		1000,
		tm.MinerAddr,
	)
	require.NoError(t, err)
	tm.SendTxWithNConfirmations(t, stakingTx, 1)
	tm.CheckNextUnconfirmedEvent(t, 0, uint64(stakingInfo.StakingOutput.Value))

	// confirm the staking tx
	tm.BitcoindHandler.GenerateBlocks(int(k))
	tm.WaitForNConfirmations(t, int(k))
	tm.CheckNextStakingEvent(t, stakingTx.TxHash())
	tm.CheckNextUnconfirmedEvent(t, uint64(stakingInfo.StakingOutput.Value), uint64(stakingInfo.StakingOutput.Value))

	// build and send unbonding tx from the previous staking tx
	unbondingSpendInfo, err := stakingInfo.UnbondingPathSpendInfo()
	require.NoError(t, err)
	stakingTxHash := stakingTx.TxHash()
	unbondingTx := testutils.BuildUnbondingTx(
		t,
		sysParams,
		tm.WalletPrivKey,
		testStakingData.FinalityProviderKey,
		testStakingData.StakingAmount,
		&stakingTxHash,
		1,
		unbondingSpendInfo,
		stakingTx,
		getCovenantPrivKeys(t),
		regtestParams,
	)
	tm.SendTxWithNConfirmations(t, unbondingTx, 1)
	tm.CheckNextUnconfirmedEvent(t, uint64(stakingInfo.StakingOutput.Value), 0)

	// confirm the unbonding tx
	tm.BitcoindHandler.GenerateBlocks(int(k))
	tm.WaitForNConfirmations(t, int(k))
	tm.CheckNextUnconfirmedEvent(t, 0, 0)
	tm.CheckNextUnbondingEvent(t, unbondingTx.TxHash())
}

// TestIndexerRestart tests following cases upon restart
//  1. it restarts from a previous height before a staking tx is found.
//     We expect the staking event to be replayed
//  2. it restarts exactly from the height it just processed.
//     We expect the staking event not to be replayed
func TestIndexerRestart(t *testing.T) {
	// ensure we have UTXOs
	n := 101
	tm := StartManagerWithNBlocks(t, n, 100)
	defer tm.Stop()

	// generate valid staking tx data
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	// TODO: test with multiple system parameters
	sysParams := tm.VersionedParams.ParamsVersions[0]
	k := sysParams.ConfirmationDepth
	testStakingData := datagen.GenerateTestStakingData(t, r, sysParams)
	stakingInfo, err := btcstaking.BuildV0IdentifiableStakingOutputs(
		sysParams.Tag,
		tm.WalletPrivKey.PubKey(),
		testStakingData.FinalityProviderKey,
		sysParams.CovenantPks,
		sysParams.CovenantQuorum,
		testStakingData.StakingTime,
		testStakingData.StakingAmount,
		regtestParams,
	)
	require.NoError(t, err)

	// send the staking tx and mine blocks
	require.NoError(t, err)
	stakingTx, err := testutils.CreateTxFromOutputsAndSign(
		tm.WalletClient,
		[]*wire.TxOut{stakingInfo.OpReturnOutput, stakingInfo.StakingOutput},
		1000,
		tm.MinerAddr,
	)
	require.NoError(t, err)
	stakingTxHash := stakingTx.TxHash()
	tm.SendTxWithNConfirmations(t, stakingTx, int(k))

	// check that the staking tx is already stored
	tm.WaitForStakingTxStored(t, stakingTxHash)

	// check the staking event is received by the queue
	tm.CheckNextStakingEvent(t, stakingTxHash)

	// restart from a height before staking tx
	restartedTm := ReStartFromHeight(t, tm, uint64(n))
	defer restartedTm.Stop()

	// check the staking event is replayed
	restartedTm.CheckNextStakingEvent(t, stakingTxHash)

	// restart the testing manager again from last processed height + 1
	restartedTm2 := ReStartFromHeight(t, restartedTm, restartedTm.Si.GetStartHeight())
	defer restartedTm2.Stop()

	// no staking event should be replayed as
	// the indexer starts from a higher height
	restartedTm2.CheckNoStakingEvent(t)
}

// TestStakingUnbondingLifeCycle covers the following life cycle
// 1. the staking tx is sent to BTC
// 2. the staking tx is parsed by the indexer
// 3. the subsequent unbonding tx is sent to BTC
// 4. the unbonding tx is identified by the indexer
// 5. the subsequent withdraw tx is sent to BTC
// 6. the withdraw tx is identified by the indexer
func TestStakingUnbondingLifeCycle(t *testing.T) {
	// ensure we have UTXOs
	n := 101
	tm := StartManagerWithNBlocks(t, n, 100)
	defer tm.Stop()

	// generate valid staking tx data
	// TODO: test with multiple system parameters
	sysParams := tm.VersionedParams.ParamsVersions[0]
	k := uint64(sysParams.ConfirmationDepth)
	testStakingData := getTestStakingData(t)
	stakingInfo, err := btcstaking.BuildV0IdentifiableStakingOutputs(
		sysParams.Tag,
		tm.WalletPrivKey.PubKey(),
		testStakingData.FinalityProviderKey,
		sysParams.CovenantPks,
		sysParams.CovenantQuorum,
		testStakingData.StakingTime,
		testStakingData.StakingAmount,
		regtestParams,
	)
	require.NoError(t, err)

	// send the staking tx and mine blocks
	require.NoError(t, err)
	stakingTx, err := testutils.CreateTxFromOutputsAndSign(
		tm.WalletClient,
		[]*wire.TxOut{stakingInfo.OpReturnOutput, stakingInfo.StakingOutput},
		1000,
		tm.MinerAddr,
	)
	require.NoError(t, err)
	stakingTxHash := stakingTx.TxHash()
	tm.SendTxWithNConfirmations(t, stakingTx, int(k))

	// check that the staking tx is already stored
	tm.WaitForStakingTxStored(t, stakingTxHash)

	// check the staking event is received by the queue
	tm.CheckNextStakingEvent(t, stakingTxHash)

	// build and send unbonding tx from the previous staking tx
	unbondingSpendInfo, err := stakingInfo.UnbondingPathSpendInfo()
	require.NoError(t, err)
	storedStakingTx, err := tm.Si.GetStakingTxByHash(&stakingTxHash)
	require.NoError(t, err)
	require.NotNil(t, storedStakingTx)
	unbondingTx := testutils.BuildUnbondingTx(
		t,
		sysParams,
		tm.WalletPrivKey,
		testStakingData.FinalityProviderKey,
		testStakingData.StakingAmount,
		&stakingTxHash,
		storedStakingTx.StakingOutputIdx,
		unbondingSpendInfo,
		stakingTx,
		getCovenantPrivKeys(t),
		regtestParams,
	)
	tm.SendTxWithNConfirmations(t, unbondingTx, int(k))

	// check the unbonding tx is already stored
	tm.WaitForUnbondingTxStored(t, unbondingTx.TxHash())

	// check the unbonding event is received
	tm.CheckNextUnbondingEvent(t, unbondingTx.TxHash())

	// wait for the unbonding tx expires
	if uint64(sysParams.UnbondingTime) > k {
		tm.BitcoindHandler.GenerateBlocks(int(uint64(sysParams.UnbondingTime) - k))
	}

	// build and send withdraw tx from the unbonding tx
	unbondingInfo, err := btcstaking.BuildUnbondingInfo(
		tm.WalletPrivKey.PubKey(),
		[]*btcec.PublicKey{testStakingData.FinalityProviderKey},
		sysParams.CovenantPks,
		sysParams.CovenantQuorum,
		sysParams.UnbondingTime,
		testStakingData.StakingAmount.MulF64(0.9),
		regtestParams,
	)
	require.NoError(t, err)
	withdrawSpendInfo, err := unbondingInfo.TimeLockPathSpendInfo()
	require.NoError(t, err)
	withdrawTx := testutils.BuildWithdrawTx(
		t,
		tm.WalletPrivKey,
		// unbonding tx only has one output
		unbondingTx.TxOut[0],
		unbondingTx.TxHash(),
		0,
		withdrawSpendInfo,
		sysParams.UnbondingTime,
		testStakingData.StakingAmount,
		regtestParams,
	)
	tm.SendTxWithNConfirmations(t, withdrawTx, int(k))

	// wait until the indexer identifies the withdraw tx
	tm.WaitForNConfirmations(t, int(k))

	// check the withdraw event is consumed
	tm.CheckNextWithdrawEvent(t, stakingTx.TxHash())
}

func TestBtcHeaders(t *testing.T) {
	r := rand.New(rand.NewSource(10))
	blocksPerRetarget := 2016
	genState := bbnbtclightclienttypes.DefaultGenesis()

	initBlocksQnt := r.Intn(15) + blocksPerRetarget
	btcd, btcClient := StartBtcClientAndBtcHandler(t, initBlocksQnt)

	// from zero height
	infos, err := cli.BtcHeaderInfoList(btcClient, 0, uint64(initBlocksQnt))
	require.NoError(t, err)
	require.Equal(t, len(infos), initBlocksQnt+1)

	// should be valid on genesis, start from zero height.
	genState.BtcHeaders = infos
	require.NoError(t, genState.Validate())

	generatedBlocksQnt := r.Intn(15) + 2
	btcd.GenerateBlocks(generatedBlocksQnt)
	totalBlks := initBlocksQnt + generatedBlocksQnt

	// check from height with interval
	fromBlockHeight := blocksPerRetarget - 1
	toBlockHeight := totalBlks - 2

	infos, err = cli.BtcHeaderInfoList(btcClient, uint64(fromBlockHeight), uint64(toBlockHeight))
	require.NoError(t, err)
	require.Equal(t, len(infos), int(toBlockHeight-fromBlockHeight)+1)

	// try to check if it is valid on genesis, should fail is not retarget block.
	genState.BtcHeaders = infos
	require.EqualError(t, genState.Validate(), "genesis block must be a difficulty adjustment block")

	// from retarget block
	infos, err = cli.BtcHeaderInfoList(btcClient, uint64(blocksPerRetarget), uint64(totalBlks))
	require.NoError(t, err)
	require.Equal(t, len(infos), int(totalBlks-blocksPerRetarget)+1)

	// check if it is valid on genesis
	genState.BtcHeaders = infos
	require.NoError(t, genState.Validate())
}

func buildUnbondingTx(
	t *testing.T,
	params *types.GlobalParams,
	stakerPrivKey *btcec.PrivateKey,
	fpKey *btcec.PublicKey,
	stakingAmount btcutil.Amount,
	stakingTxHash *chainhash.Hash,
	stakingOutputIdx uint32,
	unbondingSpendInfo *btcstaking.SpendInfo,
	stakingTx *wire.MsgTx,
	covPrivKeys []*btcec.PrivateKey,
) *wire.MsgTx {
	expectedOutputValue := stakingAmount - params.UnbondingFee
	unbondingInfo, err := btcstaking.BuildUnbondingInfo(
		stakerPrivKey.PubKey(),
		[]*btcec.PublicKey{fpKey},
		params.CovenantPks,
		params.CovenantQuorum,
		params.UnbondingTime,
		expectedOutputValue,
		regtestParams,
	)
	require.NoError(t, err)

	unbondingTx := wire.NewMsgTx(2)
	unbondingTx.AddTxIn(wire.NewTxIn(wire.NewOutPoint(stakingTxHash, stakingOutputIdx), nil, nil))
	unbondingTx.AddTxOut(unbondingInfo.UnbondingOutput)

	// generate covenant unbonding sigs
	unbondingCovSigs := make([]*schnorr.Signature, len(covPrivKeys))
	for i, privKey := range covPrivKeys {
		sig, err := btcstaking.SignTxWithOneScriptSpendInputStrict(
			unbondingTx,
			stakingTx,
			stakingOutputIdx,
			unbondingSpendInfo.GetPkScriptPath(),
			privKey,
		)
		require.NoError(t, err)

		unbondingCovSigs[i] = sig
	}

	stakerUnbondingSig, err := btcstaking.SignTxWithOneScriptSpendInputFromScript(
		unbondingTx,
		stakingTx.TxOut[stakingOutputIdx],
		stakerPrivKey,
		unbondingSpendInfo.RevealedLeaf.Script,
	)
	require.NoError(t, err)

	witness, err := unbondingSpendInfo.CreateUnbondingPathWitness(unbondingCovSigs, stakerUnbondingSig)
	require.NoError(t, err)
	unbondingTx.TxIn[0].Witness = witness

	return unbondingTx
}

func buildWithdrawTx(
	t *testing.T,
	stakerPrivKey *btcec.PrivateKey,
	fundTxOutput *wire.TxOut,
	fundTxHash chainhash.Hash,
	fundTxOutputIndex uint32,
	fundTxSpendInfo *btcstaking.SpendInfo,
	lockTime uint16,
	lockedAmount btcutil.Amount,
) *wire.MsgTx {

	destAddress, err := btcutil.NewAddressPubKey(stakerPrivKey.PubKey().SerializeCompressed(), regtestParams)

	require.NoError(t, err)
	destAddressScript, err := txscript.PayToAddrScript(destAddress)
	require.NoError(t, err)

	// to spend output with relative timelock transaction need to be version two or higher
	withdrawTx := wire.NewMsgTx(2)
	withdrawTx.AddTxIn(wire.NewTxIn(wire.NewOutPoint(&fundTxHash, fundTxOutputIndex), nil, nil))
	withdrawTx.AddTxOut(wire.NewTxOut(int64(lockedAmount.MulF64(0.5)), destAddressScript))

	// we need to set sequence number before signing, as signing commits to sequence
	// number
	withdrawTx.TxIn[0].Sequence = uint32(lockTime)

	sig, err := btcstaking.SignTxWithOneScriptSpendInputFromTapLeaf(
		withdrawTx,
		fundTxOutput,
		stakerPrivKey,
		fundTxSpendInfo.RevealedLeaf,
	)

	require.NoError(t, err)

	witness, err := fundTxSpendInfo.CreateTimeLockPathWitness(sig)

	require.NoError(t, err)

	withdrawTx.TxIn[0].Witness = witness

	return withdrawTx
}

func getCovenantPrivKeys(t *testing.T) []*btcec.PrivateKey {
	// private keys of the covenant committee which correspond to the public keys in test-params.json
	covenantPrivKeysHex := []string{
		"6a2369c2c9f5cd3c4242834228acdc38b73e5b8930f5f4a9b69e6eaf557e60ed",
	}

	privKeys := make([]*btcec.PrivateKey, len(covenantPrivKeysHex))
	for i, skHex := range covenantPrivKeysHex {
		skBytes, err := hex.DecodeString(skHex)
		require.NoError(t, err)
		sk, _ := btcec.PrivKeyFromBytes(skBytes)
		privKeys[i] = sk
	}

	return privKeys
}

func getTestStakingData(
	t *testing.T,
) *datagen.TestStakingData {
	stakerPrivKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)

	fpPrivKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)

	stakingAmount := btcutil.Amount(100000)
	stakingTime := uint16(100)

	return &datagen.TestStakingData{
		StakerKey:           stakerPrivKey.PubKey(),
		FinalityProviderKey: fpPrivKey.PubKey(),
		StakingAmount:       stakingAmount,
		StakingTime:         stakingTime,
	}
}
