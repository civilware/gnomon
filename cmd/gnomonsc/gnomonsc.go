package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/sirupsen/logrus"

	"github.com/docopt/docopt-go"
	"github.com/ybbus/jsonrpc"

	"github.com/civilware/Gnomon/indexer"
	"github.com/civilware/Gnomon/storage"
	"github.com/civilware/Gnomon/structures"
)

var walletRPCClient jsonrpc.RPCClient
var derodRPCClient jsonrpc.RPCClient
var scid string
var ringsize uint64
var prevTH int64
var pollTime time.Duration
var thAddition int64
var gnomonIndexes []*structures.GnomonSCIDQuery
var mux sync.Mutex

var command_line string = `Gnomon
Gnomon SC Index Registration Service: As the Gnomon SCID owner, you can automatically poll your local gnomon instance for new SCIDs to append to the index SC

Usage:
  gnomonsc [options]
  gnomonsc -h | --help

Options:
  -h --help     Show this screen.
  --daemon-rpc-address=<127.0.0.1:40402>	Connect to daemon rpc.
  --wallet-rpc-address=<127.0.0.1:40403>	Connect to wallet rpc.
  --gnomon-api-address=<127.0.0.1:8082>	Gnomon api to connect to.
  --block-deploy-buffer=<10>	Block buffer inbetween SC calls. This is for safety, will be hardcoded to minimum of 2 but can define here any amount (10 default).
  --search-filter=<"Function InputStr(input String, varname String) Uint64">	Defines a search filter to match on installed SCs to add to validated list and index all actions, this will most likely change in the future but can allow for some small variability. Include escapes etc. if required. If nothing is defined, it will pull all (minus hardcoded sc).
  --sf-scid-exclusions=<"a05395bb0cf77adc850928b0db00eb5ca7a9ccbafd9a38d021c8d299ad5ce1a4;;;c9d23d2fc3aaa8e54e238a2218c0e5176a6e48780920fd8474fac5b0576110a2">     Defines a scid or scids (use const separator [default ';;;']) to be excluded from indexing regardless of search-filter. If nothing is defined, all scids that match the search-filter will be indexed.
  --skip-gnomonsc-index     If the gnomonsc is caught within the supplied search filter, you can skip indexing that SC given the size/depth of calls to that SC for increased sync times.
  --debug     Enables debug logging`

// TODO: Add as a passable param perhaps? Or other. Using ;;; for now, can be anything really.. just think what isn't used in norm SC code iterations
const sf_separator = ";;;"

// local logger
var logger *logrus.Entry

