package indexer

import (
	"context"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/civilware/Gnomon/mbllookup"
	"github.com/civilware/Gnomon/rwc"
	"github.com/civilware/Gnomon/storage"
	"github.com/civilware/Gnomon/structures"

	"github.com/deroproject/derohe/block"
	"github.com/deroproject/derohe/cryptography/bn256"
	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/transaction"
	"github.com/deroproject/graviton"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"github.com/gorilla/websocket"

	"github.com/sirupsen/logrus"
	prefixed "github.com/x-cray/logrus-prefixed-formatter"
)

type Client struct {
	WS  *websocket.Conn
	RPC *jrpc2.Client
	sync.RWMutex
}

type SCIDToIndexStage struct {
	scid     string
	fsi      *structures.FastSyncImport
	scVars   []*structures.SCIDVariable
	scCode   string
	contains bool
}

type Indexer struct {
	LastIndexedHeight int64
	ChainHeight       int64
	SearchFilter      []string
	GravDBBackend     *storage.GravitonStore
	BBSBackend        *storage.BboltStore
	DBType            string
	Closing           bool
	RPC               *Client
	Endpoint          string
	RunMode           string
	MBLLookup         bool
	ValidatedSCs      []string
	CloseOnDisconnect bool
	Fastsync          bool
	sync.RWMutex
}

// Defines the number of blocks to jump when testing pruned nodes.
const block_jump = int64(10000)

// String set of hardcoded scids which are appended to in NewIndexer. These are used for reference points such as ignoring invoke calls for indexer.Fastsync == true among other procedures.
var hardcodedscids []string

var Connected bool = false

// local logger
var logger *logrus.Entry

func NewIndexer(Graviton_backend *storage.GravitonStore, Bbs_backend *storage.BboltStore, dbtype string, search_filter []string, last_indexedheight int64, endpoint string, runmode string, mbllookup bool, closeondisconnect bool, fastsync bool) *Indexer {
	hardcodedscids = append(hardcodedscids, "0000000000000000000000000000000000000000000000000000000000000001")

	logger = structures.Logger.WithFields(logrus.Fields{})

	return &Indexer{
		LastIndexedHeight: last_indexedheight,
		SearchFilter:      search_filter,
		GravDBBackend:     Graviton_backend,
		BBSBackend:        Bbs_backend,
		DBType:            dbtype,
		RPC:               &Client{},
		Endpoint:          endpoint,
		RunMode:           runmode,
		MBLLookup:         mbllookup,
		CloseOnDisconnect: closeondisconnect,
		Fastsync:          fastsync,
	}
}

