package indexer

import (
	"context"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/civilware/Gnomon/rwc"
	"github.com/civilware/Gnomon/structures"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"github.com/gorilla/websocket"
)

type Client struct {
	WS  *websocket.Conn
	RPC *jrpc2.Client
	sync.RWMutex
}

func (client *Client) Connect(endpoint string) (err error) {
	var daemon_uri string

	// Used to check if the endpoint has changed.. if so, then close WS to current and update WS
	if client.WS != nil {
		remAddr := client.WS.RemoteAddr()
		var pingpong string
		err2 := client.RPC.CallResult(context.Background(), "DERO.Ping", nil, &pingpong)
		if strings.Contains(remAddr.String(), endpoint) && err2 == nil {
			// Endpoint is the same, continue on
			return
		} else {
			// Remote addr (current ws connection endpoint) does not match indexer endpoint - re-connecting
			client.Lock()
			defer client.Unlock()
			client.WS.Close()
		}
	}

	// Trim off http, https, wss, ws to get endpoint to use for connecting
	if strings.HasPrefix(endpoint, "https") {
		ld := strings.TrimPrefix(strings.ToLower(endpoint), "https://")
		daemon_uri = "wss://" + ld + "/ws"

		client.WS, _, err = websocket.DefaultDialer.Dial(daemon_uri, nil)
	} else if strings.HasPrefix(endpoint, "http") {
		ld := strings.TrimPrefix(strings.ToLower(endpoint), "http://")
		daemon_uri = "ws://" + ld + "/ws"

		client.WS, _, err = websocket.DefaultDialer.Dial(daemon_uri, nil)
	} else if strings.HasPrefix(endpoint, "wss") {
		ld := strings.TrimPrefix(strings.ToLower(endpoint), "wss://")
		daemon_uri = "wss://" + ld + "/ws"

		client.WS, _, err = websocket.DefaultDialer.Dial(daemon_uri, nil)
	} else if strings.HasPrefix(endpoint, "ws") {
		ld := strings.TrimPrefix(strings.ToLower(endpoint), "ws://")
		daemon_uri = "ws://" + ld + "/ws"

		client.WS, _, err = websocket.DefaultDialer.Dial(daemon_uri, nil)
	} else {
		daemon_uri = "ws://" + endpoint + "/ws"

		client.WS, _, err = websocket.DefaultDialer.Dial(daemon_uri, nil)
	}

	// notify user of any state change
	// if daemon connection breaks or comes live again
	if err == nil {
		if !Connected {
			logger.Printf("[Connect] Connection to RPC server successful - %s", daemon_uri)
			Connected = true
		}
	} else {
		logger.Errorf("[Connect] ERROR connecting to endpoint %v", err)

		if Connected {
			logger.Errorf("[Connect] ERROR - Connection to RPC server Failed - %s", daemon_uri)
		}
		Connected = false
		return err
	}

	input_output := rwc.New(client.WS)
	client.RPC = jrpc2.NewClient(channel.RawJSON(input_output, input_output), nil)

	return err
}

// DERO.GetBlockHeaderByTopoHeight rpc call for returning block hash at a particular topoheight
func (client *Client) getBlockHash(height uint64) (hash string, err error) {
	//logger.Debugf("[getBlockHash] Attempting to get block details at topoheight %v", height)
	// TODO: Make this a consumable func with rpc calls and timeout / wait / retry logic for deduplication of code. Or use alternate method of checking [primary use case is remote nodes]
	var reconnect_count int
	for {
		var err error

		var io rpc.GetBlockHeaderByHeight_Result
		var ip = rpc.GetBlockHeaderByTopoHeight_Params{TopoHeight: height}

		if err = client.RPC.CallResult(context.Background(), "DERO.GetBlockHeaderByTopoHeight", ip, &io); err != nil {
			logger.Debugf("[getBlockHash] %v - GetBlockHeaderByTopoHeight failed: %v . Trying again (%v / 5)", height, err, reconnect_count)
			//return hash, fmt.Errorf("GetBlockHeaderByTopoHeight failed: %v", err)

			// TODO: Perhaps just a .Closing = true call here and then gnomonserver can be polling for any indexers with .Closing then close the rest cleanly. If packaged, then just have to handle themselves w/ .Close()
			if reconnect_count >= 5 {
				logger.Errorf("[getBlockHash] %v - GetBlockHeaderByTopoHeight failed: %v . (%v / 5 times)", height, err, reconnect_count)
				break
			}
			time.Sleep(1 * time.Second)

			reconnect_count++

			continue
		} else {
			//logger.Debugf("[getBlockHash] Retrieved block header from topoheight %v", height)
			//mainnet = !info.Testnet // inverse of testnet is mainnet
			//logger.Debugf("%v", io)
		}

		hash = io.Block_Header.Hash
		break
	}

	return hash, err
}