func main() {
	var err error

	//n := runtime.NumCPU()
	//runtime.GOMAXPROCS(n)

	pollTime, _ = time.ParseDuration("5s")
	ringsize = uint64(2)

	// Inspect argument(s)
	arguments, err := docopt.ParseArgs(command_line, nil, structures.Version.String())

	if err != nil {
		log.Fatalf("[Main] Error while parsing arguments err: %s", err)
	}

	// setup logging
	indexer.InitLog(arguments, os.Stdout)
	logger = structures.Logger.WithFields(logrus.Fields{})

	// Set variables from arguments
	daemon_rpc_endpoint := "127.0.0.1:40402"
	if arguments["--daemon-rpc-address"] != nil {
		daemon_rpc_endpoint = arguments["--daemon-rpc-address"].(string)
	}

	logger.Printf("[Main] Using daemon RPC endpoint %s", daemon_rpc_endpoint)

	gnomon_api_endpoint := "127.0.0.1:8082"
	if arguments["--gnomon-api-address"] != nil {
		gnomon_api_endpoint = arguments["--gnomon-api-address"].(string)
	}

	logger.Printf("[Main] Using gnomon API endpoint %s", gnomon_api_endpoint)

	wallet_rpc_endpoint := "127.0.0.1:40403"
	if arguments["--wallet-rpc-address"] != nil {
		wallet_rpc_endpoint = arguments["--wallet-rpc-address"].(string)
	}

	logger.Printf("[Main] Using wallet RPC endpoint %s", wallet_rpc_endpoint)

	thAddition = int64(10)
	if arguments["--block-deploy-buffer"] != nil {
		thAddition, err = strconv.ParseInt(arguments["--block-deploy-buffer"].(string), 10, 64)
		if err != nil {
			logger.Fatalf("[Main] ERROR while converting --block-deploy-buffer to int64")
			return
		}
		if thAddition < 2 {
			thAddition = int64(2)
		}
	}

	var search_filter []string
	if arguments["--search-filter"] != nil {
		search_filter_nonarr := arguments["--search-filter"].(string)
		search_filter = strings.Split(search_filter_nonarr, sf_separator)
		logger.Printf("[Main] Using search filter: %v", search_filter)
	} else {
		logger.Printf("[Main] No search filter defined.. grabbing all.")
	}

	var sf_scid_exclusions []string
	if arguments["--sf-scid-exclusions"] != nil {
		sf_scid_exclusions_nonarr := arguments["--sf-scid-exclusions"].(string)
		sf_scid_exclusions = strings.Split(sf_scid_exclusions_nonarr, sf_separator)
		logger.Printf("[Main] Using sf scid base exclusion list: %v", sf_scid_exclusions)
	}

	if arguments["--skip-gnomonsc-index"] != nil && arguments["--skip-gnomonsc-index"].(bool) == true {
		// TODO: Crude exclusion of both SCIDs. Proper fix should check daemon version and only exclude the relevant
		if !scidExist(sf_scid_exclusions, structures.MAINNET_GNOMON_SCID) {
			logger.Printf("[Main] Appending '%s' to scid exclusion list because --skip-gnomonsc-index was defined", structures.MAINNET_GNOMON_SCID)
			sf_scid_exclusions = append(sf_scid_exclusions, structures.MAINNET_GNOMON_SCID)
		}

		if !scidExist(sf_scid_exclusions, structures.TESTNET_GNOMON_SCID) {
			logger.Printf("[Main] Appending '%s' to scid exclusion list because --skip-gnomonsc-index was defined", structures.TESTNET_GNOMON_SCID)
			sf_scid_exclusions = append(sf_scid_exclusions, structures.TESTNET_GNOMON_SCID)
		}
	}

	logger.Printf("[Main] Using block deploy buffer of '%v' blocks.", thAddition)

	// wallet/derod rpc clients
	walletRPCClient = jsonrpc.NewClient("http://" + wallet_rpc_endpoint + "/json_rpc")
	derodRPCClient = jsonrpc.NewClient("http://" + daemon_rpc_endpoint + "/json_rpc")

	// Get testnet/mainnet
	var info rpc.GetInfo_Result
	err = derodRPCClient.CallFor(&info, "get_info")
	if err != nil {
		logger.Errorf("[Main] ERR: %v", err)
		return
	}

	// SCID
	switch info.Testnet {
	case false:
		scid = structures.MAINNET_GNOMON_SCID
	case true:
		scid = structures.TESTNET_GNOMON_SCID
	}

	for {
		fetchGnomonIndexes(gnomon_api_endpoint)
		runGnomonIndexer(daemon_rpc_endpoint, gnomon_api_endpoint, search_filter, sf_scid_exclusions)
		logger.Printf("[Main] Round completed. Sleeping 1 minute for next round.")
		time.Sleep(60 * time.Second)
	}
}

func fetchGnomonIndexes(gnomonendpoint string) {
	mux.Lock()
	defer mux.Unlock()
	var lastQuery map[string]interface{}
	var err error
	logger.Printf("[fetchGnomonIndexes] Getting sc data")
	rs, err := http.Get("http://" + gnomonendpoint + "/api/indexedscs")
	if err != nil {
		logger.Errorf("[fetchGnomonIndexes] gnomon query err %s", err)
	} else {
		logger.Printf("[fetchGnomonIndexes] Retrieved sc data... reading in and building structures.")
		b, err := io.ReadAll(rs.Body)
		if err != nil {
			logger.Errorf("[fetchGnomonIndexes] error reading body %s", err)
		} else {
			err = json.Unmarshal(b, &lastQuery)
			if err != nil {
				logger.Errorf("[fetchGnomonIndexes] error unmarshalling b %s", err)
			}

			if lastQuery["indexdetails"] != nil {
				var changes []*structures.GnomonSCIDQuery
				for _, v := range lastQuery["indexdetails"].([]interface{}) {
					x := v.(map[string]interface{})
					height := x["Height"].(float64)
					changes = append(changes, &structures.GnomonSCIDQuery{Owner: x["Owner"].(string), Height: uint64(height), SCID: x["SCID"].(string)})
				}
				gnomonIndexes = changes
			}
		}
	}
}

