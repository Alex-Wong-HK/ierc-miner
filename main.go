package main

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"math/rand"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	log "github.com/sirupsen/logrus"
)

var (
	priv      *ecdsa.PrivateKey
	address   common.Address
	ethClient *ethclient.Client
	dataTemp  string
)
var (
	globalNonce = time.Now().UnixNano()
	zeroAddress = common.HexToAddress("0x0000000000000000000000000000000000000000")
	chainID     = big.NewInt(0)
	stopChan    = make(chan struct{})
	userNonce   = -1
)

func main() {
	dataTemp = fmt.Sprintf(`data:application/json,{"p":"ierc-20","op":"mint","tick":"%s","amt":"%d","nonce":"%%d"}`, config.Tick, config.Amt)
	var err error
	ethClient, err = ethclient.Dial(config.Rpc)
	if err != nil {
		panic(err)
	}

	chainID, err = ethClient.ChainID(context.Background())
	if err != nil {
		panic(err)
	}

	bytePriv, err := hexutil.Decode(config.PrivateKey)
	if err != nil {
		panic(err)
	}
	prv, _ := btcec.PrivKeyFromBytes(bytePriv)
	priv = prv.ToECDSA()
	address = crypto.PubkeyToAddress(*prv.PubKey().ToECDSA())
	log.WithFields(log.Fields{
		"prefix":   config.Prefix,
		"amt":      config.Amt,
		"tick":     config.Tick,
		"count":    config.Count,
		"address":  address.String(),
		"chain_id": chainID.Int64(),
	}).Info("prepare done")

	startNonce := globalNonce
	go func() {
		for {
			last := globalNonce
			time.Sleep(time.Second * 10)
			log.WithFields(log.Fields{
				"hash_rate":  fmt.Sprintf("%dhashes/s", (globalNonce-last)/10),
				"hash_count": globalNonce - startNonce,
			}).Info()
		}
	}()

	wg := new(sync.WaitGroup)
	for i := 0; i < config.Count; i++ {
		tx := makeBaseTx()
		wg.Add(runtime.NumCPU())
		ctx, cancel := context.WithCancel(context.Background())
		for j := 0; j < runtime.NumCPU(); j++ {
			go func(ctx context.Context, cancelFunc context.CancelFunc) {
				for {
					select {
					case <-ctx.Done():
						wg.Done()
						return
					default:
						makeTx(cancelFunc, tx)
					}
				}
			}(ctx, cancel)
		}
		wg.Wait()
	}
}

func makeTx(cancelFunc context.CancelFunc, innerTx *types.DynamicFeeTx) {
	atomic.AddInt64(&globalNonce, 1)
	temp := fmt.Sprintf(dataTemp, globalNonce)
	innerTx.Data = []byte(temp)
	tx := types.NewTx(innerTx)
	signedTx, _ := types.SignTx(tx, types.NewCancunSigner(chainID), priv)
	if strings.HasPrefix(signedTx.Hash().String(), config.Prefix) {
		log.WithFields(log.Fields{
			"tx_hash": signedTx.Hash().String(),
			"data":    temp,
		}).Info("found new transaction")

		err := ethClient.SendTransaction(context.Background(), signedTx)
		if err != nil {
			log.WithFields(log.Fields{
				"tx_hash": signedTx.Hash().String(),
				"err":     err,
			}).Error("failed to send transaction")
		} else {
			log.WithFields(log.Fields{
				"tx_hash": signedTx.Hash().String(),
			}).Info("broadcast transaction")
		}

		cancelFunc()
	}
}

func makeBaseTx() *types.DynamicFeeTx {
	if userNonce < 0 {
		nonce, err := ethClient.PendingNonceAt(context.Background(), address)
		if err != nil {
			panic(err)
		}
		userNonce = int(nonce)
	} else {
		userNonce++
	}
	innerTx := &types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     uint64(userNonce),
		GasTipCap: new(big.Int).Mul(big.NewInt(1000000000), big.NewInt(int64(config.GasTip))),
		GasFeeCap: new(big.Int).Mul(big.NewInt(1000000000), big.NewInt(int64(config.GasMax))),
		Gas:       30000 + uint64(rand.Intn(1000)),
		To:        &zeroAddress,
		Value:     big.NewInt(0),
	}

	return innerTx
}