func (indexer *Indexer) StartDaemonMode(blockParallelNum int) {
	var err error

	// Simple connect loop .. if connection fails initially then keep trying, else break out and continue on. Connect() is handled in getInfo() for retries later on if connection ceases again
	for {
		if indexer.Closing {
			// Break out on closing call
			break
		}
		logger.Printf("[StartDaemonMode] Trying to connect...")
		err = indexer.RPC.Connect(indexer.Endpoint)
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}
		break
	}
	time.Sleep(1 * time.Second)

	// Continuously getInfo from daemon to update topoheight globally
	go indexer.getInfo()
	time.Sleep(1 * time.Second)

	for {
		if indexer.Closing {
			// Break out on closing call
			break
		}
		if indexer.ChainHeight == int64(0) {
			logger.Printf("[StartDaemonMode] Waiting on GetInfo...")
			time.Sleep(1 * time.Second)
			continue
		}
		break
	}

	var storedindex int64
	switch indexer.DBType {
	case "gravdb":
		storedindex = indexer.GravDBBackend.GetLastIndexHeight()
	case "boltdb":
		storedindex, err = indexer.BBSBackend.GetLastIndexHeight()
		if err != nil {
			logger.Fatalf("[bbs-StartDaemonMode] Could not get last index height - %v", err)
		}
	}

	// If storedindex returns 0, first opening, and fastsync is enabled set index to current chain height
	if storedindex == 0 && indexer.Fastsync {
		logger.Printf("[StartDaemonMode] Fastsync initiated, setting to chainheight (%v)", indexer.ChainHeight)
		storedindex = indexer.ChainHeight
	}

	for _, vi := range hardcodedscids {
		if scidExist(indexer.ValidatedSCs, vi) {
			// Hardcoded SCID already exists, no need to re-add
			continue
		}

		scVars, scCode, _, _ := indexer.RPC.GetSCVariables(vi, indexer.ChainHeight, nil, nil, nil)

		var contains bool

		// If we can get the SC and searchfilter is "" (get all), contains is true. Otherwise evaluate code against searchfilter
		if len(indexer.SearchFilter) == 0 {
			contains = true
		} else {
			// Ensure scCode is not blank (e.g. an invalid scid)
			if scCode != "" {
				for _, sfv := range indexer.SearchFilter {
					contains = strings.Contains(scCode, sfv)
					if contains {
						// Break b/c we want to ensure contains remains true. Only care if it matches at least 1 case
						break
					}
				}
			}
		}

		if contains {
			//logger.Printf("[AddSCIDToIndex] Hardcoded SCID matches search filter. Adding SCID %v", vi)
			indexer.Lock()
			indexer.ValidatedSCs = append(indexer.ValidatedSCs, vi)
			indexer.Unlock()
			writeWait, _ := time.ParseDuration("20ms")
			switch indexer.DBType {
			case "gravdb":
				for indexer.GravDBBackend.Writing == 1 {
					if indexer.Closing {
						return
					}
					//logger.Printf("[Indexer-NewIndexer] GravitonDB is writing... sleeping for %v...", writeWait)
					time.Sleep(writeWait)
				}
				indexer.GravDBBackend.Writing = 1
				var ctrees []*graviton.Tree
				sotree, sochanges, err := indexer.GravDBBackend.StoreOwner(vi, "", true)
				if err != nil {
					logger.Printf("[StartDaemonMode-hardcodedscids] Error storing owner: %v", err)
				} else {
					if sochanges {
						ctrees = append(ctrees, sotree)
					}
				}
				svdtree, svdchanges, err := indexer.GravDBBackend.StoreSCIDVariableDetails(vi, scVars, indexer.ChainHeight, true)
				if err != nil {
					logger.Printf("[StartDaemonMode-hardcodedscids] ERR - storing scid variable details: %v", err)
				} else {
					if svdchanges {
						ctrees = append(ctrees, svdtree)
					}
				}
				sihtree, sihchanges, err := indexer.GravDBBackend.StoreSCIDInteractionHeight(vi, indexer.ChainHeight, true)
				if err != nil {
					logger.Printf("[StartDaemonMode-hardcodedscids] ERR - storing scid interaction height: %v", err)
				} else {
					if sihchanges {
						ctrees = append(ctrees, sihtree)
					}
				}
				if len(ctrees) > 0 {
					_, err := indexer.GravDBBackend.CommitTrees(ctrees)
					if err != nil {
						logger.Printf("[StartDaemonMode-hardcodedscids] ERR - committing trees: %v", err)
					} else {
						//logger.Printf("[StartDaemonMode-hardcodedscids] DEBUG - cv [%v]", cv)
					}
				}
				indexer.GravDBBackend.Writing = 0
			case "boltdb":
				for indexer.BBSBackend.Writing == 1 {
					if indexer.Closing {
						return
					}
					//logger.Printf("[Indexer-StartDaemonMode-hardcodedscids] BoltDB is writing... sleeping for %v... writer %v...", writeWait, indexer.BBSBackend.Writer)
					time.Sleep(writeWait)
				}
				indexer.BBSBackend.Writing = 1
				indexer.BBSBackend.Writer = "StartDaemonMode"
				_, err := indexer.BBSBackend.StoreOwner(vi, "")
				if err != nil {
					logger.Printf("[StartDaemonMode-hardcodedscids] Error storing owner: %v", err)
				}
				_, err = indexer.BBSBackend.StoreSCIDVariableDetails(vi, scVars, indexer.ChainHeight)
				if err != nil {
					logger.Printf("[StartDaemonMode-hardcodedscids] ERR - storing scid variable details: %v", err)
				}
				_, err = indexer.BBSBackend.StoreSCIDInteractionHeight(vi, indexer.ChainHeight)
				if err != nil {
					logger.Printf("[StartDaemonMode-hardcodedscids] ERR - storing scid interaction height: %v", err)
				}
				indexer.BBSBackend.Writing = 0
				indexer.BBSBackend.Writer = ""
			}
		}
	}

	if storedindex > indexer.LastIndexedHeight {
		logger.Printf("[StartDaemonMode-storedIndex] Continuing from last indexed height %v", storedindex)
		indexer.Lock()
		indexer.LastIndexedHeight = storedindex
		indexer.Unlock()

		// We can also assume this check to mean we have stored validated SCs potentially. TODO: Do we just get stored SCs regardless of sync cycle?
		pre_validatedSCIDs := make(map[string]string)
		switch indexer.DBType {
		case "gravdb":
			pre_validatedSCIDs = indexer.GravDBBackend.GetAllOwnersAndSCIDs()
		case "boltdb":
			pre_validatedSCIDs = indexer.BBSBackend.GetAllOwnersAndSCIDs()
		}

		if len(pre_validatedSCIDs) > 0 {
			logger.Printf("[StartDaemonMode] Appending pre-validated SCIDs from store to memory.")

			for k := range pre_validatedSCIDs {
				indexer.Lock()
				indexer.ValidatedSCs = append(indexer.ValidatedSCs, k)
				indexer.Unlock()
			}
		}

		var getinfo *structures.GetInfo
		switch indexer.DBType {
		case "gravdb":
			getinfo = indexer.GravDBBackend.GetGetInfoDetails()
		case "boltdb":
			getinfo = indexer.BBSBackend.GetGetInfoDetails()
		}

		// Only pull in gnomonsc data if fastsync is defined. TODO: Maybe extra flag for checking this on startup as well.
		if getinfo != nil && indexer.Fastsync {
			// Define gnomon builtin scid for indexing
			var gnomon_scid string
			if !getinfo.Testnet {
				gnomon_scid = structures.MAINNET_GNOMON_SCID
			} else {
				gnomon_scid = structures.TESTNET_GNOMON_SCID
			}

			// All could be future optimized .. for now it's slower but works.
			variables, code, _, err := indexer.RPC.GetSCVariables(gnomon_scid, indexer.ChainHeight, nil, nil, nil)
			if err == nil && len(variables) > 0 {
				_ = code
				keysstring, _, _ := indexer.GetSCIDValuesByKey(variables, gnomon_scid, "signature", indexer.ChainHeight)

				// Check  if keysstring is nil or not to avoid any sort of panics
				var sigstr string
				if len(keysstring) > 0 {
					sigstr = keysstring[0]
				}

				validated, _, err := indexer.ValidateSCSignature(code, sigstr)
				if err != nil {
					logger.Printf("%v", err)
				}

				// Ensure SC signature is validated (LOAD("signature") checks out to code validation)
				if validated || err != nil {
					logger.Printf("[StartDaemonMode-fastsync] Gnomon SC '%v' code VALID - proceeding to inject scid data.", gnomon_scid)

					scidstoadd := make(map[string]*structures.FastSyncImport)

					// Check k/v pairs for the necessary info: keys/values - scid/headers, scidowner/owner, scidheight/height
					for _, v := range variables {
						switch ckey := v.Key.(type) {
						case string:
							if v.Value != nil {
								switch len(ckey) {
								case 64:
									// Check for k/v scid/headers
									if scidstoadd[ckey] == nil {
										scidstoadd[ckey] = &structures.FastSyncImport{}
									}
									scidstoadd[ckey].Headers = v.Value.(string)
								case 69:
									// Check for k/v scidowner/owner
									if scidstoadd[ckey[0:64]] == nil {
										scidstoadd[ckey[0:64]] = &structures.FastSyncImport{}
									}
									scidstoadd[ckey[0:64]].Owner = v.Value.(string)
								case 70:
									// Check for k/v scidheight/height
									if scidstoadd[ckey[0:64]] == nil {
										scidstoadd[ckey[0:64]] = &structures.FastSyncImport{}
									}
									scidstoadd[ckey[0:64]].Height = v.Value.(uint64)
								default:
									// Nothing - only should match defined ckey lengths
								}
							}
						default:
							// Nothing - expect only string for value types specifically to Gnomon
						}
					}

					err := indexer.AddSCIDToIndex(scidstoadd)
					if err != nil {
						logger.Printf("[StartDaemonMode-fastsync] ERR - adding scids to index - %v", err)
					}
				} else {
					logger.Printf("[StartDaemonMode-fastsync] Gnomon SC '%v' code was NOT validated against in-built signature variable. Skipping auto-population of scids.", gnomon_scid)
				}
			} else {
				if err != nil {
					logger.Printf("[StartDaemonMode] Fastsync failed to build GnomonSC index. Error - '%v'. Are you using daemon v139? Syncing from current chain height.", err)
				} else {
					logger.Printf("[StartDaemonMode] Fastsync failed to build GnomonSC index. Variables returned - '%v'. Are you using daemon v139? Syncing from current chain height.", len(variables))
				}
			}
		}
	}

	if blockParallelNum <= 0 {
		blockParallelNum = 1
	}
	logger.Printf("[StartDaemonMode] Set number of parallel blocks to index to '%v'", blockParallelNum)

	go func() {
		k := 0
		for {
			if indexer.Closing {
				// Break out on closing call
				break
			}

			// Temp stop for testing/checking data
			/*
				if indexer.LastIndexedHeight >= 1000 {
					logger.Printf("Indexer reached %v , sleeping 60 seconds.", indexer.LastIndexedHeight)
					time.Sleep(time.Second * 60)
					continue
				}
			*/

			if indexer.LastIndexedHeight >= indexer.ChainHeight {
				time.Sleep(1 * time.Second)
				continue
			}

			// Check to cover fastsync scenarios; no reason to pull all this logic into the multi-block scanning components - this will only run once
			if k == 0 {
				_, err := indexer.RPC.getBlockHash(uint64(indexer.LastIndexedHeight))
				if err != nil {
					// Handle pruned nodes index errors... find height that they have blocks able to be indexed
					//logger.Printf("Checking if strings contain: %v", err.Error())
					if strings.Contains(err.Error(), "err occured empty block") || strings.Contains(err.Error(), "err occured file does not exist") {
						currIndex := indexer.LastIndexedHeight
						rewindIndex := int64(0)
						for {
							if indexer.Closing {
								// If we do concurrent blocks in the future, this will need to move/be modified to be *after* all concurrent blocks are done incase exit etc.
								writeWait, _ := time.ParseDuration("20ms")
								switch indexer.DBType {
								case "gravdb":
									for indexer.GravDBBackend.Writing == 1 {
										if indexer.Closing {
											return
										}
										//logger.Printf("[Indexer-NewIndexer] GravitonDB is writing... sleeping for %v...", writeWait)
										time.Sleep(writeWait)
									}
									indexer.GravDBBackend.Writing = 1
									indexer.GravDBBackend.StoreLastIndexHeight(currIndex, false)
									indexer.GravDBBackend.Writing = 0
								case "boltdb":
									for indexer.BBSBackend.Writing == 1 {
										if indexer.Closing {
											return
										}
										//logger.Printf("[Indexer-StartDaemonMode-StoreLastIndexHeight] BoltDB is writing... sleeping for %v... writer %v...", writeWait, indexer.BBSBackend.Writer)
										time.Sleep(writeWait)
									}
									indexer.BBSBackend.Writing = 1
									indexer.BBSBackend.Writer = "StartDaemonMode"
									indexer.BBSBackend.StoreLastIndexHeight(currIndex)
									indexer.BBSBackend.Writing = 0
									indexer.BBSBackend.Writer = ""
								}
								// Break out on closing call
								break
							}
							_, err = indexer.RPC.getBlockHash(uint64(currIndex))
							if err != nil {
								//if strings.Contains(err.Error(), "err occured empty block") {
								//time.Sleep(200 * time.Millisecond)	// sleep for node spam, not *required* but can be useful for lesser nodes in brief catchup time.
								// Increase block by 10 to not spam the daemon at every single block, but skip along a little bit to move faster/more less impact to node. This can be modified if required.
								if (currIndex + block_jump) > indexer.ChainHeight {
									currIndex = indexer.ChainHeight
								} else {
									currIndex += block_jump
								}
								logger.Printf("GetBlock failed - checking %v", currIndex)
								//}
							} else {
								// Self-contain and loop through at most 10 or X blocks
								logger.Printf("GetBlock worked at %v", currIndex)
								for {
									if indexer.Closing {
										// If we do concurrent blocks in the future, this will need to move/be modified to be *after* all concurrent blocks are done incase exit etc.
										writeWait, _ := time.ParseDuration("20ms")
										switch indexer.DBType {
										case "gravdb":
											for indexer.GravDBBackend.Writing == 1 {
												if indexer.Closing {
													return
												}
												//logger.Printf("[Indexer-NewIndexer] GravitonDB is writing... sleeping for %v...", writeWait)
												time.Sleep(writeWait)
											}
											indexer.GravDBBackend.Writing = 1
											indexer.GravDBBackend.StoreLastIndexHeight(rewindIndex, false)
											indexer.GravDBBackend.Writing = 0
										case "boltdb":
											for indexer.BBSBackend.Writing == 1 {
												if indexer.Closing {
													return
												}
												//logger.Printf("[Indexer-StartDaemonMode-StoreLastIndexHeight] BoltDB is writing... sleeping for %v... writer %v...", writeWait, indexer.BBSBackend.Writer)
												time.Sleep(writeWait)
											}
											indexer.BBSBackend.Writing = 1
											indexer.BBSBackend.Writer = "StartDaemonMode"
											indexer.BBSBackend.StoreLastIndexHeight(rewindIndex)
											indexer.BBSBackend.Writing = 0
											indexer.BBSBackend.Writer = ""
										}

										// Break out on closing call
										break
									}
									if rewindIndex == 0 {
										rewindIndex = currIndex - block_jump + 1
										if rewindIndex < 0 {
											rewindIndex = 1
										}
									} else {
										logger.Printf("Checking GetBlock at %v", rewindIndex)
										_, err = indexer.RPC.getBlockHash(uint64(rewindIndex))
										if err != nil {
											rewindIndex++
											//time.Sleep(200 * time.Millisecond)	// sleep for node spam, not *required* but can be useful for lesser nodes in brief catchup time.
										} else {
											logger.Printf("GetBlock worked at %v - continuing as normal", rewindIndex+1)
											// Break out, we found the earliest block detail
											indexer.Lock()
											indexer.LastIndexedHeight = rewindIndex + 1
											indexer.Unlock()
											break
										}
									}
								}
								break
							}
						}
					}

					logger.Printf("[mainFOR] ERROR - %v", err)
					time.Sleep(1 * time.Second)
					continue
				}
				k++
			}

			if indexer.LastIndexedHeight+int64(blockParallelNum) > indexer.ChainHeight {
				blockParallelNum = int(indexer.ChainHeight - indexer.LastIndexedHeight)
				//logger.Printf("Parallel height is greater , setting blockParallelNum to %v : %v - %v", blockParallelNum, indexer.ChainHeight, indexer.LastIndexedHeight)

				if blockParallelNum <= 0 || indexer.LastIndexedHeight == indexer.ChainHeight {
					//logger.Printf("blockParallelNum (%v) is <= 0 or lastindex (%v) and chain height (%v) are equal", blockParallelNum, indexer.LastIndexedHeight, indexer.ChainHeight)
					time.Sleep(1 * time.Second)
					continue
				}
			}

			var regTxCount int64
			var burnTxCount int64
			var normTxCount int64
			var wg sync.WaitGroup
			wg.Add(blockParallelNum)

			var blsctxnsLock sync.RWMutex
			var blIndexTxns []*structures.BlockTxns

			for i := 1; i <= blockParallelNum; i++ {
				go func(i int) {
					if indexer.Closing {
						wg.Done()
						return
					}
					currBlHeight := indexer.LastIndexedHeight + int64(i)

					blid, err := indexer.RPC.getBlockHash(uint64(currBlHeight))
					if err != nil {
						logger.Printf("[StartDaemonMode-mainFOR-getBlockHash] %v - ERROR - getBlockHash(%v) - %v", currBlHeight, uint64(currBlHeight), err)
						wg.Done()
						return
					}

					blockTxns, err := indexer.indexBlock(blid, currBlHeight)
					if err != nil {
						logger.Printf("[StartDaemonMode-mainFOR-indexBlock] %v - ERROR - indexBlock(%v) - %v", currBlHeight, blid, err)
						wg.Done()
						return
					}

					if len(blockTxns.Tx_hashes) > 0 {
						blsctxnsLock.Lock()
						blIndexTxns = append(blIndexTxns, blockTxns)
						blsctxnsLock.Unlock()
						wg.Done()
					} else {
						wg.Done()
					}
				}(i)
			}
			wg.Wait()

			if indexer.Closing {
				break
			}

			// Arrange blIndexTxns by height so processed linearly
			sort.SliceStable(blIndexTxns, func(i, j int) bool {
				return blIndexTxns[i].Topoheight < blIndexTxns[j].Topoheight
			})

			// Run through blocks one at a time here to max cpu on a given block if large txns rather than split cpu across go routines of multiple blocks
			for _, v := range blIndexTxns {
				if len(v.Tx_hashes) > 0 {
					c_sctxs, cregTxCount, cburnTxCount, cnormTxCount, err := indexer.IndexTxn(v, false)
					if err != nil {
						logger.Printf("[StartDaemonMode-mainFOR-IndexTxn] %v - ERROR - IndexTxn(%v) - %v", v.Topoheight, v.Tx_hashes, err)
						return
					}

					regTxCount += cregTxCount
					burnTxCount += cburnTxCount
					normTxCount += cnormTxCount

					err = indexer.indexInvokes(c_sctxs, v)
					if err != nil {
						logger.Printf("[StartDaemonMode-mainFOR-indexInvokes]  ERROR - %v", err)
						break
					}
				}
			}
			if err != nil {
				logger.Printf("[StartDaemonMode-mainFOR-TxnIndexErrs] ERROR - %v", err)
				continue
			}

			if (regTxCount > 0 || burnTxCount > 0 || normTxCount > 0) && !(indexer.RunMode == "asset") {
				err = indexer.indexTxCounts(regTxCount, burnTxCount, normTxCount)
				if err != nil {
					logger.Printf("[StartDaemonMode-mainFOR-indexTxCounts] ERROR - %v", err)
					continue
				}
			}

			if indexer.LastIndexedHeight <= indexer.LastIndexedHeight+int64(blockParallelNum) {
				indexer.Lock()
				indexer.LastIndexedHeight += int64(blockParallelNum)
				indexer.Unlock()

				writeWait, _ := time.ParseDuration("20ms")
				switch indexer.DBType {
				case "gravdb":
					for indexer.GravDBBackend.Writing == 1 {
						if indexer.Closing {
							return
						}
						//logger.Printf("[Indexer-NewIndexer] GravitonDB is writing... sleeping for %v...", writeWait)
						time.Sleep(writeWait)
					}
					indexer.GravDBBackend.Writing = 1
					_, _, err := indexer.GravDBBackend.StoreLastIndexHeight(indexer.LastIndexedHeight, false)
					if err != nil {
						logger.Printf("[StartDaemonMode-mainFOR-StoreLastIndexHeight] ERROR - %v", err)
					}
					indexer.GravDBBackend.Writing = 0
				case "boltdb":
					for indexer.BBSBackend.Writing == 1 {
						if indexer.Closing {
							return
						}
						//logger.Printf("[Indexer-StartDaemonMode-StoreLastIndexHeight] BoltDB is writing... sleeping for %v... writer %v...", writeWait, indexer.BBSBackend.Writer)
						time.Sleep(writeWait)
					}
					indexer.BBSBackend.Writing = 1
					indexer.BBSBackend.Writer = "StartDaemonMode"
					indexer.BBSBackend.StoreLastIndexHeight(indexer.LastIndexedHeight)
					indexer.BBSBackend.Writing = 0
					indexer.BBSBackend.Writer = ""
				}
			}
		}
	}()
}