func runGnomonIndexer(derodendpoint string, gnomonendpoint string, search_filter []string, sf_scid_exclusions []string) {
	mux.Lock()
	defer mux.Unlock()
	var lastQuery map[string]interface{}
	var currheight int64
	logger.Printf("[runGnomonIndexer] Provisioning new RAM indexer...")
	graviton_backend, err := storage.NewGravDBRAM("25ms")
	if err != nil {
		logger.Errorf("[runGnomonIndexer] Error creating new gravdb: %v", err)
		return
	}

	// Get current height from getinfo api to poll current network states. Fallback to slow and steady mode.
	var defaultIndexer *indexer.Indexer
	logger.Printf("[fetchGnomonIndexes] Getting current height data")
	rs, err := http.Get("http://" + gnomonendpoint + "/api/getinfo")
	if err != nil {
		logger.Errorf("[fetchGnomonIndexes] gnomon height query err %s", err)
	} else {
		logger.Printf("[fetchGnomonIndexes] Retrieved getinfo data... reading in current height.")
		b, err := io.ReadAll(rs.Body)
		if err != nil {
			logger.Errorf("[fetchGnomonIndexes] error reading getinfo body %s", err)
		} else {
			err = json.Unmarshal(b, &lastQuery)
			if err != nil {
				logger.Errorf("[fetchGnomonIndexes] error unmarshalling b %s", err)
			}

			if lastQuery["getinfo"] != nil {
				for k, v := range lastQuery["getinfo"].(map[string]interface{}) {
					if k == "height" {
						currheight = int64(v.(float64))
					}
				}
			}
		}
	}

	// If we can gather the current height from /api/getinfo then start-topoheight will be passed and fastsync not used. This saves time to not check all SCIDs from gnomon SC. Otherwise default back to "slow and steady" method.
	if currheight > 0 {
		defaultIndexer = indexer.NewIndexer(graviton_backend, nil, "gravdb", nil, currheight, derodendpoint, "daemon", false, false, nil, sf_scid_exclusions, false)
		defaultIndexer.StartDaemonMode(1)
	} else {
		fsc := &structures.FastSyncConfig{Enabled: true, SkipFSRecheck: true, ForceFastSync: true, NoCode: false}
		defaultIndexer = indexer.NewIndexer(graviton_backend, nil, "gravdb", nil, int64(1), derodendpoint, "daemon", false, false, fsc, sf_scid_exclusions, false)
		defaultIndexer.StartDaemonMode(1)
	}

	for {
		if defaultIndexer.ChainHeight <= 1 || defaultIndexer.LastIndexedHeight < defaultIndexer.ChainHeight {
			logger.Printf("[runGnomonIndexer] Waiting on defaultIndexer... (%v / %v)", defaultIndexer.LastIndexedHeight, defaultIndexer.ChainHeight)
			time.Sleep(5 * time.Second)
		} else {
			break
		}
	}

	var changes bool
	var variables []*structures.SCIDVariable
	variables, _, _, _ = defaultIndexer.RPC.GetSCVariables(scid, defaultIndexer.ChainHeight, nil, nil, nil, false)

	logger.Printf("[runGnomonIndexer] Looping through discovered SCs and checking to see if any are not indexed.")
	var perc float64
	var tperc, intperc int64
	percStep := 1
	for k, v := range gnomonIndexes {
		// Crude percentage output tracker for longer running operations. Remove later, just debugging purposes.
		perc = (float64(k) / float64(len(gnomonIndexes))) * float64(100)
		intperc = int64(math.Trunc(perc))
		if intperc%int64(percStep) == 0 && tperc < intperc {
			tperc = intperc
			logger.Printf("[runGnomonIndexer] Looping... %.0f %% - %v / %v", perc, k, len(gnomonIndexes))
		}

		var contains bool
		var code string
		i := 0
		// This is slower due to lookup each time, however we need to ensure that every instance is checked as blocks happen and other gnomonsc instances could be indexing
		// TODO: Need to track mempool for changes as well
		valuesstringbykey, valuesuint64bykey, err := defaultIndexer.GetSCIDValuesByKey(variables, scid, v.SCID+"height", defaultIndexer.ChainHeight)
		if err != nil {
			// Do not attempt to index if err is returned. Possible reasons being daemon connectivity failure etc.
			logger.Errorf("[runGnomonIndexer] Skipping index of '%v' this round. GetSCIDValuesByKey errored out - %v", v.SCID, err)
			continue
		}
		if len(valuesstringbykey) > 0 {
			i++
		}
		if len(valuesuint64bykey) > 0 {
			i++
		}

		if i == 0 {
			// If we can get the SC and searchfilter is "" (get all), contains is true. Otherwise evaluate code against searchfilter
			if len(search_filter) == 0 {
				contains = true
			} else {
				_, code, _, _ = defaultIndexer.RPC.GetSCVariables(v.SCID, defaultIndexer.ChainHeight, nil, nil, nil, true)
				// Ensure scCode is not blank (e.g. an invalid scid)
				if code != "" {
					for _, sfv := range search_filter {
						contains = strings.Contains(code, sfv)
						if contains {
							// Break b/c we want to ensure contains remains true. Only care if it matches at least 1 case
							break
						}
					}
				}
			}

			if contains {
				changes = true
				logger.Printf("[runGnomonIndexer] SCID has not been indexed - %v ... Indexing now", v.SCID)
				// Do indexing job here.

				// Check txpool to see if current txns exist for indexing of same SCID
				var txpool []string
				txpool, err = defaultIndexer.RPC.GetTxPool()
				if err != nil {
					logger.Errorf("[runGnomonIndexer-GetTxPool] ERROR Getting TX Pool - %v . Skipping index of SCID '%v' for safety.", err, v.SCID)
					continue
				} else {
					logger.Printf("[runGnomonIndexer-GetTxPool] TX Pool List - %v", txpool)
				}

				var chashtxns []crypto.Hash
				var inputsc bool
				for _, tx := range txpool {
					var thash crypto.Hash
					copy(thash[:], []byte(tx)[:])
					chashtxns = append(chashtxns, thash)
				}

				cIndex := &structures.BlockTxns{Topoheight: defaultIndexer.ChainHeight, Tx_hashes: chashtxns}
				bl_sctxs, _, _, _, err := defaultIndexer.IndexTxn(cIndex, true)
				if err != nil {
					logger.Errorf("[runGnomonIndexer-IndexTxn] ERROR - %v . Skipping index of SCID '%v' for safety.", err, v.SCID)
					continue
				}

				// If no sc txns, then go ahead and input as nothing is in mempool
				if len(bl_sctxs) == 0 {
					inputsc = true
				} else {
					var txc int
					for _, txpv := range bl_sctxs {
						// Check if any of the mempool txns are for the gnomon SCID
						if txpv.Scid == scid {
							// Mempool txn is pending for gnomon SCID. Check payload to see if the SCID matches the intended SCID to be indexed
							argscid := fmt.Sprintf("%v", txpv.Sc_args.Value("scid", "S"))
							if argscid == v.SCID {
								logger.Printf("[runGnomonIndexer-inputscid] Skipping index of SCID '%v' as mempool txn '%v' includes SCID, safety.", v.SCID, txpv.Txid)
								txc++
							} else {
								logger.Printf("[runGnomonIndexer-inputscid] Gnomon SCID found in mempool txn '%v' . SCID '%v' not in the payload, continuing.", txpv.Txid, v.SCID)
							}
						}
					}
					// If no flags raised on a mempool txn matching gnomon scid -> v.SCID . Go ahead and input scid index.
					if txc == 0 {
						inputsc = true
					}
				}

				if inputsc {
					logger.Printf("[runGnomonIndexer-inputscid] Clear to input scid '%v'", v.SCID)
					// TODO: Support for authenticator/user:password rpc login for wallet interactions
					inputscid(v.SCID, v.Owner, v.Height, defaultIndexer)
				}
			}
		}
	}
	if !changes {
		logger.Printf("[runGnomonIndexer] No changes made.")
	}

	logger.Printf("[runGnomonIndexer] Closing temporary indexer...")
	defaultIndexer.Close()
	time.Sleep(5 * time.Second)
	logger.Printf("[runGnomonIndexer] Indexer closed.")
}