// DERO.GetTxPool rpc call for returning current mempool txns
func (client *Client) GetTxPool() (txlist []string, err error) {
	// TODO: Make this a consumable func with rpc calls and timeout / wait / retry logic for deduplication of code. Or use alternate method of checking [primary use case is remote nodes]
	var reconnect_count int
	for {
		var err error

		var io rpc.GetTxPool_Result

		if err = client.RPC.CallResult(context.Background(), "DERO.GetTxPool", nil, &io); err != nil {
			if reconnect_count >= 5 {
				logger.Errorf("[getTxPool] GetTxPool failed: %v . (%v / 5 times)", err, reconnect_count)
				break
			}
			time.Sleep(1 * time.Second)

			reconnect_count++

			continue
		}

		txlist = io.Tx_list
		break
	}

	return
}

// Gets SC variable details
func (client *Client) GetSCVariables(scid string, topoheight int64, keysuint64 []uint64, keysstring []string, keysbytes [][]byte, codeonly bool) (variables []*structures.SCIDVariable, code string, balances map[string]uint64, err error) {
	//balances = make(map[string]uint64)

	isAlpha := regexp.MustCompile(`^[A-Za-z]+$`).MatchString

	var getSCResults rpc.GetSC_Result
	var getSCParams rpc.GetSC_Params
	if codeonly {
		getSCParams = rpc.GetSC_Params{SCID: scid, Code: true, Variables: false, TopoHeight: topoheight}
	} else {
		getSCParams = rpc.GetSC_Params{SCID: scid, Code: true, Variables: true, TopoHeight: topoheight}
	}
	if client.WS == nil {
		return
	}

	// TODO: Make this a consumable func with rpc calls and timeout / wait / retry logic for deduplication of code. Or use alternate method of checking [primary use case is remote nodes]
	var reconnect_count int
	for {
		if err = client.RPC.CallResult(context.Background(), "DERO.GetSC", getSCParams, &getSCResults); err != nil {
			// Catch for v139 daemons that reject >1024 var returns and we need to be specific (if defined, otherwise we'll err out after 5 tries)
			if strings.Contains(err.Error(), "max 1024 variables can be returned") || strings.Contains(err.Error(), "namesc cannot request all variables") {
				if keysuint64 != nil || keysstring != nil || keysbytes != nil {
					getSCParams = rpc.GetSC_Params{SCID: scid, Code: true, Variables: false, TopoHeight: topoheight, KeysUint64: keysuint64, KeysString: keysstring, KeysBytes: keysbytes}
				} else {
					// Default to at least return code true and variables false if we run into max var can't be returned (derod v139)
					getSCParams = rpc.GetSC_Params{SCID: scid, Code: true, Variables: false, TopoHeight: topoheight}
				}
			}

			logger.Debugf("[GetSCVariables] ERROR - GetSCVariables failed for '%v': %v . Trying again (%v / 5)", scid, err, reconnect_count+1)
			if reconnect_count >= 5 {
				logger.Errorf("[GetSCVariables] ERROR - GetSCVariables failed for '%v': %v . (%v / 5 times)", scid, err, reconnect_count)
				return variables, code, balances, err
			}
			time.Sleep(1 * time.Second)

			reconnect_count++

			continue
		}

		break
	}

	code = getSCResults.Code

	for k, v := range getSCResults.VariableStringKeys {
		currVar := &structures.SCIDVariable{}
		// TODO: Do we need to store "C" through these means? If we don't , need to update the len(scVars) etc. calls to ensure that even a 0 count goes through, but perhaps validate off code/balances
		/*
			if k == "C" {
				continue
			}
		*/
		currVar.Key = k
		switch cval := v.(type) {
		case float64:
			currVar.Value = uint64(cval)
		case uint64:
			currVar.Value = cval
		case string:
			// hex decode since all strings are hex encoded
			dstr, _ := hex.DecodeString(cval)
			// Check if dstr is an address raw
			p := new(crypto.Point)
			if err := p.DecodeCompressed(dstr); err == nil {

				addr := rpc.NewAddressFromKeys(p)
				currVar.Value = addr.String()
			} else {
				// Check specific patterns which reflect STORE() operations of TXID(), SCID(), etc.
				str := string(dstr)
				if len(str) == crypto.HashLength {
					var h crypto.Hash
					copy(h[:crypto.HashLength], []byte(str)[:])

					if len(h.String()) == 64 && !isAlpha(str) {
						if !crypto.HashHexToHash(str).IsZero() {
							currVar.Value = str
						} else {
							currVar.Value = h.String()
						}
					} else {
						currVar.Value = str
					}
				} else {
					currVar.Value = str
				}
			}
		default:
			// non-string/uint64 (shouldn't be here actually since it's either uint64 or string conversion)
			str := fmt.Sprintf("%v", cval)
			// Check specific patterns which reflect STORE() operations of TXID(), SCID(), etc.
			if len(str) == crypto.HashLength {
				var h crypto.Hash
				copy(h[:crypto.HashLength], []byte(str)[:])

				if len(h.String()) == 64 && !isAlpha(str) {
					if !crypto.HashHexToHash(str).IsZero() {
						currVar.Value = str
					} else {
						currVar.Value = h.String()
					}
				} else {
					currVar.Value = str
				}
			} else {
				currVar.Value = str
			}
		}
		variables = append(variables, currVar)
	}

	for k, v := range getSCResults.VariableUint64Keys {
		currVar := &structures.SCIDVariable{}
		currVar.Key = k
		switch cval := v.(type) {
		case string:
			// hex decode since all strings are hex encoded
			decd, _ := hex.DecodeString(cval)
			p := new(crypto.Point)
			if err := p.DecodeCompressed(decd); err == nil {

				addr := rpc.NewAddressFromKeys(p)
				currVar.Value = addr.String()
			} else {
				// Check specific patterns which reflect STORE() operations of TXID(), SCID(), etc.
				str := string(decd)
				if len(str) == crypto.HashLength {
					var h crypto.Hash
					copy(h[:crypto.HashLength], []byte(str)[:])

					if len(h.String()) == 64 && !isAlpha(str) {
						if !crypto.HashHexToHash(str).IsZero() {
							currVar.Value = str
						} else {
							currVar.Value = h.String()
						}
					} else {
						currVar.Value = str
					}
				} else {
					currVar.Value = str
				}
			}
		case uint64:
			currVar.Value = cval
		case float64:
			currVar.Value = uint64(cval)
		default:
			// non-string/uint64 (shouldn't be here actually since it's either uint64 or string conversion)
			str := fmt.Sprintf("%v", cval)
			// Check specific patterns which reflect STORE() operations of TXID(), SCID(), etc.
			if len(str) == crypto.HashLength {
				var h crypto.Hash
				copy(h[:crypto.HashLength], []byte(str)[:])

				if len(h.String()) == 64 && !isAlpha(str) {
					if !crypto.HashHexToHash(str).IsZero() {
						currVar.Value = str
					} else {
						currVar.Value = h.String()
					}
				} else {
					currVar.Value = str
				}
			} else {
				currVar.Value = str
			}
		}
		variables = append(variables, currVar)
	}

	// Derod v139 workaround. Everything that returns normal should always have variables of count at least 1 for 'C', but even if not these should still loop on nil and not produce bad data.
	// We loop for safety, however returns really should only ever satisfy 1 variable end of the day since 1 key matches to 1 value. But bruteforce it for workaround
	if len(variables) == 0 {
		for _, ku := range keysuint64 {
			currVar := &structures.SCIDVariable{}
			for _, v := range getSCResults.ValuesUint64 {
				currVar.Key = ku
				currVar.Value = v
				// TODO: Perhaps a more appropriate err match to the graviton codebase rather than just the 'leaf not found' string.
				if strings.Contains(v, "leaf not found") {
					continue
				}
				variables = append(variables, currVar)
			}
		}
		for _, ks := range keysstring {
			currVar := &structures.SCIDVariable{}
			for _, v := range getSCResults.ValuesString {
				currVar.Key = ks
				currVar.Value = v
				// TODO: Perhaps a more appropriate err match to the graviton codebase rather than just the 'leaf not found' string.
				if strings.Contains(v, "leaf not found") {
					continue
				}
				variables = append(variables, currVar)
			}
		}
		for _, kb := range keysbytes {
			currVar := &structures.SCIDVariable{}
			for _, v := range getSCResults.ValuesBytes {
				currVar.Key = kb
				currVar.Value = v
				// TODO: Perhaps a more appropriate err match to the graviton codebase rather than just the 'leaf not found' string.
				if strings.Contains(v, "leaf not found") {
					continue
				}
				variables = append(variables, currVar)
			}
		}
	}

	balances = getSCResults.Balances

	return variables, code, balances, err
}