// Potential future item - may be removed as primary srevice of Gnomon is against daemon and not wallet due to security
func (indexer *Indexer) StartWalletMode(runType string) {
	var err error

	// Simple connect loop .. if connection fails initially then keep trying, else break out and continue on. Connect() is handled in getInfo() for retries later on if connection ceases again
	/*
		TODO:
		var astr []string
		astr = append(astr, "Basic dGVzdDp0ZXN0cGFzcw==")
		client.WS, _, err = websocket.DefaultDialer.Dial("ws://"+endpoint+"/ws", http.Header{"Authorization": astr})
	*/
	for {
		if indexer.Closing {
			// Break out on closing call
			break
		}
		logger.Printf("[StartDaemonMode] Trying to connect...")
		err = indexer.RPC.Connect(indexer.Endpoint)
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}
		break
	}
	time.Sleep(1 * time.Second)

	// Continuously getInfo from daemon to update topoheight globally
	switch runType {
	case "receive":
		// do receive actions here (e.g. from data source via API/WS)
		// TODO: is there anything we need to do within indexer itself if just receiving?
	default:
		// 'retrieve'/etc.
		go indexer.getWalletHeight()
		time.Sleep(1 * time.Second)

		go func() {
			for {
				if indexer.Closing {
					// Break out on closing call
					break
				}

				if indexer.LastIndexedHeight > indexer.ChainHeight {
					time.Sleep(1 * time.Second)
					continue
				}

				// Do indexing calls here

				// TODO: Modify this to be the height of the *next* tx index etc.
				indexer.Lock()
				indexer.LastIndexedHeight++
				indexer.Unlock()
			}
		}()
	}

	// Hold
	select {}
}

// Manually add/inject a SCID to be indexed. Checks validity and then stores within owner tree (no signer addr) and stores a set of current variables.
func (indexer *Indexer) AddSCIDToIndex(scidstoadd map[string]*structures.FastSyncImport) (err error) {
	var wg sync.WaitGroup
	wg.Add(len(scidstoadd))

	var scilock sync.RWMutex
	var scidstoindexstage []SCIDToIndexStage

	logger.Printf("[AddSCIDToIndex] Starting - Sorting %v SCIDs to index", len(scidstoadd))
	var tempdb *storage.GravitonStore
	var treenames []string
	switch indexer.DBType {
	case "gravdb":
		tempdb, err = storage.NewGravDBRAM("25ms")
		if err != nil {
			return fmt.Errorf("[AddSCIDToIndex] Error creating new gravdb: %v", err)
		}
		// We know owner is a tree that'll be written to, no need to loop through the scexists func every time when we *know* this one exists and isn't unique by scid etc.
		treenames = append(treenames, "owner")
	}

	for scid, fsi := range scidstoadd {
		go func(scid string, fsi *structures.FastSyncImport) {
			// Check if already validated
			if scidExist(indexer.ValidatedSCs, scid) || indexer.Closing {
				//logger.Printf("[AddSCIDToIndex] SCID '%v' already in validated list.", scid)
				wg.Done()

				return
			} else {
				// Validate SCID is *actually* a valid SCID
				scVars, scCode, _, _ := indexer.RPC.GetSCVariables(scid, indexer.ChainHeight, nil, nil, nil)

				var contains bool

				// If we can get the SC and searchfilter is "" (get all), contains is true. Otherwise evaluate code against searchfilter
				if len(indexer.SearchFilter) == 0 {
					contains = true
				} else {
					// Ensure scCode is not blank (e.g. an invalid scid)
					if scCode != "" {
						for _, sfv := range indexer.SearchFilter {
							contains = strings.Contains(scCode, sfv)
							if contains {
								// Break b/c we want to ensure contains remains true. Only care if it matches at least 1 case
								break
							}
						}
					}
				}

				scilock.Lock()
				scidstoindexstage = append(scidstoindexstage, SCIDToIndexStage{scid: scid, fsi: fsi, scVars: scVars, scCode: scCode, contains: contains})
				scilock.Unlock()
			}
			wg.Done()
		}(scid, fsi)
	}
	wg.Wait()

	for _, v := range scidstoindexstage {
		if v.contains {
			// By returning valid variables of a given Scid (GetSC --> parse vars), we can conclude it is a valid SCID. Otherwise, skip adding to validated scids
			if len(v.scVars) > 0 {
				indexer.Lock()
				indexer.ValidatedSCs = append(indexer.ValidatedSCs, v.scid)
				indexer.Unlock()
				if v.fsi != nil {
					//logger.Printf("[AddSCIDToIndex] SCID matches search filter. Adding SCID %v / Signer %v", scid, fsi.Owner)
				} else {
					//logger.Printf("[AddSCIDToIndex] SCID matches search filter. Adding SCID %v", scid)
				}

				writeWait, _ := time.ParseDuration("20ms")
				switch indexer.DBType {
				case "gravdb":
					for tempdb.Writing == 1 {
						if indexer.Closing {
							return
						}
						//logger.Printf("[Indexer-NewIndexer] GravitonDB is writing... sleeping for %v...", writeWait)
						time.Sleep(writeWait)
					}

					if indexer.Closing {
						return
					}
					tempdb.Writing = 1
					var ctrees []*graviton.Tree

					var sochanges bool
					var sotree *graviton.Tree
					if v.fsi != nil {
						sotree, sochanges, err = tempdb.StoreOwner(v.scid, v.fsi.Owner, true)
					} else {
						sotree, sochanges, err = tempdb.StoreOwner(v.scid, "", true)
					}
					if err != nil {
						logger.Printf("[AddSCIDToIndex] ERR - storing owner: %v", err)
					} else {
						if sochanges {
							ctrees = append(ctrees, sotree)
						}
					}
					svdtree, svdchanges, err := tempdb.StoreSCIDVariableDetails(v.scid, v.scVars, indexer.ChainHeight, true)
					if err != nil {
						logger.Printf("[AddSCIDToIndex] ERR - storing scid variable details: %v", err)
					} else {
						if svdchanges {
							ctrees = append(ctrees, svdtree)
						}
					}
					if !scidExist(treenames, v.scid+"vars") {
						treenames = append(treenames, v.scid+"vars")
					}
					sihtree, sihchanges, err := tempdb.StoreSCIDInteractionHeight(v.scid, indexer.ChainHeight, true)
					if err != nil {
						logger.Printf("[AddSCIDToIndex] ERR - storing scid interaction height: %v", err)
					} else {
						if sihchanges {
							ctrees = append(ctrees, sihtree)
						}
					}
					if !scidExist(treenames, v.scid+"heights") {
						treenames = append(treenames, v.scid+"heights")
					}
					if len(ctrees) > 0 {
						_, err := tempdb.CommitTrees(ctrees)
						if err != nil {
							logger.Printf("[AddSCIDToIndex] ERR - committing trees: %v", err)
						} else {
							//logger.Printf("[AddSCIDToIndex] DEBUG - cv [%v]", cv)
						}
					}
					tempdb.Writing = 0
				case "boltdb":
					for indexer.BBSBackend.Writing == 1 {
						if indexer.Closing {
							return
						}
						logger.Printf("[Indexer-AddSCIDToIndex] BoltDB is writing... sleeping for %v... writer %v...", writeWait, indexer.BBSBackend.Writer)
						time.Sleep(writeWait)
					}
					indexer.BBSBackend.Writing = 1
					indexer.BBSBackend.Writer = "AddSCIDToIndex"
					if v.fsi != nil {
						_, err = indexer.BBSBackend.StoreOwner(v.scid, v.fsi.Owner)
					} else {
						_, err = indexer.BBSBackend.StoreOwner(v.scid, "")
					}
					if err != nil {
						logger.Printf("[AddSCIDToIndex] ERR - storing owner: %v", err)
					}
					_, err = indexer.BBSBackend.StoreSCIDVariableDetails(v.scid, v.scVars, indexer.ChainHeight)
					if err != nil {
						logger.Printf("[AddSCIDToIndex] ERR - storing scid variable details: %v", err)
					}
					if !scidExist(treenames, v.scid+"vars") {
						treenames = append(treenames, v.scid+"vars")
					}
					_, err = indexer.BBSBackend.StoreSCIDInteractionHeight(v.scid, indexer.ChainHeight)
					if err != nil {
						logger.Printf("[AddSCIDToIndex] ERR - storing scid interaction height: %v", err)
					}
					indexer.BBSBackend.Writing = 0
					indexer.BBSBackend.Writer = ""
				}
			} else {
				logger.Printf("[AddSCIDToIndex] ERR - SCID '%v' doesn't exist at height %v", v.scid, indexer.ChainHeight)
			}
		}
	}

	logger.Printf("[AddSCIDToIndex] Done - Sorting %v SCIDs to index", len(scidstoadd))
	switch indexer.DBType {
	case "gravdb":
		// TODO: Sometimes the RAM store does not properly take in all values and are missing some index SCs. To investigate...
		logger.Printf("[AddSCIDToIndex] Current stored disk: %v", len(indexer.GravDBBackend.GetAllOwnersAndSCIDs()))
		logger.Printf("[AddSCIDToIndex] Current stored ram: %v", len(tempdb.GetAllOwnersAndSCIDs()))

		logger.Printf("[AddSCIDToIndex] Starting - Committing RAM SCID sort to disk storage...")
		writeWait, _ := time.ParseDuration("10ms")
		for tempdb.Writing == 1 || indexer.GravDBBackend.Writing == 1 {
			if indexer.Closing {
				return
			}
			//logger.Printf("[AddSCIDToIndex-StoreAltDBInput] GravitonDB is writing... sleeping for %v...", writeWait)
			time.Sleep(writeWait)
		}
		tempdb.Writing = 1
		indexer.GravDBBackend.Writing = 1
		indexer.GravDBBackend.StoreAltDBInput(treenames, tempdb)
		tempdb.Writing = 0
		indexer.GravDBBackend.Writing = 0
		logger.Printf("[AddSCIDToIndex] Done - Committing RAM SCID sort to disk storage...")
		logger.Printf("[AddSCIDToIndex] New stored disk: %v", len(indexer.GravDBBackend.GetAllOwnersAndSCIDs()))
	case "boltdb":
		logger.Printf("[AddSCIDToIndex] New stored disk: %v", len(indexer.BBSBackend.GetAllOwnersAndSCIDs()))
	}

	return err
}

func (client *Client) Connect(endpoint string) (err error) {
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

	client.WS, _, err = websocket.DefaultDialer.Dial("ws://"+endpoint+"/ws", nil)

	// notify user of any state change
	// if daemon connection breaks or comes live again
	if err == nil {
		if !Connected {
			logger.Printf("[Connect] Connection to RPC server successful - ws://%s/ws", endpoint)
			Connected = true
		}
	} else {
		logger.Printf("[Connect] ERROR connecting to endpoint %v", err)

		if Connected {
			logger.Printf("[Connect] ERROR - Connection to RPC server Failed - ws://%s/ws", endpoint)
		}
		Connected = false
		return err
	}

	input_output := rwc.New(client.WS)
	client.RPC = jrpc2.NewClient(channel.RawJSON(input_output, input_output), nil)

	return err
}