func inputscid(inpscid string, scowner string, deployheight uint64, defaultIndexer *indexer.Indexer) {
	// Get gas estimate based on updatecode function to calculate appropriate storage fees to append
	var rpcArgs = rpc.Arguments{}
	rpcArgs = append(rpcArgs, rpc.Argument{Name: "entrypoint", DataType: "S", Value: "InputSCID"})
	rpcArgs = append(rpcArgs, rpc.Argument{Name: "scid", DataType: "S", Value: inpscid})

	// If there is no scowner (deployed ring size > 2), then inject the gnomon indexer address
	// NOTE: Reason for injecting the gnomon indexer address (today) is because in the SC, if we need to remove/modify the entry, the scowner must do that and we don't want to lock it out.
	if scowner == "" {
		var addr rpc.GetAddress_Result
		err := walletRPCClient.CallFor(&addr, "GetAddress")
		if addr.Address == "" {
			logger.Errorf("[GetAddress] Failed - %v", err)
			return
		}

		scowner = addr.Address

		logger.Printf("[inputsc] scowner for '%s' is nil. Setting owner to '%s' for index management capabilities.", inpscid, addr.Address)
	}

	if scowner == "" {
		logger.Errorf("[inputsc] scowner for '%s' is ''. Skipping.", inpscid)
		return
	}
	rpcArgs = append(rpcArgs, rpc.Argument{Name: "scowner", DataType: "S", Value: scowner})
	rpcArgs = append(rpcArgs, rpc.Argument{Name: "deployheight", DataType: "U", Value: deployheight})
	var transfers []rpc.Transfer

	sendtx(rpcArgs, transfers, defaultIndexer)
}