func (indexer *Indexer) indexBlock(blid string, topoheight int64) (blockTxns *structures.BlockTxns, err error) {
	blockTxns = &structures.BlockTxns{}

	var io rpc.GetBlock_Result
	var ip = rpc.GetBlock_Params{Hash: blid}

	if indexer.Closing {
		return
	}

	// TODO: Make this a consumable func with rpc calls and timeout / wait / retry logic for deduplication of code. Or use alternate method of checking [primary use case is remote nodes]
	var reconnect_count int
	for {
		if err = indexer.RPC.RPC.CallResult(context.Background(), "DERO.GetBlock", ip, &io); err != nil {
			//logger.Printf("[indexBlock] ERROR - GetBlock failed: %v . Trying again (%v / 5) ", err, reconnect_count)
			if reconnect_count >= 5 {
				return blockTxns, fmt.Errorf("[indexBlock] ERROR - GetBlock failed: %v", err)
			}
			time.Sleep(1 * time.Second)

			reconnect_count++

			continue
		}

		break
	}

	var bl block.Block
	var block_bin []byte

	block_bin, _ = hex.DecodeString(io.Blob)
	bl.Deserialize(block_bin)

	if indexer.MBLLookup {
		mbldetails, err2 := mbllookup.GetMBLByBLHash(bl)
		if err2 != nil {
			logger.Printf("[indexBlock] Error getting miniblock details for blid %v", bl.GetHash().String())
			return blockTxns, err2
		}

		writeWait, _ := time.ParseDuration("20ms")
		switch indexer.DBType {
		case "gravdb":
			if !(indexer.RunMode == "asset") {
				for indexer.GravDBBackend.Writing == 1 {
					if indexer.Closing {
						return
					}
					//logger.Printf("[Indexer-NewIndexer] GravitonDB is writing... sleeping for %v...", writeWait)
					time.Sleep(writeWait)
				}

				indexer.GravDBBackend.Writing = 1
				_, _, err2 = indexer.GravDBBackend.StoreMiniblockDetailsByHash(blid, mbldetails, false)
				if err2 != nil {
					logger.Printf("[indexBlock] Error storing miniblock details for blid %v", err2)
					indexer.GravDBBackend.Writing = 0
					return blockTxns, err2
				}
				indexer.GravDBBackend.Writing = 0
			}
		case "boltdb":
			if !(indexer.RunMode == "asset") {
				for indexer.BBSBackend.Writing == 1 {
					if indexer.Closing {
						return
					}
					//logger.Printf("[Indexer-IndexBlock-StoreMiniblockDetailsByHash] BoltDB is writing... sleeping for %v... writer %v...", writeWait, indexer.BBSBackend.Writer)
					time.Sleep(writeWait)
				}

				indexer.BBSBackend.Writing = 1
				indexer.BBSBackend.Writer = "IndexBlock"
				_, err2 = indexer.BBSBackend.StoreMiniblockDetailsByHash(blid, mbldetails)
				if err2 != nil {
					logger.Printf("[indexBlock] Error storing miniblock details for blid %v", err2)
					indexer.BBSBackend.Writing = 0
					indexer.BBSBackend.Writer = ""
					return blockTxns, err2
				}
				indexer.BBSBackend.Writing = 0
				indexer.BBSBackend.Writer = ""
			}
		}
	}

	blockTxns.Topoheight = int64(bl.Height)
	blockTxns.Tx_hashes = bl.Tx_hashes

	return
}

func (indexer *Indexer) IndexTxn(blTxns *structures.BlockTxns, noStore bool) (bl_sctxs []structures.SCTXParse, regTxCount int64, burnTxCount int64, normTxCount int64, err error) {
	var txslock sync.RWMutex

	var wg sync.WaitGroup
	wg.Add(len(blTxns.Tx_hashes))

	for i := 0; i < len(blTxns.Tx_hashes); i++ {
		go func(i int) {
			if indexer.Closing {
				wg.Done()
				return
			}

			// We can match the PoW scheme result to filter out reg txns without needing to waste GetTransaction calls against saving time - https://github.com/deroproject/derohe/blob/main/cmd/dero-wallet-cli/easymenu_post_open.go#L150
			if blTxns.Tx_hashes[i][0] == 0 && blTxns.Tx_hashes[i][1] == 0 && blTxns.Tx_hashes[i][2] == 0 {
				txslock.Lock()
				regTxCount++
				txslock.Unlock()
				wg.Done()
				return
			}

			var tx transaction.Transaction
			var sc_args rpc.Arguments
			var sc_fees uint64
			var sender string

			var inputparam rpc.GetTransaction_Params
			var output rpc.GetTransaction_Result

			inputparam.Tx_Hashes = append(inputparam.Tx_Hashes, blTxns.Tx_hashes[i].String())

			// TODO: Make this a consumable func with rpc calls and timeout / wait / retry logic for deduplication of code. Or use alternate method of checking [primary use case is remote nodes]
			var reconnect_count int
			for {
				if err = indexer.RPC.RPC.CallResult(context.Background(), "DERO.GetTransaction", inputparam, &output); err != nil {
					//logger.Printf("[IndexTxn] ERROR - GetTransaction for txid '%v' failed: %v . Trying again (%v / 5)", inputparam.Tx_Hashes, err, reconnect_count)
					if reconnect_count >= 5 {
						// TODO - In event indexer.Endpoint is being swapped, this case will fail and you could miss a txn. Need another handle rather than just "assume" skip/move on.
						wg.Done()
						// If we error, this could be due to regtxn not valid on pruned node or other reasons. We will just nil the err and then return and move on.
						err = nil
						logger.Printf("[IndexTxn] ERROR - GetTransaction for txid '%v' failed: %v . (%v / 5 times)", inputparam.Tx_Hashes, err, reconnect_count)
						return
					}
					time.Sleep(1 * time.Second)

					reconnect_count++

					continue
				}

				break
			}

			tx_bin, _ := hex.DecodeString(output.Txs_as_hex[0])
			tx.Deserialize(tx_bin)

			// TODO: Add count for registration TXs and store the following on normal txs: IF SCID IS PRESENT, store tx details + ring members + fees + etc. Use later for scid balance queries
			if tx.TransactionType == transaction.SC_TX {
				sc_args = tx.SCDATA
				sc_fees = tx.Fees()
				var method string
				var scid string
				var scid_hex []byte

				entrypoint := fmt.Sprintf("%v", sc_args.Value("entrypoint", "S"))

				sc_action := fmt.Sprintf("%v", sc_args.Value("SC_ACTION", "U"))

				// Other ways to parse this, but will do for now --> see https://github.com/deroproject/derohe/blob/main/blockchain/blockchain.go#L688
				if sc_action == "1" {
					method = "installsc"
					scid = string(blTxns.Tx_hashes[i].String())
					scid_hex = []byte(scid)
				} else {
					method = "scinvoke"
					// Get "SC_ID" which is of type H to byte.. then to string
					scid_hex = []byte(fmt.Sprintf("%v", sc_args.Value("SC_ID", "H")))
					scid = string(scid_hex)
				}

				// TODO: What if there are multiple payloads with potentially different ringsizes, can that happen?
				if tx.Payloads[0].Statement.RingSize == 2 {
					sender = output.Txs[0].Signer
				} else {
					//logger.Printf("[indexBlock] ERR - Ringsize for %v is != 2. Storing blank value for txid sender.", bl.Tx_hashes[i])
					//continue
					/*
						if method == "installsc" {
							// We do not store a ringsize > 2 of installsc calls. Only of SC interactions via sc_invoke for ringsize > 2 and just blank out the sender
							//continue
							//wg.Done()
						}
					*/
				}
				//time.Sleep(2 * time.Second)
				txslock.Lock()
				bl_sctxs = append(bl_sctxs, structures.SCTXParse{Txid: blTxns.Tx_hashes[i].String(), Scid: scid, Scid_hex: scid_hex, Entrypoint: entrypoint, Method: method, Sc_args: sc_args, Sender: sender, Payloads: tx.Payloads, Fees: sc_fees, Height: blTxns.Topoheight})
				txslock.Unlock()
			} else if tx.TransactionType == transaction.REGISTRATION {
				txslock.Lock()
				regTxCount++
				txslock.Unlock()
			} else if tx.TransactionType == transaction.BURN_TX {
				// TODO: Handle burn_tx here
				txslock.Lock()
				burnTxCount++
				txslock.Unlock()
			} else if tx.TransactionType == transaction.NORMAL {
				// TODO: Handle normal tx here
				txslock.Lock()
				normTxCount++
				txslock.Unlock()

				for j := 0; j < len(tx.Payloads); j++ {
					var zhash crypto.Hash
					if tx.Payloads[j].SCID != zhash {
						//logger.Printf("[indexBlock] TXID '%v' has SCID in payload of '%v' and ring members: %v.", bl.Tx_hashes[i], tx.Payloads[j].SCID, output.Txs[j].Ring[j])
						for _, v := range output.Txs[0].Ring[j] {
							//bl_normtxs = append(bl_normtxs, structures.NormalTXWithSCIDParse{Txid: bl.Tx_hashes[i].String(), Scid: tx.Payloads[j].SCID.String(), Fees: tx_fees, Height: int64(bl.Height)})
							if !noStore {
								writeWait, _ := time.ParseDuration("20ms")
								switch indexer.DBType {
								case "gravdb":
									if !(indexer.RunMode == "asset") {
										for indexer.GravDBBackend.Writing == 1 {
											if indexer.Closing {
												return
											}
											//logger.Printf("[Indexer-NewIndexer] GravitonDB is writing... sleeping for %v...", writeWait)
											time.Sleep(writeWait)
										}
										indexer.GravDBBackend.Writing = 1
										indexer.GravDBBackend.StoreNormalTxWithSCIDByAddr(v, &structures.NormalTXWithSCIDParse{Txid: blTxns.Tx_hashes[i].String(), Scid: tx.Payloads[j].SCID.String(), Fees: sc_fees, Height: int64(blTxns.Topoheight)}, false)
										indexer.GravDBBackend.Writing = 0
									}
								case "boltdb":
									if !(indexer.RunMode == "asset") {
										for indexer.BBSBackend.Writing == 1 {
											if indexer.Closing {
												return
											}
											//logger.Printf("[Indexer-IndexTxn-StoreNormalTxWithSCIDByAddr] BoltDB is writing... sleeping for %v... writer %v...", writeWait, indexer.BBSBackend.Writer)
											time.Sleep(writeWait)
										}
										indexer.BBSBackend.Writing = 1
										indexer.BBSBackend.Writer = "IndexTxn"
										indexer.BBSBackend.StoreNormalTxWithSCIDByAddr(v, &structures.NormalTXWithSCIDParse{Txid: blTxns.Tx_hashes[i].String(), Scid: tx.Payloads[j].SCID.String(), Fees: sc_fees, Height: int64(blTxns.Topoheight)})
										indexer.BBSBackend.Writing = 0
										indexer.BBSBackend.Writer = ""
									}
								}
							}
						}
					}
				}
			} else {
				//logger.Printf("TX %v type is NOT handled.", bl.Tx_hashes[i])
			}
			wg.Done()
		}(i)
	}
	wg.Wait()

	return bl_sctxs, regTxCount, burnTxCount, normTxCount, err
}

func (indexer *Indexer) indexTxCounts(regTxCount int64, burnTxCount int64, normTxCount int64) (err error) {
	if indexer.Closing {
		return
	}
	var ctrees []*graviton.Tree

	writeWait, _ := time.ParseDuration("20ms")
	switch indexer.DBType {
	case "gravdb":
		for indexer.GravDBBackend.Writing == 1 {
			if indexer.Closing {
				return
			}
			//logger.Printf("[Indexer-NewIndexer] GravitonDB is writing... sleeping for %v...", writeWait)
			time.Sleep(writeWait)
		}
		indexer.GravDBBackend.Writing = 1
		if regTxCount > 0 && !indexer.Fastsync {
			// Load from mem existing regTxCount and append new value
			currRegTxCount := indexer.GravDBBackend.GetTxCount("registration")
			/*
				writeWait, _ := time.ParseDuration("50ms")
				for indexer.GravDBBackend.Writing == 1 {
					if indexer.Closing {
						return
					}
					//logger.Printf("[Indexer-indexBlock-regTxCount] GravitonDB is writing... sleeping for %v...", writeWait)
					time.Sleep(writeWait)
				}
				indexer.GravDBBackend.Writing = 1
			*/
			rtxtree, rtxchanges, err := indexer.GravDBBackend.StoreTxCount(regTxCount+currRegTxCount, "registration", true)
			//indexer.GravDBBackend.Writing = 0
			if err != nil {
				logger.Printf("[indexBlock] ERROR - Error storing registration tx count. DB '%v' - this block count '%v' - total '%v'", currRegTxCount, regTxCount, regTxCount+currRegTxCount)
				indexer.GravDBBackend.Writing = 0
				return err
			} else {
				if rtxchanges {
					ctrees = append(ctrees, rtxtree)
				}
			}
		}

		if burnTxCount > 0 && !indexer.Fastsync {
			// Load from mem existing burnTxCount and append new value
			currBurnTxCount := indexer.GravDBBackend.GetTxCount("burn")
			/*
				writeWait, _ := time.ParseDuration("50ms")
				for indexer.GravDBBackend.Writing == 1 {
					if indexer.Closing {
						return
					}
					//logger.Printf("[Indexer-indexBlock-burnTxCount] GravitonDB is writing... sleeping for %v...", writeWait)
					time.Sleep(writeWait)
				}
				indexer.GravDBBackend.Writing = 1
			*/
			btxtree, btxchanges, err := indexer.GravDBBackend.StoreTxCount(burnTxCount+currBurnTxCount, "burn", true)
			if err != nil {
				logger.Printf("[indexBlock] ERROR - Error storing burn tx count. DB '%v' - this block count '%v' - total '%v'", currBurnTxCount, burnTxCount, regTxCount+currBurnTxCount)
				indexer.GravDBBackend.Writing = 0
				return err
			} else {
				if btxchanges {
					ctrees = append(ctrees, btxtree)
				}
			}
			//indexer.GravDBBackend.Writing = 0
		}

		if normTxCount > 0 && !indexer.Fastsync {
			/*
				// Test code for finding highest tps block
				var io rpc.GetBlockHeaderByHeight_Result
				var ip = rpc.GetBlockHeaderByTopoHeight_Params{TopoHeight: bl.Height - 1}

				if err = client.RPC.CallResult(context.Background(), "DERO.GetBlockHeaderByTopoHeight", ip, &io); err != nil {
					logger.Printf("[getBlockHash] GetBlockHeaderByTopoHeight failed: %v", err)
					return err
				} else {
					//logger.Printf("[getBlockHash] Retrieved block header from topoheight %v", height)
					//mainnet = !info.Testnet // inverse of testnet is mainnet
					//logger.Printf("%v", io)
				}

				blid := io.Block_Header.Hash

				var io2 rpc.GetBlock_Result
				var ip2 = rpc.GetBlock_Params{Hash: blid}

				if err = client.RPC.CallResult(context.Background(), "DERO.GetBlock", ip2, &io2); err != nil {
					logger.Printf("[indexBlock] ERROR - GetBlock failed: %v", err)
					return err
				}

				var bl2 block.Block
				var block_bin2 []byte

				block_bin2, _ = hex.DecodeString(io2.Blob)
				bl2.Deserialize(block_bin2)

				prevtimestamp := bl2.Timestamp

				// Load from mem existing normTxCount and append new value
				currNormTxCount := Graviton_backend.GetTxCount("normal")

				//logger.Printf("%v / (%v - %v)", normTxCount, int64(bl.Timestamp), int64(prevtimestamp))
				tps := normTxCount / ((int64(bl.Timestamp) - int64(prevtimestamp)) / 1000)

				//err := Graviton_backend.StoreTxCount(normTxCount+currNormTxCount, "normal")
				if tps > currNormTxCount {
					err := Graviton_backend.StoreTxCount(tps, "normal")
					if err != nil {
						logger.Printf("ERROR - Error storing normal tx count. DB '%v' - this block count '%v' - total '%v'", currNormTxCount, tps, regTxCount+currNormTxCount)
					}

					err = Graviton_backend.StoreTxCount(blheight, "registration")
					if err != nil {
						logger.Printf("ERROR - Error storing registration tx count. DB '%v' - this block count '%v' - total '%v'", currNormTxCount, normTxCount, regTxCount+currNormTxCount)
					}

					err = Graviton_backend.StoreTxCount((int64(bl.Timestamp) - int64(prevtimestamp)), "burn")
					if err != nil {
						logger.Printf("ERROR - Error storing registration tx count. DB '%v' - this block count '%v' - total '%v'", currNormTxCount, normTxCount, regTxCount+currNormTxCount)
					}
				}
			*/

			// Load from mem existing normTxCount and append new value
			currNormTxCount := indexer.GravDBBackend.GetTxCount("normal")
			/*
				writeWait, _ := time.ParseDuration("50ms")
				for indexer.GravDBBackend.Writing == 1 {
					if indexer.Closing {
						return
					}
					//logger.Printf("[Indexer-indexBlock-normTxCount] GravitonDB is writing... sleeping for %v...", writeWait)
					time.Sleep(writeWait)
				}
				indexer.GravDBBackend.Writing = 1
			*/
			ntxtree, ntxchanges, err := indexer.GravDBBackend.StoreTxCount(normTxCount+currNormTxCount, "normal", true)
			if err != nil {
				logger.Printf("[indexBlock] ERROR - Error storing normal tx count. DB '%v' - this block count '%v' - total '%v'", currNormTxCount, currNormTxCount, normTxCount+currNormTxCount)
				indexer.GravDBBackend.Writing = 0
				return err
			} else {
				if ntxchanges {
					ctrees = append(ctrees, ntxtree)
				}
			}
		}
		if len(ctrees) > 0 {
			_, err := indexer.GravDBBackend.CommitTrees(ctrees)
			if err != nil {
				logger.Printf("[indexBlock-indexTxCounts] ERR - committing trees: %v", err)
			} else {
				//logger.Printf("[indexBlock-installsc] DEBUG - cv [%v]", cv)
			}
		}
		indexer.GravDBBackend.Writing = 0
	case "boltdb":
		for indexer.BBSBackend.Writing == 1 {
			if indexer.Closing {
				return
			}
			//logger.Printf("[Indexer-IndexTxCounts] BoltDB is writing... sleeping for %v... writer %v...", writeWait, indexer.BBSBackend.Writer)
			time.Sleep(writeWait)
		}
		indexer.BBSBackend.Writing = 1
		indexer.BBSBackend.Writer = "IndexTxCounts"
		if regTxCount > 0 && !indexer.Fastsync {
			// Load from mem existing regTxCount and append new value
			currRegTxCount := indexer.BBSBackend.GetTxCount("registration")
			_, err := indexer.BBSBackend.StoreTxCount(regTxCount+currRegTxCount, "registration")
			if err != nil {
				logger.Printf("[indexBlock] ERROR - Error storing registration tx count. DB '%v' - this block count '%v' - total '%v'", currRegTxCount, regTxCount, regTxCount+currRegTxCount)
				indexer.BBSBackend.Writing = 0
				indexer.BBSBackend.Writer = ""
				return err
			}
		}

		if burnTxCount > 0 && !indexer.Fastsync {
			// Load from mem existing burnTxCount and append new value
			currBurnTxCount := indexer.BBSBackend.GetTxCount("burn")
			_, err := indexer.BBSBackend.StoreTxCount(burnTxCount+currBurnTxCount, "burn")
			if err != nil {
				logger.Printf("[indexBlock] ERROR - Error storing burn tx count. DB '%v' - this block count '%v' - total '%v'", currBurnTxCount, burnTxCount, regTxCount+currBurnTxCount)
				indexer.BBSBackend.Writing = 0
				indexer.BBSBackend.Writer = ""
				return err
			}
		}

		if normTxCount > 0 && !indexer.Fastsync {
			// Load from mem existing normTxCount and append new value
			currNormTxCount := indexer.BBSBackend.GetTxCount("normal")
			_, err := indexer.BBSBackend.StoreTxCount(normTxCount+currNormTxCount, "normal")
			if err != nil {
				logger.Printf("[indexBlock] ERROR - Error storing normal tx count. DB '%v' - this block count '%v' - total '%v'", currNormTxCount, currNormTxCount, normTxCount+currNormTxCount)
				indexer.BBSBackend.Writing = 0
				indexer.BBSBackend.Writer = ""
				return err
			}
		}
		indexer.BBSBackend.Writing = 0
		indexer.BBSBackend.Writer = ""
	}

	return nil
}