func sendtx(rpcArgs rpc.Arguments, transfers []rpc.Transfer, defaultIndexer *indexer.Indexer) {
	var err error
	var gasstr rpc.GasEstimate_Result
	var addr rpc.GetAddress_Result
	err = walletRPCClient.CallFor(&addr, "GetAddress")
	if addr.Address == "" {
		logger.Errorf("[GetAddress] Failed - %v", err)
		return
	}
	gasRpc := rpcArgs
	gasRpc = append(gasRpc, rpc.Argument{Name: "SC_ACTION", DataType: "U", Value: rpc.SC_CALL})
	gasRpc = append(gasRpc, rpc.Argument{Name: "SC_ID", DataType: "H", Value: string([]byte(scid))})

	var gasestimateparams rpc.GasEstimate_Params
	if len(transfers) > 0 {
		if ringsize > 2 {
			gasestimateparams = rpc.GasEstimate_Params{SC_RPC: gasRpc, Ringsize: ringsize, Signer: "", Transfers: transfers}
		} else {
			gasestimateparams = rpc.GasEstimate_Params{SC_RPC: gasRpc, Ringsize: ringsize, Signer: addr.Address, Transfers: transfers}
		}
	} else {
		if ringsize > 2 {
			gasestimateparams = rpc.GasEstimate_Params{SC_RPC: gasRpc, Ringsize: ringsize, Signer: ""}
		} else {
			gasestimateparams = rpc.GasEstimate_Params{SC_RPC: gasRpc, Ringsize: ringsize, Signer: addr.Address}
		}
	}
	err = derodRPCClient.CallFor(&gasstr, "DERO.GetGasEstimate", gasestimateparams)
	if err != nil {
		logger.Errorf("[getGasEstimate] gas estimate err %s", err)
		return
	} else {
		logger.Printf("[getGasEstimate] gas estimate results: %v", gasstr)
	}
	var txnp rpc.Transfer_Params
	var str rpc.Transfer_Result

	txnp.SC_RPC = gasRpc
	if len(transfers) > 0 {
		txnp.Transfers = transfers
	}
	txnp.Ringsize = ringsize
	txnp.Fees = gasstr.GasStorage

	// Loop through to ensure we haven't recently sent in this session too quickly
	if prevTH != 0 {
		for {
			var info rpc.GetInfo_Result
			err := derodRPCClient.CallFor(&info, "get_info")
			if err != nil {
				logger.Errorf("[sendtx] ERR: %v", err)
				return
			}

			targetTH := prevTH + thAddition

			if targetTH <= info.TopoHeight {
				prevTH = info.TopoHeight

				// Check txpool to see if current txns exist for indexing of same SCID
				var txpool []string
				txpool, err = defaultIndexer.RPC.GetTxPool()
				if err != nil {
					logger.Errorf("[runGnomonIndexer-GetTxPool] ERROR Getting TX Pool - %v . Skipping index of SCID '%v' for safety.", err, scid)
					continue
				} else {
					logger.Printf("[runGnomonIndexer-GetTxPool] TX Pool List - %v", txpool)
				}

				break
			} else {
				logger.Printf("[sendtx] Waiting until topoheights line up to send next TX [last: %v / curr: %v]", info.TopoHeight, targetTH)
				time.Sleep(pollTime)
			}
		}
	} else {
		var info rpc.GetInfo_Result
		err := derodRPCClient.CallFor(&info, "get_info")
		if err != nil {
			logger.Errorf("[sendtx] ERR: %v", err)
			return
		}

		prevTH = info.TopoHeight
	}

	// Call Transfer (not scinvoke) since we append fees above like a normal txn.

	err = walletRPCClient.CallFor(&str, "Transfer", txnp)
	if err != nil {
		logger.Errorf("[sendtx] err: %v", err)
		return
	} else {
		logger.Printf("[sendtx] Tx sent successfully - txid: %v", str.TXID)
	}
}

// Check if value exists within a string array/slice
func scidExist(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}

	return false
}