func (indexer *Indexer) indexInvokes(bl_sctxs []structures.SCTXParse, bl_txns *structures.BlockTxns) (err error) {

	if indexer.Closing {
		return
	}

	if len(bl_sctxs) > 0 {
		//logger.Printf("Block %v has %v SC tx(s).", bl.GetHash(), len(bl_sctxs))

		// TODO: Go routine possible for pre-storage components given the number of 'potential' getscvar calls that may be required.. could speed up indexing some more.
		for i := 0; i < len(bl_sctxs); i++ {
			if bl_sctxs[i].Method == "installsc" {
				var contains bool

				code := fmt.Sprintf("%v", bl_sctxs[i].Sc_args.Value("SC_CODE", "S"))

				// Temporary check - will need something more robust to code compare potentially all except InitializePrivate() with a given template file or other filter inputs.
				//contains := strings.Contains(code, "200 STORE(\"somevar\", 1)")
				if len(indexer.SearchFilter) == 0 {
					contains = true
				} else {
					for _, sfv := range indexer.SearchFilter {
						contains = strings.Contains(code, sfv)
						if contains {
							// Break b/c we want to ensure contains remains true. Only care if it matches at least 1 case
							break
						}
					}
				}

				if !contains {
					// Then reject the validation that this is an installsc action and move on
					logger.Printf("[indexBlock-installsc] SCID %v does not contain the search filter string, moving on.", bl_sctxs[i].Scid)
				} else {
					// Gets the SC variables (key/value) at a given topoheight and then stores them
					scVars, _, _, _ := indexer.RPC.GetSCVariables(bl_sctxs[i].Scid, bl_txns.Topoheight, nil, nil, nil)

					if len(scVars) > 0 {
						// Append into db for validated SC
						logger.Printf("[indexBlock-installsc] SCID matches search filter. Adding SCID %v / Signer %v", bl_sctxs[i].Scid, bl_sctxs[i].Sender)
						indexer.Lock()
						indexer.ValidatedSCs = append(indexer.ValidatedSCs, bl_sctxs[i].Scid)
						indexer.Unlock()

						writeWait, _ := time.ParseDuration("20ms")
						switch indexer.DBType {
						case "gravdb":
							for indexer.GravDBBackend.Writing == 1 {
								if indexer.Closing {
									return
								}
								//logger.Printf("[Indexer-NewIndexer] GravitonDB is writing... sleeping for %v...", writeWait)
								time.Sleep(writeWait)
							}
							indexer.GravDBBackend.Writing = 1
							var ctrees []*graviton.Tree
							sotree, sochanges, err := indexer.GravDBBackend.StoreOwner(bl_sctxs[i].Scid, bl_sctxs[i].Sender, true)
							if err != nil {
								logger.Printf("[indexBlock-installsc] Error storing owner: %v", err)
							} else {
								if sochanges {
									ctrees = append(ctrees, sotree)
								}
							}

							sidtree, sidchanges, err := indexer.GravDBBackend.StoreInvokeDetails(bl_sctxs[i].Scid, bl_sctxs[i].Sender, bl_sctxs[i].Entrypoint, bl_txns.Topoheight, &bl_sctxs[i], true)
							if err != nil {
								logger.Printf("[indexBlock-installsc] Err storing invoke details. Err: %v", err)
								time.Sleep(5 * time.Second)
								return err
							} else {
								if sidchanges {
									ctrees = append(ctrees, sidtree)
								}
							}

							svdtree, svdchanges, err := indexer.GravDBBackend.StoreSCIDVariableDetails(bl_sctxs[i].Scid, scVars, bl_txns.Topoheight, true)
							if err != nil {
								logger.Printf("[indexBlock-installsc] ERR - storing scid variable details: %v", err)
							} else {
								if svdchanges {
									ctrees = append(ctrees, svdtree)
								}
							}
							sihtree, sihchanges, err := indexer.GravDBBackend.StoreSCIDInteractionHeight(bl_sctxs[i].Scid, bl_txns.Topoheight, true)
							if err != nil {
								logger.Printf("[indexBlock-installsc] ERR - storing scid interaction height: %v", err)
							} else {
								if sihchanges {
									ctrees = append(ctrees, sihtree)
								}
							}
							if len(ctrees) > 0 {
								_, err := indexer.GravDBBackend.CommitTrees(ctrees)
								if err != nil {
									logger.Printf("[indexBlock-installsc] ERR - committing trees: %v", err)
								} else {
									//logger.Printf("[indexBlock-installsc] DEBUG - cv [%v]", cv)
								}
							}
							indexer.GravDBBackend.Writing = 0
						case "boltdb":
							for indexer.BBSBackend.Writing == 1 {
								if indexer.Closing {
									return
								}
								//logger.Printf("[Indexer-IndexInvokes] BoltDB is writing... sleeping for %v... writer %v...", writeWait, indexer.BBSBackend.Writer)
								time.Sleep(writeWait)
							}
							indexer.BBSBackend.Writing = 1
							indexer.BBSBackend.Writer = "IndexInvokes"

							_, err := indexer.BBSBackend.StoreOwner(bl_sctxs[i].Scid, bl_sctxs[i].Sender)
							if err != nil {
								logger.Printf("[indexBlock-installsc] Error storing owner: %v", err)
							}

							_, err = indexer.BBSBackend.StoreInvokeDetails(bl_sctxs[i].Scid, bl_sctxs[i].Sender, bl_sctxs[i].Entrypoint, bl_txns.Topoheight, &bl_sctxs[i])
							if err != nil {
								logger.Printf("[indexBlock-installsc] Err storing invoke details. Err: %v", err)
								time.Sleep(5 * time.Second)
								return err
							}

							_, err = indexer.BBSBackend.StoreSCIDVariableDetails(bl_sctxs[i].Scid, scVars, bl_txns.Topoheight)
							if err != nil {
								logger.Printf("[indexBlock-installsc] ERR - storing scid variable details: %v", err)
							}
							_, err = indexer.BBSBackend.StoreSCIDInteractionHeight(bl_sctxs[i].Scid, bl_txns.Topoheight)
							if err != nil {
								logger.Printf("[indexBlock-installsc] ERR - storing scid interaction height: %v", err)
							}
							indexer.BBSBackend.Writing = 0
							indexer.BBSBackend.Writer = ""
						}

						//logger.Printf("SCID: %v ; Sender: %v ; Entrypoint: %v ; topoheight : %v ; info: %v", bl_sctxs[i].Scid, bl_sctxs[i].Sender, bl_sctxs[i].Entrypoint, topoheight, &bl_sctxs[i])
						logger.Debugf("Sender: %v ; topoheight : %v ; args: %v ; burnValue: %v", bl_sctxs[i].Sender, bl_txns.Topoheight, bl_sctxs[i].Sc_args, bl_sctxs[i].Payloads[0].BurnValue)
					} else {
						logger.Printf("[indexBlock-installsc] SCID '%v' appears to be invalid.", bl_sctxs[i].Scid)
						writeWait, _ := time.ParseDuration("20ms")
						switch indexer.DBType {
						case "gravdb":
							if !(indexer.RunMode == "asset") {
								for indexer.GravDBBackend.Writing == 1 {
									if indexer.Closing {
										return
									}
									//logger.Printf("[Indexer-NewIndexer] GravitonDB is writing... sleeping for %v...", writeWait)
									time.Sleep(writeWait)
								}
								indexer.GravDBBackend.Writing = 1
								indexer.GravDBBackend.StoreInvalidSCIDDeploys(bl_sctxs[i].Scid, bl_sctxs[i].Fees, false)
								indexer.GravDBBackend.Writing = 0
							}
						case "boltdb":
							if !(indexer.RunMode == "asset") {
								for indexer.BBSBackend.Writing == 1 {
									if indexer.Closing {
										return
									}
									//logger.Printf("[Indexer-IndexInvokes-StoreInvalidSCIDDeploys] BoltDB is writing... sleeping for %v... writer %v...", writeWait, indexer.BBSBackend.Writer)
									time.Sleep(writeWait)
								}
								indexer.BBSBackend.Writing = 1
								indexer.BBSBackend.Writer = "IndexInvokes"
								indexer.BBSBackend.StoreInvalidSCIDDeploys(bl_sctxs[i].Scid, bl_sctxs[i].Fees)
								indexer.BBSBackend.Writing = 0
								indexer.BBSBackend.Writer = ""
							}
						}
					}
				}
			} else {
				if !scidExist(indexer.ValidatedSCs, bl_sctxs[i].Scid) {

					// Validate SCID is *actually* a valid SCID
					// This assumes we can return all variables.
					// TODO: For daemon v139 should we append []string "C" to lookup key C to confirm if >1024 k/v pairs exist?
					valVars, _, _, _ := indexer.RPC.GetSCVariables(bl_sctxs[i].Scid, bl_txns.Topoheight, nil, nil, nil)

					// By returning valid variables of a given Scid (GetSC --> parse vars), we can conclude it is a valid SCID. Otherwise, skip adding to validated scids
					if len(valVars) > 0 {
						logger.Printf("[indexBlock] SCID matches search filter. Adding SCID %v / Signer %v", bl_sctxs[i].Scid, "")
						indexer.Lock()
						indexer.ValidatedSCs = append(indexer.ValidatedSCs, bl_sctxs[i].Scid)
						indexer.Unlock()

						writeWait, _ := time.ParseDuration("20ms")
						switch indexer.DBType {
						case "gravdb":
							for indexer.GravDBBackend.Writing == 1 {
								if indexer.Closing {
									return
								}
								//logger.Printf("[Indexer-NewIndexer] GravitonDB is writing... sleeping for %v...", writeWait)
								time.Sleep(writeWait)
							}
							indexer.GravDBBackend.Writing = 1
							_, _, err = indexer.GravDBBackend.StoreOwner(bl_sctxs[i].Scid, "", false)
							if err != nil {
								logger.Printf("[indexBlock] Error storing owner: %v", err)
							}
							indexer.GravDBBackend.Writing = 0
						case "boltdb":
							for indexer.BBSBackend.Writing == 1 {
								if indexer.Closing {
									return
								}
								//logger.Printf("[Indexer-IndexInvokes-StoreOwner] BoltDB is writing... sleeping for %v... writer %v...", writeWait, indexer.BBSBackend.Writer)
								time.Sleep(writeWait)
							}
							indexer.BBSBackend.Writing = 1
							indexer.BBSBackend.Writer = "IndexInvokesOwnerStore"

							_, err = indexer.BBSBackend.StoreOwner(bl_sctxs[i].Scid, "")
							if err != nil {
								logger.Printf("[indexBlock] Error storing owner: %v", err)
							}

							indexer.BBSBackend.Writing = 0
							indexer.BBSBackend.Writer = ""
						}
					}
				}

				if scidExist(indexer.ValidatedSCs, bl_sctxs[i].Scid) {
					//logger.Printf("SCID %v is validated, checking the SC TX entrypoints to see if they should be logged.", bl_sctxs[i].Scid)
					// TODO: Modify this to be either all entrypoints, just Start, or a subset that is defined in pre-run params or not needed?
					//if bl_sctxs[i].entrypoint == "Start" {
					//if bl_sctxs[i].Entrypoint == "InputStr" {
					if true {
						currsctx := bl_sctxs[i]

						//logger.Printf("Tx %v matches scinvoke call filter(s). Adding %v to DB.", bl_sctxs[i].Txid, currsctx)

						writeWait, _ := time.ParseDuration("20ms")
						switch indexer.DBType {
						case "gravdb":
							if !(indexer.RunMode == "asset") {
								for indexer.GravDBBackend.Writing == 1 {
									if indexer.Closing {
										return
									}
									//logger.Printf("[Indexer-NewIndexer] GravitonDB is writing... sleeping for %v...", writeWait)
									time.Sleep(writeWait)
								}
								indexer.GravDBBackend.Writing = 1
								var ctrees []*graviton.Tree

								// If a hardcodedscid invoke + fastsync is enabled, do not log any new details. We will only retain within DB on-launch data.
								if scidExist(hardcodedscids, bl_sctxs[i].Scid) && indexer.Fastsync {
									logger.Printf("[indexBlock] Skipping invoke detail store of '%v' since fastsync is '%v'.", bl_sctxs[i].Scid, indexer.Fastsync)
								} else {

									sidtree, sidchanges, err := indexer.GravDBBackend.StoreInvokeDetails(bl_sctxs[i].Scid, bl_sctxs[i].Sender, bl_sctxs[i].Entrypoint, bl_txns.Topoheight, &currsctx, true)
									if err != nil {
										logger.Printf("[indexBlock] Err storing invoke details. Err: %v", err)
										time.Sleep(5 * time.Second)
										indexer.GravDBBackend.Writing = 0
										return err
									} else {
										if sidchanges {
											ctrees = append(ctrees, sidtree)
										}
									}

									// Gets the SC variables (key/value) at a given topoheight and then stores them
									scVars, _, _, _ := indexer.RPC.GetSCVariables(bl_sctxs[i].Scid, bl_txns.Topoheight, nil, nil, nil)
									svdtree, svdchanges, err := indexer.GravDBBackend.StoreSCIDVariableDetails(bl_sctxs[i].Scid, scVars, bl_txns.Topoheight, true)
									if err != nil {
										logger.Printf("[indexBlock] ERR - storing scid variable details: %v", err)
									} else {
										if svdchanges {
											ctrees = append(ctrees, svdtree)
										}
									}
									sihtree, sihchanges, err := indexer.GravDBBackend.StoreSCIDInteractionHeight(bl_sctxs[i].Scid, bl_txns.Topoheight, true)
									if err != nil {
										logger.Printf("[indexBlock] ERR - storing scid interaction height: %v", err)
									} else {
										if sihchanges {
											ctrees = append(ctrees, sihtree)
										}
									}

								}

								if len(ctrees) > 0 {
									_, err := indexer.GravDBBackend.CommitTrees(ctrees)
									if err != nil {
										logger.Printf("[indexBlock] ERR - committing trees: %v", err)
									} else {
										//logger.Printf("[indexBlock] DEBUG - cv [%v]", cv)
									}
								}
								indexer.GravDBBackend.Writing = 0
							}
						case "boltdb":
							if !(indexer.RunMode == "asset") {
								for indexer.BBSBackend.Writing == 1 {
									if indexer.Closing {
										return
									}
									//logger.Printf("[Indexer-IndexInvokes] BoltDB is writing... sleeping for %v... writer %v...", writeWait, indexer.BBSBackend.Writer)
									time.Sleep(writeWait)
								}
								indexer.BBSBackend.Writing = 1
								indexer.BBSBackend.Writer = "IndexInvokesDetailsStore"

								// If a hardcodedscid invoke + fastsync is enabled, do not log any new details. We will only retain within DB on-launch data.
								if scidExist(hardcodedscids, bl_sctxs[i].Scid) && indexer.Fastsync {
									logger.Printf("[indexBlock] Skipping invoke detail store of '%v' since fastsync is '%v'.", bl_sctxs[i].Scid, indexer.Fastsync)
								} else {

									_, err := indexer.BBSBackend.StoreInvokeDetails(bl_sctxs[i].Scid, bl_sctxs[i].Sender, bl_sctxs[i].Entrypoint, bl_txns.Topoheight, &currsctx)
									if err != nil {
										logger.Printf("[indexBlock] Err storing invoke details. Err: %v", err)
										time.Sleep(5 * time.Second)
										indexer.BBSBackend.Writing = 0
										indexer.BBSBackend.Writer = ""
										return err
									}

									// Gets the SC variables (key/value) at a given topoheight and then stores them
									scVars, _, _, _ := indexer.RPC.GetSCVariables(bl_sctxs[i].Scid, bl_txns.Topoheight, nil, nil, nil)
									_, err = indexer.BBSBackend.StoreSCIDVariableDetails(bl_sctxs[i].Scid, scVars, bl_txns.Topoheight)
									if err != nil {
										logger.Printf("[indexBlock] ERR - storing scid variable details: %v", err)
									}
									_, err = indexer.BBSBackend.StoreSCIDInteractionHeight(bl_sctxs[i].Scid, bl_txns.Topoheight)
									if err != nil {
										logger.Printf("[indexBlock] ERR - storing scid interaction height: %v", err)
									}

								}
								indexer.BBSBackend.Writing = 0
								indexer.BBSBackend.Writer = ""
							}
						}

						//logger.Printf("SCID: %v ; Sender: %v ; Entrypoint: %v ; topoheight : %v ; info: %v", bl_sctxs[i].Scid, bl_sctxs[i].Sender, bl_sctxs[i].Entrypoint, topoheight, &currsctx)
						logger.Debugf("Sender: %v ; topoheight : %v ; args: %v ; burnValue: %v", bl_sctxs[i].Sender, bl_txns.Topoheight, bl_sctxs[i].Sc_args, bl_sctxs[i].Payloads[0].BurnValue)
					} else {
						//logger.Printf("Tx %v does not match scinvoke call filter(s), but %v instead. This should not (currently) be added to DB.", bl_sctxs[i].Txid, bl_sctxs[i].Entrypoint)
					}
				} else {
					//logger.Printf("SCID %v is not validated and thus we do not log SC interactions for this. Moving on.", bl_sctxs[i].Scid)
				}
			}
		}
	} else {
		//logger.Printf("Block %v does not have any SC txs", bl.GetHash())
	}

	return nil
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

// DERO.GetTxPool rpc call for returning current mempool txns
func (client *Client) GetTxPool() (txlist []string, err error) {
	// TODO: Make this a consumable func with rpc calls and timeout / wait / retry logic for deduplication of code. Or use alternate method of checking [primary use case is remote nodes]
	var reconnect_count int
	for {
		var err error

		var io rpc.GetTxPool_Result

		if err = client.RPC.CallResult(context.Background(), "DERO.GetTxPool", nil, &io); err != nil {
			if reconnect_count >= 5 {
				logger.Printf("[getTxPool] GetTxPool failed: %v . (%v / 5 times)", err, reconnect_count)
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

// DERO.GetBlockHeaderByTopoHeight rpc call for returning block hash at a particular topoheight
func (client *Client) getBlockHash(height uint64) (hash string, err error) {
	//logger.Printf("[getBlockHash] Attempting to get block details at topoheight %v", height)
	// TODO: Make this a consumable func with rpc calls and timeout / wait / retry logic for deduplication of code. Or use alternate method of checking [primary use case is remote nodes]
	var reconnect_count int
	for {
		var err error

		var io rpc.GetBlockHeaderByHeight_Result
		var ip = rpc.GetBlockHeaderByTopoHeight_Params{TopoHeight: height}

		if err = client.RPC.CallResult(context.Background(), "DERO.GetBlockHeaderByTopoHeight", ip, &io); err != nil {
			//logger.Printf("[getBlockHash] %v - GetBlockHeaderByTopoHeight failed: %v . Trying again (%v / 5)", height, err, reconnect_count)
			//return hash, fmt.Errorf("GetBlockHeaderByTopoHeight failed: %v", err)

			// TODO: Perhaps just a .Closing = true call here and then gnomonserver can be polling for any indexers with .Closing then close the rest cleanly. If packaged, then just have to handle themselves w/ .Close()
			if reconnect_count >= 5 {
				logger.Printf("[getBlockHash] %v - GetBlockHeaderByTopoHeight failed: %v . (%v / 5 times)", height, err, reconnect_count)
				break
			}
			time.Sleep(1 * time.Second)

			reconnect_count++

			continue
		} else {
			//logger.Printf("[getBlockHash] Retrieved block header from topoheight %v", height)
			//mainnet = !info.Testnet // inverse of testnet is mainnet
			//logger.Printf("%v", io)
		}

		hash = io.Block_Header.Hash
		break
	}

	return hash, err
}

// Looped interval to probe DERO.GetInfo rpc call for updating chain topoheight. Also handles keeping connection to daemon via RPC.Connect() calls
func (indexer *Indexer) getInfo() {
	var reconnect_count int
	for {
		if indexer.Closing {
			// Break out on closing call
			break
		}
		var err error

		// Check connection to be sure indexer.Endpoint hasn't changed. If it has, then update. Otherwise Connect will just return back no issues
		indexer.RPC.Connect(indexer.Endpoint)

		var info *structures.GetInfo

		// collect all the data afresh,  execute rpc to service
		if err = indexer.RPC.RPC.CallResult(context.Background(), "DERO.GetInfo", nil, &info); err != nil {
			//logger.Printf("[getInfo] ERROR - GetInfo failed: %v . Trying again (%v / 5)", err, reconnect_count)

			// TODO: Perhaps just a .Closing = true call here and then gnomonserver can be polling for any indexers with .Closing then close the rest cleanly. If packaged, then just have to handle themselves w/ .Close()
			if reconnect_count >= 5 && indexer.CloseOnDisconnect {
				indexer.Close()
				logger.Printf("[getInfo] ERROR - GetInfo failed: %v . (%v / 5 times)", err, reconnect_count)
				break
			}
			time.Sleep(1 * time.Second)
			indexer.RPC.Connect(indexer.Endpoint) // Attempt to re-connect now

			reconnect_count++

			continue
		} else {
			if reconnect_count > 0 {
				reconnect_count = 0
			}
			//mainnet = !info.Testnet // inverse of testnet is mainnet
			//logger.Printf("%v", info)
		}

		var currStoreGetInfo *structures.GetInfo
		switch indexer.DBType {
		case "gravdb":
			currStoreGetInfo = indexer.GravDBBackend.GetGetInfoDetails()
		case "boltdb":
			currStoreGetInfo = indexer.BBSBackend.GetGetInfoDetails()
		}

		if currStoreGetInfo != nil {
			// Ensure you are not connecting to testnet or mainnet unintentionally based on store getinfo history
			if currStoreGetInfo.Testnet == info.Testnet {
				if currStoreGetInfo.Height < info.Height {
					structureGetInfo := info

					writeWait, _ := time.ParseDuration("20ms")
					switch indexer.DBType {
					case "gravdb":
						for indexer.GravDBBackend.Writing == 1 {
							if indexer.Closing {
								return
							}
							//logger.Printf("[Indexer-NewIndexer] GravitonDB is writing... sleeping for %v...", writeWait)
							time.Sleep(writeWait)
						}
						indexer.GravDBBackend.Writing = 1
						_, _, err := indexer.GravDBBackend.StoreGetInfoDetails(structureGetInfo, false)
						if err != nil {
							logger.Printf("[getInfo] ERROR - GetInfo store failed: %v", err)
						}
						indexer.GravDBBackend.Writing = 0
					case "boltdb":
						for indexer.BBSBackend.Writing == 1 {
							if indexer.Closing {
								return
							}
							//logger.Printf("[Indexer-getinfo-StoreGetInfoDetails] BoltDB is writing... sleeping for %v... writer %v...", writeWait, indexer.BBSBackend.Writer)
							time.Sleep(writeWait)
						}
						indexer.BBSBackend.Writing = 1
						indexer.BBSBackend.Writer = "getInfo"
						_, err := indexer.BBSBackend.StoreGetInfoDetails(structureGetInfo)
						if err != nil {
							logger.Printf("[getInfo] ERROR - GetInfo store failed: %v", err)
						}
						indexer.BBSBackend.Writing = 0
						indexer.BBSBackend.Writer = ""
					}
				}
			} else {
				if indexer.RPC.WS != nil {
					// Remote addr (current ws connection endpoint) does not match indexer endpoint - re-connecting
					logger.Printf("[getInfo] ERROR - Endpoint network (testnet - %v) is not the same as past stored network (testnet - %v)", info.Testnet, currStoreGetInfo.Testnet)
					indexer.RPC.Lock()
					indexer.RPC.WS.Close()
					indexer.RPC.Unlock()

					indexer.Lock()
					indexer.ChainHeight = 0
					indexer.Unlock()

					time.Sleep(5 * time.Second)
					continue
				}
			}
		} else {
			structureGetInfo := info

			writeWait, _ := time.ParseDuration("20ms")
			switch indexer.DBType {
			case "gravdb":
				for indexer.GravDBBackend.Writing == 1 {
					if indexer.Closing {
						return
					}
					//logger.Printf("[Indexer-NewIndexer] GravitonDB is writing... sleeping for %v...", writeWait)
					time.Sleep(writeWait)
				}
				indexer.GravDBBackend.Writing = 1
				_, _, err := indexer.GravDBBackend.StoreGetInfoDetails(structureGetInfo, false)
				if err != nil {
					logger.Printf("[getInfo] ERROR - GetInfo store failed: %v", err)
				}
				indexer.GravDBBackend.Writing = 0
			case "boltdb":
				for indexer.BBSBackend.Writing == 1 {
					if indexer.Closing {
						return
					}
					//logger.Printf("[Indexer-getinfo-StoreGetInfoDetails] BoltDB is writing... sleeping for %v... writer %v...", writeWait, indexer.BBSBackend.Writer)
					time.Sleep(writeWait)
				}
				indexer.BBSBackend.Writing = 1
				indexer.BBSBackend.Writer = "getInfo"
				_, err := indexer.BBSBackend.StoreGetInfoDetails(structureGetInfo)
				if err != nil {
					logger.Printf("[getInfo] ERROR - GetInfo store failed: %v", err)
				}
				indexer.BBSBackend.Writing = 0
				indexer.BBSBackend.Writer = ""
			}
		}
		indexer.Lock()
		indexer.ChainHeight = info.TopoHeight
		indexer.Unlock()

		time.Sleep(5 * time.Second)
	}
}

// Looped interval to probe WALLET.GetHeight rpc call for updating wallet height
func (indexer *Indexer) getWalletHeight() {
	for {
		if indexer.Closing {
			// Break out on closing call
			break
		}
		var err error

		var info rpc.GetHeight_Result

		// collect all the data afresh,  execute rpc to service
		if err = indexer.RPC.RPC.CallResult(context.Background(), "WALLET.GetHeight", nil, &info); err != nil {
			logger.Printf("[getWalletHeight] ERROR - GetHeight failed: %v", err)
			time.Sleep(1 * time.Second)
			indexer.RPC.Connect(indexer.Endpoint) // Attempt to re-connect now
			continue
		} else {
			//mainnet = !info.Testnet // inverse of testnet is mainnet
			//logger.Printf("%v", info)
		}

		indexer.Lock()
		indexer.ChainHeight = int64(info.Height)
		indexer.Unlock()

		time.Sleep(5 * time.Second)
	}
}

// Gets SC variable details
func (client *Client) GetSCVariables(scid string, topoheight int64, keysuint64 []uint64, keysstring []string, keysbytes [][]byte) (variables []*structures.SCIDVariable, code string, balances map[string]uint64, err error) {
	//balances = make(map[string]uint64)

	var getSCResults rpc.GetSC_Result
	getSCParams := rpc.GetSC_Params{SCID: scid, Code: true, Variables: true, TopoHeight: topoheight}
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

			//logger.Printf("[GetSCVariables] ERROR - GetSCVariables failed for '%v': %v . Trying again (%v / 5)", scid, err, reconnect_count+1)
			if reconnect_count >= 5 {
				logger.Printf("[GetSCVariables] ERROR - GetSCVariables failed for '%v': %v . (%v / 5 times)", scid, err, reconnect_count)
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
			p := new(crypto.Point)
			if err := p.DecodeCompressed(dstr); err == nil {

				addr := rpc.NewAddressFromKeys(p)
				currVar.Value = addr.String()
			} else {
				currVar.Value = string(dstr)
			}
		default:
			// non-string/uint64 (shouldn't be here actually since it's either uint64 or string conversion)
			str := fmt.Sprintf("%v", cval)
			currVar.Value = str
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
				currVar.Value = string(decd)
			}
		case uint64:
			currVar.Value = cval
		case float64:
			currVar.Value = uint64(cval)
		default:
			// non-string/uint64 (shouldn't be here actually since it's either uint64 or string conversion)
			str := fmt.Sprintf("%v", cval)
			currVar.Value = str
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

// Gets SC variable keys at given topoheight who's value equates to a given interface{} (string/uint64)
func (indexer *Indexer) GetSCIDKeysByValue(variables []*structures.SCIDVariable, scid string, val interface{}, height int64) (keysstring []string, keysuint64 []uint64, err error) {
	// If variables were not provided, then fetch them.
	if len(variables) <= 0 {
		// Can't pass the val interface{} as the input params for getsc only are in reference to key lookup or all variables return (edge in event of v139 daemon)
		variables, _, _, err = indexer.RPC.GetSCVariables(scid, height, nil, nil, nil)
		if err != nil {
			logger.Printf("[GetSCIDKeysByValue] ERROR during GetSCVariables - %v", err)
			return
		}
	}

	// Switch against the value passed. If it's a uint64 or string
	switch inpvar := val.(type) {
	case uint64:
		for _, v := range variables {
			switch cval := v.Value.(type) {
			case uint64:
				if inpvar == cval {
					switch ckey := v.Key.(type) {
					case float64:
						keysuint64 = append(keysuint64, uint64(ckey))
					case uint64:
						keysuint64 = append(keysuint64, ckey)
					default:
						// default just store as string. Keys should only ever be strings or uint64, however, but assume default to string
						keysstring = append(keysstring, v.Key.(string))
					}
				}
			default:
				// Nothing - expect only string/uint64 for value types
			}
		}
	case string:
		for _, v := range variables {
			switch cval := v.Value.(type) {
			case string:
				if inpvar == cval {
					switch ckey := v.Key.(type) {
					case float64:
						keysuint64 = append(keysuint64, uint64(ckey))
					case uint64:
						keysuint64 = append(keysuint64, ckey)
					default:
						// default just store as string. Keys should only ever be strings or uint64, however, but assume default to string
						keysstring = append(keysstring, v.Key.(string))
					}
				}
			default:
				// Nothing - expect only string/uint64 for value types
			}
		}
	default:
		// Nothing - expect only string/uint64 for value types
	}

	return keysstring, keysuint64, err
}

// Gets SC values by key at given topoheight who's key equates to a given interface{} (string/uint64)
func (indexer *Indexer) GetSCIDValuesByKey(variables []*structures.SCIDVariable, scid string, key interface{}, height int64) (valuesstring []string, valuesuint64 []uint64, err error) {
	// If variables were not provided, then fetch them.
	if len(variables) <= 0 {
		// Can pass the key interface{} in the input param.
		var keysuint64 []uint64
		var keysstring []string
		var keysbytes [][]byte
		switch ta := key.(type) {
		case uint64:
			keysuint64 = append(keysuint64, ta)
		case string:
			keysstring = append(keysstring, ta)
		default:
			// Nothing - expect only string/uint64 for value types
		}

		variables, _, _, err = indexer.RPC.GetSCVariables(scid, height, keysuint64, keysstring, keysbytes)
		if err != nil {
			logger.Printf("[GetSCIDValuesByKey] ERROR during GetSCVariables - %v", err)
			return
		}
	}

	// Switch against the value passed. If it's a uint64 or string
	switch inpvar := key.(type) {
	case uint64:
		for _, v := range variables {
			switch ckey := v.Key.(type) {
			case uint64:
				if inpvar == ckey {
					switch cval := v.Value.(type) {
					case float64:
						valuesuint64 = append(valuesuint64, uint64(cval))
					case uint64:
						valuesuint64 = append(valuesuint64, cval)
					default:
						// default just store as string. Keys should only ever be strings or uint64, however, but assume default to string
						valuesstring = append(valuesstring, v.Value.(string))
					}
				}
			default:
				// Nothing - expect only string/uint64 for value types
			}
		}
	case string:
		for _, v := range variables {
			switch ckey := v.Key.(type) {
			case string:
				if inpvar == ckey {
					switch cval := v.Value.(type) {
					case float64:
						valuesuint64 = append(valuesuint64, uint64(cval))
					case uint64:
						valuesuint64 = append(valuesuint64, cval)
					default:
						// default just store as string. Values should only ever be strings or uint64, however, but assume default to string
						valuesstring = append(valuesstring, v.Value.(string))
					}
				}
			default:
				// Nothing - expect only string/uint64 for value types
			}
		}
	default:
		// Nothing - expect only string/uint64 for value types
	}

	return valuesstring, valuesuint64, err
}

// Converts returned SCIDVariables KEY values who's values equates to a given interface{} (string/uint64)
func (indexer *Indexer) ConvertSCIDKeys(variables []*structures.SCIDVariable) (keysstring []string, keysuint64 []uint64) {
	for _, v := range variables {
		switch ckey := v.Key.(type) {
		case float64:
			keysuint64 = append(keysuint64, uint64(ckey))
		case uint64:
			keysuint64 = append(keysuint64, ckey)
		default:
			// default just store as string. Keys should only ever be strings or uint64, however, but assume default to string
			keysstring = append(keysstring, v.Key.(string))
		}
	}

	return keysstring, keysuint64
}

// Converts returned SCIDVariables VALUE values who's values equates to a given interface{} (string/uint64)
func (indexer *Indexer) ConvertSCIDValues(variables []*structures.SCIDVariable) (valuesstring []string, valuesuint64 []uint64) {
	for _, v := range variables {
		switch cval := v.Value.(type) {
		case float64:
			valuesuint64 = append(valuesuint64, uint64(cval))
		case uint64:
			valuesuint64 = append(valuesuint64, cval)
		default:
			// default just store as string. Keys should only ever be strings or uint64, however, but assume default to string
			valuesstring = append(valuesstring, v.Value.(string))
		}
	}

	return valuesstring, valuesuint64
}

// Validates that a stored signature results in the code deployed to a SC - currently allowing any 'key' to be passed through, however intended key is 'signature' or similar
func (indexer *Indexer) ValidateSCSignature(code string, key string) (validated bool, signer string, err error) {
	if key == "" {
		return
	}

	// CheckSignature
	filedata := []byte(key)
	p, _ := pem.Decode(filedata)
	if p == nil {
		logger.Printf("[ValidateSCSignature] ERR - Unknown format of input data - %v", key)
		return
	}

	astr := p.Headers["Address"]
	cstr := p.Headers["C"]
	sstr := p.Headers["S"]

	addr, err := rpc.NewAddress(astr)
	if err != nil {
		logger.Printf("[ValidateSCSignature] ERR - Cannot validate Address header")
		return
	}

	c, ok := new(big.Int).SetString(cstr, 16)
	if !ok {
		err = fmt.Errorf("[ValidateSCSignature] Unknown C format")
		return
	}

	s, ok := new(big.Int).SetString(sstr, 16)
	if !ok {
		err = fmt.Errorf("[ValidateSCSignature] Unknown S format")
		return
	}

	tmppoint := new(bn256.G1).Add(new(bn256.G1).ScalarMult(crypto.G, s), new(bn256.G1).ScalarMult(addr.PublicKey.G1(), new(big.Int).Neg(c)))
	serialize := []byte(fmt.Sprintf("%s%s%x", addr.PublicKey.G1().String(), tmppoint.String(), p.Bytes))

	c_calculated := crypto.ReducedHash(serialize)
	if c.String() != c_calculated.String() {
		err = fmt.Errorf("[ValidateSCSignature] signature mismatch")
		return
	}

	signer = addr.String()
	message := p.Bytes

	if string(message) == code {
		validated = true
	}

	return
}

// Close cleanly the indexer
func (ind *Indexer) Close() {
	// Tell indexer a closing operation is happening; this will close out loops on next iteration
	ind.Closing = true

	switch ind.DBType {
	case "gravdb":
		ind.GravDBBackend.Closing = true
	case "boltdb":
		ind.BBSBackend.Closing = true
	}

	// Sleep for safety
	time.Sleep(time.Second * 1)

	// Close websocket connection cleanly
	if ind.RPC.WS != nil {
		ind.RPC.WS.Close()
	}

	// Close out grav db cleanly
	writeWait, _ := time.ParseDuration("20ms")
	switch ind.DBType {
	case "gravdb":
		for ind.GravDBBackend.Writing == 1 {
			if ind.Closing {
				return
			}
			//logger.Printf("[Indexer-NewIndexer] GravitonDB is writing... sleeping for %v...", writeWait)
			time.Sleep(writeWait)
		}
		ind.GravDBBackend.Writing = 1
		ind.GravDBBackend.DB.Close()
		ind.GravDBBackend.Writing = 0
	case "boltdb":
		for ind.BBSBackend.Writing == 1 {
			if ind.Closing {
				return
			}
			//logger.Printf("[Indexer-Close] BoltDB is writing... sleeping for %v... writer %v...", writeWait, ind.BBSBackend.Writer)
			time.Sleep(writeWait)
		}
		ind.BBSBackend.Writing = 1
		ind.BBSBackend.Writer = "Close"
		ind.BBSBackend.DB.Sync()
		ind.BBSBackend.DB.Close()
		ind.BBSBackend.Writing = 0
		ind.BBSBackend.Writer = ""
	}
}

func InitLog(args map[string]interface{}, console io.Writer) {
	loglevel_console := logrus.InfoLevel

	if args["--debug"] != nil && args["--debug"].(bool) == true {
		loglevel_console = logrus.DebugLevel
	}

	structures.Logger = logrus.Logger{
		Out:   console,
		Level: loglevel_console,
		Formatter: &prefixed.TextFormatter{
			ForceColors:     true,
			DisableColors:   false,
			TimestampFormat: "01/02/2006 15:04:05",
			FullTimestamp:   true,
			ForceFormatting: true,
		},
	}
}
