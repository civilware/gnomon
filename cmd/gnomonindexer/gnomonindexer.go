package main

import (
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chzyer/readline"
	"github.com/civilware/Gnomon/api"
	"github.com/civilware/Gnomon/indexer"
	"github.com/civilware/Gnomon/mbllookup"
	"github.com/civilware/Gnomon/storage"
	"github.com/civilware/Gnomon/structures"
	"github.com/civilware/Gnomon/wsserver"
	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/globals"

	"github.com/docopt/docopt-go"

	"github.com/sirupsen/logrus"
)

type GnomonServer struct {
	LastIndexedHeight int64
	SearchFilters     []string
	Indexers          map[string]*indexer.Indexer
	Closing           bool
	DaemonEndpoint    string
	RunMode           string
	DBType            string
	MBLLookup         bool
}

var command_line string = `Gnomon
Gnomon Indexing Service: Index DERO's blockchain for Smart Contract deployments/listings/etc. as well as other data analysis.

Usage:
  gnomonindexer [options]
  gnomonindexer -h | --help

Options:
  -h --help     Show this screen.
  --daemon-rpc-address=<127.0.0.1:40402>    Connect to daemon.
  --enable-api    Enable api/ws
  --api-address=<127.0.0.1:8082>     Host api.
  --enable-api-ssl     Enable ssl.
  --api-ssl-address=<127.0.0.1:9092>     Host ssl api.
  --get-info-ssl-address=<127.0.0.1:9394>     Host GetInfo ssl api. This is to completely isolate it from gnomon api results as a whole. Normal api endpoints also surface the getinfo call if needed.
  --ws-address=<127.0.0.1:9190>     Host websocket server.
  --start-topoheight=<31170>     Define a start topoheight other than 1 if required to index at a higher block (pruned db etc.).
  --search-filter=<"Function InputStr(input String, varname String) Uint64">     Defines a search filter to match on installed SCs to add to validated list and index all actions, this will most likely change in the future but can allow for some small variability. Include escapes etc. if required. If nothing is defined, it will pull all (minus hardcoded sc).
  --runmode=<daemon>     Defines the runmode of gnomon (daemon/wallet/asset). By default this is daemon mode which indexes directly from the chain. Wallet mode indexes from wallet tx history (use/store with caution).
  --enable-miniblock-lookup     True/false value to store all miniblocks and their respective details and miner addresses who found them. This currently REQUIRES a full node db in same directory
  --store-integrators     True/false value to store integrator addresses for each block and keep track of how many blocks they've submitted
  --close-on-disconnect     True/false value to close out indexers in the event of daemon disconnect. Daemon will fail connections for 30 seconds and then close the indexer. This is for HA pairs or wanting services off on disconnect.
  --fastsync     True/false value to define loading at chain height and only keeping track of list of SCIDs and their respective up-to-date variable stores as it hits them. NOTE: You will not get all information and may rely on manual scid additions.
  --skipfsrecheck     True/false value (only relevant when --fastsync is used) to define if SC validity should be re-checked from data coming via Gnomon SC index or not.
  --forcefastsync     True/false value (only relevant when --fastsync is used) to force fastsync to occur if chainheight and stored index height differ greater than 100 blocks or n blocks represented by -forcefastsyncdiff.
  --forcefastsyncdiff=<100>     Int64 value (only relevant when --fastsync is used) to force fastsync to occur if chainheight and stored index height differ greater than supplied number of blocks.
  --nocode     True/false value (only relevant when --fastsync and --skipfsrecheck are used) to index code from the fastsync index if skipfsrecheck is defined.
  --dbtype=<boltdb>     Defines type of database. 'gravdb' or 'boltdb'. If gravdb, expect LARGE local storage if running in daemon mode until further optimized later. [--ramstore can only be valid with gravdb]. Defaults to boltdb.
  --ramstore     True/false value to define if the db [only if gravdb] will be used in RAM or on disk. Keep in mind on close, the RAM store will be non-persistent.
  --num-parallel-blocks=<5>     Defines the number of parallel blocks to index in daemonmode. While a lower limit of 1 is defined, there is no hardcoded upper limit. Be mindful the higher set, the greater the daemon load potentially (highly recommend local nodes if this is greater than 1-5)
  --remove-api-throttle     Removes the api throttle against number of sc variables, sc invoke data etc. to return
  --sf-scid-exclusions=<"a05395bb0cf77adc850928b0db00eb5ca7a9ccbafd9a38d021c8d299ad5ce1a4;;;c9d23d2fc3aaa8e54e238a2218c0e5176a6e48780920fd8474fac5b0576110a2">     Defines a scid or scids (use const separator [default ';;;']) to be excluded from indexing regardless of search-filter. If nothing is defined, all scids that match the search-filter will be indexed.
  --skip-gnomonsc-index     If the gnomonsc is caught within the supplied search filter, you can skip indexing that SC given the size/depth of calls to that SC for increased sync times.
  --gnomondb-dest-dir=<"">     Defines the destination directory where gnomondb will be stored
  --derodb-dir=<"">     Defines the location where derodb can be referenced from
  --debug     Enables debug logging`

var Exit_In_Progress = make(chan bool)

var RLI *readline.Instance

var Gnomon = &GnomonServer{}

// local logger
var logger *logrus.Entry

// TODO: Add as a passable param perhaps? Or other. Using ;;; for now, can be anything really.. just think what isn't used in norm SC code iterations
const sf_separator = ";;;"

func main() {
	var err error

	n := runtime.NumCPU()
	runtime.GOMAXPROCS(n)

	Gnomon.Indexers = make(map[string]*indexer.Indexer)

	// Inspect argument(s)
	arguments, err := docopt.ParseArgs(command_line, nil, structures.Version.String())
	if err != nil {
		log.Fatalf("[Main] Error while parsing arguments err: %s", err)
	}

	// Readline GNOMON
	RLI, err = readline.NewEx(&readline.Config{
		Prompt:          "\033[92mGNOMON\033[32m>>>\033[0m ",
		HistoryFile:     filepath.Join(os.TempDir(), "gnomon_readline.tmp"),
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",

		HistorySearchFold:   true,
		FuncFilterInputRune: filterInput,
	})
	if err != nil {
		fmt.Printf("Error starting readline err: %s", err)
		return
	}
	defer RLI.Close()

	// setup logging
	indexer.InitLog(arguments, RLI.Stdout())
	logger = structures.Logger.WithFields(logrus.Fields{})

	// Set variables from arguments
	gnomondb_wd, err := os.Getwd()
	if err != nil {
		logger.Fatalf("[Main] Err getting working directory: %v", err)
	}
	if arguments["--gnomondb-dest-dir"] != nil && arguments["--gnomondb-dest-dir"].(string) != "" {
		gnomondb_wd = arguments["--gnomondb-dest-dir"].(string)
		logger.Printf("[Main] Using gnomondb destination directory '%s'", gnomondb_wd)
		_, err = os.Stat(gnomondb_wd)
		if err != nil {
			logger.Fatalf("[Main] Err setting gnomondb working directory to '%s'", gnomondb_wd)
		}
	}

	if arguments["--derodb-dir"] != nil && arguments["--derodb-dir"].(string) != "" {
		derodb_wd := arguments["--derodb-dir"].(string)
		err = mbllookup.SetDeroDBWD(derodb_wd)
		if err != nil {
			logger.Fatalf("[Main] Err setting derodb directory to '%s'", derodb_wd)
		}
	}

	daemon_endpoint := "127.0.0.1:40402"
	if arguments["--daemon-rpc-address"] != nil {
		daemon_endpoint = arguments["--daemon-rpc-address"].(string)
	}
	Gnomon.DaemonEndpoint = daemon_endpoint

	logger.Printf("[Main] Using daemon RPC endpoint %s", daemon_endpoint)

	var enableapi bool
	if arguments["--enable-api"] != nil && arguments["--enable-api"].(bool) == true {
		enableapi = true
	}

	api_endpoint := "127.0.0.1:8082"
	if arguments["--api-address"] != nil {
		api_endpoint = arguments["--api-address"].(string)
	}

	api_ssl_endpoint := "127.0.0.1:9092"
	if arguments["--api-ssl-address"] != nil {
		api_ssl_endpoint = arguments["--api-ssl-address"].(string)
	}

	ws_endpoint := "127.0.0.1:9190"
	if arguments["--ws-address"] != nil {
		ws_endpoint = arguments["--ws-address"].(string)
	}

	get_info_ssl_endpoint := "127.0.0.1:9394"
	if arguments["--get-info-ssl-address"] != nil {
		get_info_ssl_endpoint = arguments["--get-info-ssl-address"].(string)
	}

	var sslenabled bool
	if arguments["--enable-api-ssl"] != nil && arguments["--enable-api-ssl"].(bool) == true {
		sslenabled = true
	}

	Gnomon.RunMode = "daemon"
	if arguments["--runmode"] != nil {
		if arguments["--runmode"] == "daemon" || arguments["--runmode"] == "wallet" || arguments["--runmode"] == "asset" {
			Gnomon.RunMode = arguments["--runmode"].(string)
		} else {
			logger.Fatalf("[Main] ERR - Runmode must be either 'daemon' or 'wallet'")
			return
		}
	}

	last_indexedheight := int64(1)
	if arguments["--start-topoheight"] != nil {
		last_indexedheight, err = strconv.ParseInt(arguments["--start-topoheight"].(string), 10, 64)
		if err != nil {
			logger.Fatalf("[Main] ERROR while converting --start-topoheight to int64")
			return
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

	var mbl bool
	if arguments["--enable-miniblock-lookup"] != nil && arguments["--enable-miniblock-lookup"].(bool) == true {
		mbl = true

		err = mbllookup.DeroDB.LoadDeroDB()
		if err != nil {
			logger.Fatalf("[Main] ERR Loading DeroDB - Be sure to run from directory of fully synced mainnet - %v", err)
			return
		}
	}
	Gnomon.MBLLookup = mbl

	var storeintegrators bool
	if arguments["--store-integrators"] != nil && arguments["--store-integrators"].(bool) == true {
		storeintegrators = true
	}

	numParallelBlocks := 1
	if arguments["--num-parallel-blocks"] != nil {
		numParallelBlocks, err = strconv.Atoi(arguments["--num-parallel-blocks"].(string))
		if err != nil {
			logger.Fatalf("[Main] ERR converting '%v' to int for --num-parallel-blocks.", arguments["--num-parllel-blocks"].(string))
		}
	}

	// Edge flag to be able to close on disconnect from a daemon after x failures. Can be used for smaller nodes or other areas where you want the API to offline when no new data is ingested/indexed.
	var closeondisconnect bool
	if arguments["--close-on-disconnect"] != nil && arguments["--close-on-disconnect"].(bool) == true {
		closeondisconnect = true
	}

	// Starts at current chainheight and retrieves a list of SCIDs to auto-add to index validation list
	var fastsync bool
	if arguments["--fastsync"] != nil && arguments["--fastsync"].(bool) == true {
		fastsync = true
	}

	// If fastsync, define addtl fastsync config options
	var skipfsrecheck bool
	var forcefastsync bool
	var nocode bool
	forcefastsyncdiff := structures.FORCE_FASTSYNC_DIFF
	if fastsync {
		if arguments["--skipfsrecheck"] != nil && arguments["--skipfsrecheck"].(bool) == true {
			skipfsrecheck = true
		}
		if arguments["--forcefastsync"] != nil && arguments["--forcefastsync"].(bool) == true {
			forcefastsync = true
		}
		if arguments["--nocode"] != nil && arguments["--nocode"].(bool) == true {
			nocode = true
		}
		if arguments["--forcefastsyncdiff"] != nil {
			ffsdatoi, err := strconv.Atoi(arguments["--forcefastsyncdiff"].(string))
			if err != nil {
				logger.Fatalf("[Main] ERR converting '%v' to int for --forcefastsyncdiff.", arguments["--forcefastsyncdiff"].(string))
			}
			forcefastsyncdiff = int64(ffsdatoi)
		}
	}

	Gnomon.DBType = "boltdb"
	if arguments["--dbtype"] != nil {
		if arguments["--dbtype"] == "boltdb" || arguments["--dbtype"] == "gravdb" {
			Gnomon.DBType = arguments["--dbtype"].(string)
		} else {
			logger.Fatalf("[Main] ERR - dbtype must be either 'boltdb' or 'gravdb'")
			return
		}
	}

	// Uses RAM store for grav db
	var ramstore bool
	if arguments["--ramstore"] != nil && arguments["--ramstore"].(bool) == true && Gnomon.DBType == "gravdb" {
		ramstore = true
	}

	// Enable api throttle (or disable if set)
	api_throttle := true
	if arguments["--remove-api-throttle"] != nil && arguments["--remove-api-throttle"].(bool) == true {
		api_throttle = false
	}

	// Database
	var Graviton_backend *storage.GravitonStore
	var Bbs_backend *storage.BboltStore
	var csearch_filter string

	switch Gnomon.DBType {
	case "gravdb":
		if ramstore {
			Graviton_backend, err = storage.NewGravDBRAM("25ms")
			if err != nil {
				logger.Fatalf("[Main] Err creating gravdb: %v", err)
			}
		} else {
			var shasum string
			if len(search_filter) == 0 {
				shasum = fmt.Sprintf("%x", sha1.Sum([]byte("gnomon")))
			} else {
				csearch_filter = strings.Join(search_filter, sf_separator)
				shasum = fmt.Sprintf("%x", sha1.Sum([]byte(csearch_filter)))
			}
			db_folder := fmt.Sprintf("gnomondb\\%s_%s", "GNOMON", shasum)
			db_path := filepath.Join(gnomondb_wd, db_folder)
			Graviton_backend, err = storage.NewGravDB(db_path, "25ms")
			if err != nil {
				logger.Fatalf("[Main] Err creating gravdb: %v", err)
			}
		}

	case "boltdb":
		var shasum string
		if len(search_filter) == 0 {
			shasum = fmt.Sprintf("%x", sha1.Sum([]byte("gnomon")))
		} else {
			csearch_filter = strings.Join(search_filter, sf_separator)
			shasum = fmt.Sprintf("%x", sha1.Sum([]byte(csearch_filter)))
		}
		db_name := fmt.Sprintf("%s_%s.db", "GNOMON", shasum)
		db_path := filepath.Join(gnomondb_wd, "gnomondb")
		Bbs_backend, err = storage.NewBBoltDB(db_path, db_name)
		if err != nil {
			logger.Fatalf("[Main] Err creating boltdb: %v", err)
		}
	}

	// API
	apic := &structures.APIConfig{
		Enabled:              true,
		Listen:               api_endpoint,
		StatsCollectInterval: "5s",
		SSL:                  sslenabled,
		SSLListen:            api_ssl_endpoint,
		GetInfoSSLListen:     get_info_ssl_endpoint,
		CertFile:             "fullchain.cer",
		GetInfoCertFile:      "getinfofullchain.cer",
		KeyFile:              "cert.key",
		GetInfoKeyFile:       "getinfocert.key",
		MBLLookup:            mbl,
		ApiThrottle:          api_throttle,
	}
	// TODO: Add default search filter index of sorts, rather than passing through Graviton_backend object as a whole
	if enableapi {
		apis := api.NewApiServer(apic, Graviton_backend, Bbs_backend, Gnomon.DBType)
		go apis.Start()
	}

	// Start default indexer based on search_filter params
	fsc := &structures.FastSyncConfig{
		Enabled:           fastsync,
		SkipFSRecheck:     skipfsrecheck,
		ForceFastSync:     forcefastsync,
		ForceFastSyncDiff: forcefastsyncdiff,
		NoCode:            nocode,
	}
	defaultIndexer := indexer.NewIndexer(Graviton_backend, Bbs_backend, Gnomon.DBType, search_filter, last_indexedheight, daemon_endpoint, Gnomon.RunMode, mbl, closeondisconnect, fsc, sf_scid_exclusions, storeintegrators)

	switch Gnomon.RunMode {
	case "daemon":
		go defaultIndexer.StartDaemonMode(numParallelBlocks)
		if enableapi {
			go wsserver.ListenWS(ws_endpoint, defaultIndexer)
		}
	case "wallet":
		go defaultIndexer.StartWalletMode("")
	case "asset":
		go defaultIndexer.StartDaemonMode(numParallelBlocks)
	default:
		go defaultIndexer.StartDaemonMode(numParallelBlocks)
	}
	Gnomon.Indexers[csearch_filter] = defaultIndexer

	go func() {
		for {
			if err = Gnomon.readline_loop(RLI); err == nil {
				break
			}
		}
	}()

	// This tiny goroutine continuously updates status as required
	go func() {
		for {
			select {
			case <-Exit_In_Progress:
				Gnomon.Close()
				return
			default:
			}
			if Gnomon.Closing {
				return
			}

			validatedSCIDs := make(map[string]string)
			switch Gnomon.DBType {
			case "gravdb":
				validatedSCIDs = Graviton_backend.GetAllOwnersAndSCIDs()
			case "boltdb":
				validatedSCIDs = Bbs_backend.GetAllOwnersAndSCIDs()
			}

			gnomon_count := int64(len(validatedSCIDs))

			currheight := defaultIndexer.LastIndexedHeight

			// choose color based on urgency
			color := "\033[32m" // default is green color
			if currheight < defaultIndexer.ChainHeight {
				color = "\033[33m" // make prompt yellow
			} else if currheight > defaultIndexer.ChainHeight {
				color = "\033[31m" // make prompt red
			}

			gcolor := "\033[32m" // default is green color
			if gnomon_count < 1 {
				gcolor = "\033[33m" // make prompt yellow
			}

			RLI.SetPrompt(fmt.Sprintf("\033[1m\033[32mGNOMON \033[0m"+color+"[%d/%d] "+gcolor+"R:%d G:%d >>\033[0m ", currheight, defaultIndexer.ChainHeight, gnomon_count, len(Gnomon.Indexers)))
			RLI.Refresh()
			time.Sleep(3 * time.Second)
		}
	}()

	setPasswordCfg := RLI.GenPasswordConfig()
	setPasswordCfg.SetListener(func(line []rune, pos int, key rune) (newLine []rune, newPos int, ok bool) {
		RLI.SetPrompt(fmt.Sprintf("Enter password(%v): ", len(line)))
		RLI.Refresh()
		return nil, 0, false
	})
	RLI.Refresh() // refresh the prompt

	// Hold
	select {}
}

func filterInput(r rune) (rune, bool) {
	switch r {
	// block CtrlZ feature
	case readline.CharCtrlZ:
		return r, false
	}
	return r, true
}

func (g *GnomonServer) readline_loop(l *readline.Instance) (err error) {

	/*
		defer func() {
			if r := recover(); r != nil {
				logger.Printf("[Main] Readline_loop err: %v", err)
				err = fmt.Errorf("crashed")
			}
		}()
	*/

	//restart_loop:
	for {
		line, err := RLI.Readline()
		if err == io.EOF {
			<-Exit_In_Progress
			return nil
		}

		if err == readline.ErrInterrupt {
			if len(line) == 0 {
				logger.Printf("[Main] Ctrl-C received, putting gnomes to sleep. This will take ~5sec.")
				g.Close()
				return nil
			} else {
				continue
			}
		}

		line = strings.TrimSpace(line)
		line_parts := strings.Fields(line)

		command := ""
		if len(line_parts) >= 1 {
			command = strings.ToLower(line_parts[0])
		}

		// TODO: CLI commands may not necessarily always need to print from every indexer, could produce multiple results. Issues? Maybe modify in future.
		switch {
		case line == "help":
			usage(l.Stderr())
		case line == "version":
			logger.Printf("Version: %v", structures.Version.String())
		case command == "listsc":
			// Split up line_parts and identify any common language filtering
			filt_line_parts := indexer.SplitLineParts(line_parts, "|")

			if len(line_parts) >= 2 && len(line_parts[1]) == 66 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)

					scinstalls, _ := wsserver.ListSC(context.Background(), structures.WS_ListSC_Params{Address: line_parts[1]}, vi)

					if len(scinstalls.ListSC) > 0 {
						// Sort heights so most recent is index 0 [if preferred reverse, just swap > with <]
						sort.SliceStable(scinstalls.ListSC, func(i, j int) bool {
							return scinstalls.ListSC[i].Height < scinstalls.ListSC[j].Height
						})

						// Filter line inputs (if applicable) and return a trimmed list to print out to cli
						var filteredResults []*structures.SCTXParse
						if len(filt_line_parts) > 1 {
							for i := range filt_line_parts {
								if i == 0 {
									filteredResults = vi.PipeFilter(filt_line_parts[i], scinstalls.ListSC)
								} else {
									filteredResults = vi.PipeFilter(filt_line_parts[i], filteredResults)
								}

							}
						} else {
							filteredResults = vi.PipeFilter(filt_line_parts[0], scinstalls.ListSC)
						}

						for _, invoke := range filteredResults {
							logger.Printf("SCID: %v ; Owner: %v ; DeployHeight: %v", invoke.Scid, invoke.Sender, invoke.Height)
						}

						logger.Printf("Total SCs installed: %v", len(filteredResults))
					} else {
						logger.Printf("No SCIDs installed by %v", line_parts[1])
					}
				}
			} else if len(line_parts) >= 2 && len(line_parts[1]) == 64 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)

					scinstalls, _ := wsserver.ListSC(context.Background(), structures.WS_ListSC_Params{SCID: line_parts[1]}, vi)

					if len(scinstalls.ListSC) > 0 {
						// Sort heights so most recent is index 0 [if preferred reverse, just swap > with <]
						sort.SliceStable(scinstalls.ListSC, func(i, j int) bool {
							return scinstalls.ListSC[i].Height < scinstalls.ListSC[j].Height
						})

						// Filter line inputs (if applicable) and return a trimmed list to print out to cli
						var filteredResults []*structures.SCTXParse
						if len(filt_line_parts) > 1 {
							for i := range filt_line_parts {
								if i == 0 {
									filteredResults = vi.PipeFilter(filt_line_parts[i], scinstalls.ListSC)
								} else {
									filteredResults = vi.PipeFilter(filt_line_parts[i], filteredResults)
								}

							}
						} else {
							filteredResults = vi.PipeFilter(filt_line_parts[0], scinstalls.ListSC)
						}

						for _, invoke := range filteredResults {
							logger.Printf("SCID: %v ; Owner: %v ; DeployHeight: %v", invoke.Scid, invoke.Sender, invoke.Height)
						}

						logger.Printf("Total SCs installed: %v", len(filteredResults))
					} else {
						logger.Printf("No SCIDs installed by %v", line_parts[1])
					}
				}
			} else {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)

					scinstalls, _ := wsserver.ListSC(context.Background(), structures.WS_ListSC_Params{}, vi)

					for _, v := range scinstalls.ListSC {
						logger.Printf("SCID: %v ; Owner: %v", v.Scid, v.Sender)
					}
				}
			}
		case command == "listsc_hardcoded":
			// Simple print out of hardcoded scid for reference point
			listsc_hardcoded, _ := wsserver.ListSCHardcoded(context.Background())
			for _, s := range listsc_hardcoded.SCHardcoded {
				logger.Printf("%s", s)
			}
		case command == "listsc_code":
			switch len(line_parts) {
			case 2:
				i := 0
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					sccode, _ := wsserver.ListSCCode(context.Background(), structures.WS_ListSCCode_Params{SCID: line_parts[1]}, vi)

					if sccode.Code != "" {
						logger.Printf("SCID: %v ; Owner: %v", line_parts[1], sccode.Owner)
						logger.Printf("%s", sccode.Code)
						i++
						break
					} else {
						continue
					}
				}

				if i == 0 {
					logger.Printf("SCID '%s' code was unable to be retrieved. Is it installed?", line_parts[1])
				}
			case 3:
				if s, err := strconv.Atoi(line_parts[2]); err == nil {
					i := 0
					for ki, vi := range g.Indexers {
						logger.Printf("- Indexer '%v'", ki)
						sccode, _ := wsserver.ListSCCode(context.Background(), structures.WS_ListSCCode_Params{SCID: line_parts[1], Height: int64(s)}, vi)

						if sccode.Code != "" {
							logger.Printf("SCID: %v ; Owner: %v", line_parts[1], sccode.Owner)
							logger.Printf("%s", sccode.Code)
							i++
							break
						} else {
							continue
						}
					}

					if i == 0 {
						logger.Printf("SCID '%s' code was unable to be retrieved at height '%v'. Was it installed?", line_parts[1], int64(s))
					}
				} else {
					logger.Errorf("Could not parse '%v' into an int for height", line_parts[2])
				}

			default:
				logger.Printf("listsc_code needs one value: single scid")
			}
		case command == "listsc_codematch":
			if len(line_parts) >= 2 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					var err error
					var result structures.WS_ListSCCodeMatch_Result
					if len(line_parts) == 2 {
						result, err = wsserver.ListSCCodeMatch(context.Background(), structures.WS_ListSCCodeMatch_Params{Match: line_parts[1], IncludeCode: true}, vi)
					} else {
						result, err = wsserver.ListSCCodeMatch(context.Background(), structures.WS_ListSCCodeMatch_Params{Match: strings.Join(line_parts[1:], " "), IncludeCode: true}, vi)
					}
					if err != nil {
						logger.Errorf("%v", err)
					}

					for _, v := range result.Results {
						logger.Printf("SCID: %v, Owner: %v", v.SCID, v.Owner)
						logger.Printf("SC Code: %v", v.Code)
					}
					if len(result.Results) > 0 {
						logger.Printf("SCID Matches returned - %v", len(result.Results))
					}
				}
			} else {
				logger.Printf("listsc_codematch needs some string argment to match against")
			}
		case command == "listsc_variables":
			switch len(line_parts) {
			case 2:
				i := 0
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					var owner string
					switch vi.DBType {
					case "gravdb":
						owner = vi.GravDBBackend.GetOwner(line_parts[1])
					case "boltdb":
						owner = vi.BBSBackend.GetOwner(line_parts[1])
					}
					vars, _, _, err := vi.RPC.GetSCVariables(line_parts[1], vi.ChainHeight, nil, nil, nil, false)
					if err != nil {
						logger.Errorf("%v", err)
					}

					if len(vars) > 0 {
						logger.Printf("SCID: %v ; Owner: %v", line_parts[1], owner)
						for _, vvar := range vars {
							switch vvar.Key.(type) {
							case string:
								if vvar.Key.(string) == "C" {
									continue
								}

								logger.Printf("Key: %v ; Value: %v", vvar.Key, vvar.Value)
							default:
								logger.Printf("Key: %v ; Value: %v", vvar.Key, vvar.Value)
							}
						}
						i++
						break
					} else {
						continue
					}
				}

				if i == 0 {
					logger.Printf("SCID '%s' code was unable to be retrieved. Is it installed?", line_parts[1])
				}
			case 3:
				if s, err := strconv.Atoi(line_parts[2]); err == nil {
					i := 0
					for ki, vi := range g.Indexers {
						logger.Printf("- Indexer '%v'", ki)
						var owner string
						switch vi.DBType {
						case "gravdb":
							owner = vi.GravDBBackend.GetOwner(line_parts[1])
						case "boltdb":
							owner = vi.BBSBackend.GetOwner(line_parts[1])
						}
						vars, _, _, err := vi.RPC.GetSCVariables(line_parts[1], int64(s), nil, nil, nil, false)
						if err != nil {
							logger.Errorf("%v", err)
						}

						if len(vars) > 0 {
							logger.Printf("SCID: %v ; Owner: %v", line_parts[1], owner)
							for _, vvar := range vars {
								if vvar.Key.(string) == "C" {
									continue
								}
								logger.Printf("Key: %v ; Value: %v", vvar.Key, vvar.Value)
							}
							i++
							break
						} else {
							continue
						}
					}

					if i == 0 {
						logger.Printf("SCID '%s' variables were unable to be retrieved at height '%v'. Was it installed?", line_parts[1], int64(s))
					}
				} else {
					logger.Errorf("Could not parse '%v' into an int for height", line_parts[2])
				}

			default:
				logger.Printf("listsc_variables needs one value: single scid")
			}
		case command == "listsc_byheight":
			// Split up line_parts and identify any common language filtering
			filt_line_parts := indexer.SplitLineParts(line_parts, "|")

			if len(line_parts) == 1 || line_parts[1] == "|" {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					var scinstalls []*structures.SCTXParse
					var sclist map[string]string
					switch vi.DBType {
					case "gravdb":
						sclist = vi.GravDBBackend.GetAllOwnersAndSCIDs()
					case "boltdb":
						sclist = vi.BBSBackend.GetAllOwnersAndSCIDs()
					}
					for k, _ := range sclist {
						var invokedetails []*structures.SCTXParse
						switch vi.DBType {
						case "gravdb":
							invokedetails = vi.GravDBBackend.GetAllSCIDInvokeDetails(k)
						case "boltdb":
							invokedetails = vi.BBSBackend.GetAllSCIDInvokeDetails(k)
						}
						i := 0
						for _, v := range invokedetails {
							sc_action := fmt.Sprintf("%v", v.Sc_args.Value("SC_ACTION", "U"))
							if sc_action == "1" {
								i++
								scinstalls = append(scinstalls, v)
							}
						}

						if i == 0 {
							logger.Debugf("No sc_action of '1' for %v", k)
						}
					}

					if len(scinstalls) > 0 {
						// Sort heights so most recent is index 0 [if preferred reverse, just swap > with <]
						sort.SliceStable(scinstalls, func(i, j int) bool {
							return scinstalls[i].Height < scinstalls[j].Height
						})

						// Filter line inputs (if applicable) and return a trimmed list to print out to cli
						var filteredResults []*structures.SCTXParse
						if len(filt_line_parts) > 1 {
							for i := range filt_line_parts {
								if i == 0 {
									filteredResults = vi.PipeFilter(filt_line_parts[i], scinstalls)
								} else {
									filteredResults = vi.PipeFilter(filt_line_parts[i], filteredResults)
								}

							}
						} else {
							filteredResults = vi.PipeFilter(filt_line_parts[0], scinstalls)
						}

						for _, invoke := range filteredResults {
							logger.Printf("SCID: %v ; Owner: %v ; DeployHeight: %v", invoke.Scid, invoke.Sender, invoke.Height)
						}

						logger.Printf("Total SCs installed: %v", len(filteredResults)+len(structures.Hardcoded_SCIDS))
					}
				}
			} else if len(line_parts) >= 2 {
				if sh, err := strconv.Atoi(line_parts[1]); err == nil {
					for ki, vi := range g.Indexers {
						logger.Printf("- Indexer '%v'", ki)
						var scinstalls []*structures.SCTXParse
						var sclist map[string]string
						switch vi.DBType {
						case "gravdb":
							sclist = vi.GravDBBackend.GetAllOwnersAndSCIDs()
						case "boltdb":
							sclist = vi.BBSBackend.GetAllOwnersAndSCIDs()
						}
						for k, _ := range sclist {
							var invokedetails []*structures.SCTXParse
							switch vi.DBType {
							case "gravdb":
								invokedetails = vi.GravDBBackend.GetAllSCIDInvokeDetails(k)
							case "boltdb":
								invokedetails = vi.BBSBackend.GetAllSCIDInvokeDetails(k)
							}
							i := 0
							for _, v := range invokedetails {
								sc_action := fmt.Sprintf("%v", v.Sc_args.Value("SC_ACTION", "U"))
								if sc_action == "1" {
									i++
									scinstalls = append(scinstalls, v)
								}
							}

							if i == 0 {
								logger.Debugf("No sc_action of '1' for %v", k)
							}
						}

						if len(scinstalls) > 0 {
							// Sort heights so most recent is index 0 [if preferred reverse, just swap > with <]
							sort.SliceStable(scinstalls, func(i, j int) bool {
								return scinstalls[i].Height < scinstalls[j].Height
							})

							l := 0

							// Filter line inputs (if applicable) and return a trimmed list to print out to cli
							var filteredResults []*structures.SCTXParse
							if len(filt_line_parts) > 1 {
								for i := range filt_line_parts {
									if i == 0 {
										filteredResults = vi.PipeFilter(filt_line_parts[i], scinstalls)
									} else {
										filteredResults = vi.PipeFilter(filt_line_parts[i], filteredResults)
									}

								}
							} else {
								filteredResults = vi.PipeFilter(filt_line_parts[0], scinstalls)
							}

							for _, invoke := range filteredResults {
								if invoke.Height <= int64(sh) {
									logger.Printf("SCID: %v ; Owner: %v ; DeployHeight: %v", invoke.Scid, invoke.Sender, invoke.Height)
									l++
								}
							}

							logger.Printf("Total SCs installed: %v", l+len(structures.Hardcoded_SCIDS))
						}
					}
				} else {
					logger.Errorf("Could not parse '%v' into an int for height", line_parts[1])
				}
			} else {
				logger.Printf("listsc_byheight needs either no arguments or a single height argument")
			}
		case command == "listsc_balances":
			if len(line_parts) == 1 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					var sclist map[string]string
					switch vi.DBType {
					case "gravdb":
						sclist = vi.GravDBBackend.GetAllOwnersAndSCIDs()
					case "boltdb":
						sclist = vi.BBSBackend.GetAllOwnersAndSCIDs()
					}
					var count int64
					for k, _ := range sclist {
						_, _, cbal, _ := vi.RPC.GetSCVariables(k, vi.ChainHeight, nil, nil, nil, false)
						var pc int
						for kb, vb := range cbal {
							if vb > 0 {
								if pc == 0 {
									logger.Printf("%v:", k)
								}
								if kb == "0000000000000000000000000000000000000000000000000000000000000000" {
									logger.Printf("_DERO: %v\n", vb)
								} else {
									logger.Printf("_Asset: %v:%v\n", kb, vb)
								}
								pc++
							}
						}
						count++
					}

					if count == 0 {
						logger.Printf("No SCIDs installed matching %v", line_parts[1])
					}
				}
			} else if len(line_parts) == 2 && len(line_parts[1]) == 64 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					var sclist map[string]string
					switch vi.DBType {
					case "gravdb":
						sclist = vi.GravDBBackend.GetAllOwnersAndSCIDs()
					case "boltdb":
						sclist = vi.BBSBackend.GetAllOwnersAndSCIDs()
					}
					var count int64
					for k, _ := range sclist {
						if k != line_parts[1] {
							continue
						}
						_, _, cbal, _ := vi.RPC.GetSCVariables(k, vi.ChainHeight, nil, nil, nil, false)
						var pc int
						for kb, vb := range cbal {
							if vb > 0 {
								if pc == 0 {
									logger.Printf("%v:\n", k)
								}
								if kb == "0000000000000000000000000000000000000000000000000000000000000000" {
									logger.Printf("_DERO: %v\n", vb)
								} else {
									logger.Printf("_Asset: %v:%v\n", kb, vb)
								}
								pc++
							}
						}
						count++
					}

					if count == 0 {
						logger.Printf("No SCIDs installed matching %v", line_parts[1])
					}
				}
			} else {
				logger.Printf("listsc_byscid needs a single scid or no SCIDs as argument")
			}
		case command == "listscinvoke_byscid":
			// Split up line_parts and identify any common language filtering
			filt_line_parts := indexer.SplitLineParts(line_parts, "|")

			if len(line_parts) >= 2 && len(line_parts[1]) == 64 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					var sclist map[string]string
					switch vi.DBType {
					case "gravdb":
						sclist = vi.GravDBBackend.GetAllOwnersAndSCIDs()
					case "boltdb":
						sclist = vi.BBSBackend.GetAllOwnersAndSCIDs()
					}
					var count int64
					for k, v := range sclist {
						if k == line_parts[1] {
							logger.Printf("SCID: %v ; Owner: %v", k, v)
							var invokedetails []*structures.SCTXParse
							switch vi.DBType {
							case "gravdb":
								invokedetails = vi.GravDBBackend.GetAllSCIDInvokeDetails(k)
							case "boltdb":
								invokedetails = vi.BBSBackend.GetAllSCIDInvokeDetails(k)
							}

							// Filter line inputs (if applicable) and return a trimmed list to print out to cli
							var filteredResults []*structures.SCTXParse
							if len(filt_line_parts) > 1 {
								for i := range filt_line_parts {
									if i == 0 {
										filteredResults = vi.PipeFilter(filt_line_parts[i], invokedetails)
									} else {
										filteredResults = vi.PipeFilter(filt_line_parts[i], filteredResults)
									}

								}
							} else {
								filteredResults = vi.PipeFilter(filt_line_parts[0], invokedetails)
							}

							for _, invoke := range filteredResults {
								logger.Printf("Sender: %v ; topoheight : %v ; args: %v ; burnValue: %v", invoke.Sender, invoke.Height, invoke.Sc_args, invoke.Payloads[0].BurnValue)
							}

							count++
						}
					}

					if count == 0 {
						logger.Printf("No SCIDs installed matching %v", line_parts[1])
					}
				}
			} else {
				logger.Printf("listsc_byscid needs a single scid as argument")
			}
		case command == "listscinvoke_byentrypoint":
			// Split up line_parts and identify any common language filtering
			filt_line_parts := indexer.SplitLineParts(line_parts, "|")

			if len(line_parts) >= 3 && len(line_parts[1]) == 64 && line_parts[2] != "|" {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					var indexbyentry []*structures.SCTXParse
					switch vi.DBType {
					case "gravdb":
						indexbyentry = vi.GravDBBackend.GetAllSCIDInvokeDetailsByEntrypoint(line_parts[1], line_parts[2])
					case "boltdb":
						indexbyentry = vi.BBSBackend.GetAllSCIDInvokeDetailsByEntrypoint(line_parts[1], line_parts[2])
					}
					var count int64

					// Filter line inputs (if applicable) and return a trimmed list to print out to cli
					var filteredResults []*structures.SCTXParse
					if len(filt_line_parts) > 1 {
						for i := range filt_line_parts {
							if i == 0 {
								filteredResults = vi.PipeFilter(filt_line_parts[i], indexbyentry)
							} else {
								filteredResults = vi.PipeFilter(filt_line_parts[i], filteredResults)
							}

						}
					} else {
						filteredResults = vi.PipeFilter(filt_line_parts[0], indexbyentry)
					}

					for _, invoke := range filteredResults {
						logger.Printf("Sender: %v ; topoheight : %v ; args: %v ; burnValue: %v", invoke.Sender, invoke.Height, invoke.Sc_args, invoke.Payloads[0].BurnValue)
						count++
					}

					if count == 0 {
						logger.Printf("No SCID invokes of entrypoint '%v' for %v", line_parts[2], line_parts[1])
					}
				}
			} else {
				logger.Printf("listsc_byscid needs a single scid and entrypoint as argument")
			}
		case command == "listscinvoke_byinitialize":
			// Split up line_parts and identify any common language filtering
			filt_line_parts := indexer.SplitLineParts(line_parts, "|")

			if len(line_parts) == 1 || (len(line_parts) >= 1 && len(filt_line_parts) > 1 && line_parts[1] == "|") { //&& len(line_parts[1]) == 64 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					var sclist map[string]string
					switch vi.DBType {
					case "gravdb":
						sclist = vi.GravDBBackend.GetAllOwnersAndSCIDs()
					case "boltdb":
						sclist = vi.BBSBackend.GetAllOwnersAndSCIDs()
					}
					var count, count2 int64
					for k, _ := range sclist {
						var indexbyentry []*structures.SCTXParse
						switch vi.DBType {
						case "gravdb":
							indexbyentry = vi.GravDBBackend.GetAllSCIDInvokeDetailsByEntrypoint(k, "Initialize")
						case "boltdb":
							indexbyentry = vi.BBSBackend.GetAllSCIDInvokeDetailsByEntrypoint(k, "Initialize")
						}

						// Filter line inputs (if applicable) and return a trimmed list to print out to cli
						var filteredResults []*structures.SCTXParse
						if len(filt_line_parts) > 1 {
							for i := range filt_line_parts {
								if i == 0 {
									filteredResults = vi.PipeFilter(filt_line_parts[i], indexbyentry)
								} else {
									filteredResults = vi.PipeFilter(filt_line_parts[i], filteredResults)
								}

							}
						} else {
							filteredResults = vi.PipeFilter(filt_line_parts[0], indexbyentry)
						}

						for _, invoke := range filteredResults {
							sc_action := fmt.Sprintf("%v", invoke.Sc_args.Value("SC_ACTION", "U"))
							// If action is 'installsc' we don't need to return results for this
							if sc_action == "1" {
								continue
							}
							logger.Printf("Sender: %v ; topoheight : %v ; args: %v ; burnValue: %v", invoke.Sender, invoke.Height, invoke.Sc_args, invoke.Payloads[0].BurnValue)
							count++
						}

						var indexbyentry2 []*structures.SCTXParse
						switch vi.DBType {
						case "gravdb":
							indexbyentry2 = vi.GravDBBackend.GetAllSCIDInvokeDetailsByEntrypoint(k, "InitializePrivate")
						case "boltdb":
							indexbyentry2 = vi.BBSBackend.GetAllSCIDInvokeDetailsByEntrypoint(k, "InitializePrivate")
						}

						// Filter line inputs (if applicable) and return a trimmed list to print out to cli
						var filteredResults2 []*structures.SCTXParse
						if len(filt_line_parts) > 1 {
							for i := range filt_line_parts {
								if i == 0 {
									filteredResults2 = vi.PipeFilter(filt_line_parts[i], indexbyentry2)
								} else {
									filteredResults2 = vi.PipeFilter(filt_line_parts[i], filteredResults2)
								}

							}
						} else {
							filteredResults2 = vi.PipeFilter(filt_line_parts[0], indexbyentry2)
						}

						for _, invoke := range filteredResults2 {
							sc_action := fmt.Sprintf("%v", invoke.Sc_args.Value("SC_ACTION", "U"))
							// If action is 'installsc' we don't need to return results for this
							if sc_action == "1" {
								continue
							}
							logger.Printf("Sender: %v ; topoheight : %v ; args: %v ; burnValue: %v", invoke.Sender, invoke.Height, invoke.Sc_args, invoke.Payloads[0].BurnValue)
							count2++
						}
					}

					if count == 0 && count2 == 0 {
						logger.Printf("No SCIDs with initialize called.")
					}
				}
			} else if len(line_parts) == 2 && len(line_parts[1]) == 64 || (len(filt_line_parts) > 1 && len(line_parts) >= 2 && len(line_parts[1]) == 64 && line_parts[2] == "|") {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					var sclist map[string]string
					switch vi.DBType {
					case "gravdb":
						sclist = vi.GravDBBackend.GetAllOwnersAndSCIDs()
					case "boltdb":
						sclist = vi.BBSBackend.GetAllOwnersAndSCIDs()
					}
					var count, count2 int64
					for k, _ := range sclist {
						if k != line_parts[1] {
							continue
						}
						var indexbyentry []*structures.SCTXParse
						switch vi.DBType {
						case "gravdb":
							indexbyentry = vi.GravDBBackend.GetAllSCIDInvokeDetailsByEntrypoint(k, "Initialize")
						case "boltdb":
							indexbyentry = vi.BBSBackend.GetAllSCIDInvokeDetailsByEntrypoint(k, "Initialize")
						}

						// Filter line inputs (if applicable) and return a trimmed list to print out to cli
						var filteredResults []*structures.SCTXParse
						if len(filt_line_parts) > 1 {
							for i := range filt_line_parts {
								if i == 0 {
									filteredResults = vi.PipeFilter(filt_line_parts[i], indexbyentry)
								} else {
									filteredResults = vi.PipeFilter(filt_line_parts[i], filteredResults)
								}

							}
						} else {
							filteredResults = vi.PipeFilter(filt_line_parts[0], indexbyentry)
						}

						for _, invoke := range filteredResults {
							sc_action := fmt.Sprintf("%v", invoke.Sc_args.Value("SC_ACTION", "U"))
							// If action is 'installsc' we don't need to return results for this
							if sc_action == "1" {
								continue
							}
							logger.Printf("Sender: %v ; topoheight : %v ; args: %v ; burnValue: %v", invoke.Sender, invoke.Height, invoke.Sc_args, invoke.Payloads[0].BurnValue)
							count++
						}

						var indexbyentry2 []*structures.SCTXParse
						switch vi.DBType {
						case "gravdb":
							indexbyentry2 = vi.GravDBBackend.GetAllSCIDInvokeDetailsByEntrypoint(k, "InitializePrivate")
						case "boltdb":
							indexbyentry2 = vi.BBSBackend.GetAllSCIDInvokeDetailsByEntrypoint(k, "InitializePrivate")
						}

						// Filter line inputs (if applicable) and return a trimmed list to print out to cli
						var filteredResults2 []*structures.SCTXParse
						if len(filt_line_parts) > 1 {
							for i := range filt_line_parts {
								if i == 0 {
									filteredResults2 = vi.PipeFilter(filt_line_parts[i], indexbyentry2)
								} else {
									filteredResults2 = vi.PipeFilter(filt_line_parts[i], filteredResults2)
								}

							}
						} else {
							filteredResults2 = vi.PipeFilter(filt_line_parts[0], indexbyentry2)
						}

						for _, invoke := range filteredResults2 {
							sc_action := fmt.Sprintf("%v", invoke.Sc_args.Value("SC_ACTION", "U"))
							// If action is 'installsc' we don't need to return results for this
							if sc_action == "1" {
								continue
							}
							logger.Printf("Sender: %v ; topoheight : %v ; args: %v ; burnValue: %v", invoke.Sender, invoke.Height, invoke.Sc_args, invoke.Payloads[0].BurnValue)
							count2++
						}
					}

					if count == 0 && count2 == 0 {
						logger.Printf("No SCIDs with initialize called.")
					}
				}
			} else {
				logger.Printf("listsc_byscid needs a single scid and entrypoint as argument")
			}
		case command == "listscinvoke_bysigner":
			{
				// Split up line_parts and identify any common language filtering
				filt_line_parts := indexer.SplitLineParts(line_parts, "|")

				if len(line_parts) >= 2 {
					for ki, vi := range g.Indexers {
						logger.Printf("- Indexer '%v'", ki)
						var sclist map[string]string
						switch vi.DBType {
						case "gravdb":
							sclist = vi.GravDBBackend.GetAllOwnersAndSCIDs()
						case "boltdb":
							sclist = vi.BBSBackend.GetAllOwnersAndSCIDs()
						}
						for k, v := range sclist {
							if len(line_parts) > 2 && len(line_parts[2]) == 64 {
								if k != line_parts[2] {
									continue
								}
							}
							var indexbypartialsigner []*structures.SCTXParse
							switch vi.DBType {
							case "gravdb":
								indexbypartialsigner = vi.GravDBBackend.GetAllSCIDInvokeDetailsBySigner(k, line_parts[1])
							case "boltdb":
								indexbypartialsigner = vi.BBSBackend.GetAllSCIDInvokeDetailsBySigner(k, line_parts[1])
							}
							if len(indexbypartialsigner) > 0 {
								// Filter line inputs (if applicable) and return a trimmed list to print out to cli
								var filteredResults []*structures.SCTXParse
								if len(filt_line_parts) > 1 {
									for i := range filt_line_parts {
										if i == 0 {
											filteredResults = vi.PipeFilter(filt_line_parts[i], indexbypartialsigner)
										} else {
											filteredResults = vi.PipeFilter(filt_line_parts[i], filteredResults)
										}

									}
								} else {
									filteredResults = vi.PipeFilter(filt_line_parts[0], indexbypartialsigner)
								}

								if len(filteredResults) > 0 {
									logger.Printf("SCID: %v ; Owner: %v", k, v)

									for _, invoke := range filteredResults {
										logger.Printf("Sender: %v ; topoheight : %v ; args: %v ; burnValue: %v", invoke.Sender, invoke.Height, invoke.Sc_args, invoke.Payloads[0].BurnValue)
									}
								}
							}
						}
					}
				} else {
					logger.Printf("listscinvoke_bysigner needs a single scid and partialsigner string as argument")
				}
			}
		case command == "listscidkey_byvaluestored":
			if len(line_parts) >= 3 && len(line_parts[1]) == 64 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					var sclist map[string]string
					switch vi.DBType {
					case "gravdb":
						sclist = vi.GravDBBackend.GetAllOwnersAndSCIDs()
					case "boltdb":
						sclist = vi.BBSBackend.GetAllOwnersAndSCIDs()
					}
					var count int64
					for k, _ := range sclist {
						if k == line_parts[1] {
							var keysstringbyvalue []string
							var keysuint64byvalue []uint64

							switch vi.DBType {
							case "gravdb":
								intCheck, err := strconv.Atoi(line_parts[2])
								if err != nil {
									keysstringbyvalue, keysuint64byvalue = vi.GravDBBackend.GetSCIDKeysByValue(k, strings.Join(line_parts[2:], " "), vi.ChainHeight, true)
								} else {
									keysstringbyvalue, keysuint64byvalue = vi.GravDBBackend.GetSCIDKeysByValue(k, uint64(intCheck), vi.ChainHeight, true)
								}
							case "boltdb":
								intCheck, err := strconv.Atoi(line_parts[2])
								if err != nil {
									keysstringbyvalue, keysuint64byvalue = vi.BBSBackend.GetSCIDKeysByValue(k, strings.Join(line_parts[2:], " "), vi.ChainHeight, true)
								} else {
									keysstringbyvalue, keysuint64byvalue = vi.BBSBackend.GetSCIDKeysByValue(k, uint64(intCheck), vi.ChainHeight, true)
								}
							}

							for _, skey := range keysstringbyvalue {
								logger.Printf("%v", skey)
							}
							for _, ukey := range keysuint64byvalue {
								logger.Printf("%v", ukey)
							}
							count++
						}
					}

					if count == 0 {
						logger.Printf("No SCIDs installed matching %v", line_parts[1])
					}
				}
			} else {
				logger.Printf("listscidkey_byvalue needs two values: single scid and value to match as arguments")
			}
		case command == "listscidkey_byvaluelive":
			if len(line_parts) >= 3 && len(line_parts[1]) == 64 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					var variables []*structures.SCIDVariable
					var keysstringbyvalue []string
					var keysuint64byvalue []uint64

					intCheck, err := strconv.Atoi(line_parts[2])
					if err != nil {
						keysstringbyvalue, keysuint64byvalue, _ = vi.GetSCIDKeysByValue(variables, line_parts[1], strings.Join(line_parts[2:], " "), vi.ChainHeight)
					} else {
						keysstringbyvalue, keysuint64byvalue, _ = vi.GetSCIDKeysByValue(variables, line_parts[1], uint64(intCheck), vi.ChainHeight)
					}

					for _, skey := range keysstringbyvalue {
						logger.Printf("%v", skey)
					}
					for _, ukey := range keysuint64byvalue {
						logger.Printf("%v", ukey)
					}
					// TODO: We can break, it's using the daemon to return the results. TODO Could pass mainnet/testnet and check indexers for different endpoints on different chains etc. but may not be needed
					break
				}
			} else {
				logger.Printf("listscidkey_byvalue needs two values: single scid and value to match as arguments")
			}
		case command == "listscidvalue_bykeystored":
			if len(line_parts) >= 3 && len(line_parts[1]) == 64 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					var sclist map[string]string
					switch vi.DBType {
					case "gravdb":
						sclist = vi.GravDBBackend.GetAllOwnersAndSCIDs()
					case "boltdb":
						sclist = vi.BBSBackend.GetAllOwnersAndSCIDs()
					}
					var count int64
					for k, _ := range sclist {
						if k == line_parts[1] {
							var valuesstringbykey []string
							var valuesuint64bykey []uint64

							switch vi.DBType {
							case "gravdb":
								intCheck, err := strconv.Atoi(line_parts[2])
								if err != nil {
									valuesstringbykey, valuesuint64bykey = vi.GravDBBackend.GetSCIDValuesByKey(k, strings.Join(line_parts[2:], " "), vi.ChainHeight, true)
								} else {
									valuesstringbykey, valuesuint64bykey = vi.GravDBBackend.GetSCIDValuesByKey(k, uint64(intCheck), vi.ChainHeight, true)
								}
							case "boltdb":
								intCheck, err := strconv.Atoi(line_parts[2])
								if err != nil {
									valuesstringbykey, valuesuint64bykey = vi.BBSBackend.GetSCIDValuesByKey(k, strings.Join(line_parts[2:], " "), vi.ChainHeight, true)
								} else {
									valuesstringbykey, valuesuint64bykey = vi.BBSBackend.GetSCIDValuesByKey(k, uint64(intCheck), vi.ChainHeight, true)
								}
							}

							for _, sval := range valuesstringbykey {
								logger.Printf("%v", sval)

								/*
									var h crypto.Hash
									copy(h[:], []byte(sval)[:])
									logger.Printf("%v", h.String())
									logger.Printf("%v", []byte(sval))
								*/
							}
							for _, uval := range valuesuint64bykey {
								logger.Printf("%v", uval)
							}
							count++
						}
					}

					if count == 0 {
						logger.Printf("No SCIDs installed matching %v", line_parts[1])
					}
				}
			} else {
				logger.Printf("listscidkey_byvalue needs two values: single scid and value to match as arguments")
			}
		case command == "listscidvalue_bykeylive":
			if len(line_parts) >= 3 && len(line_parts[1]) == 64 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					var variables []*structures.SCIDVariable
					var valuesstringbykey []string
					var valuesuint64bykey []uint64

					intCheck, err := strconv.Atoi(line_parts[2])
					if err != nil {
						valuesstringbykey, valuesuint64bykey, _ = vi.GetSCIDValuesByKey(variables, line_parts[1], strings.Join(line_parts[2:], " "), vi.ChainHeight)
					} else {
						valuesstringbykey, valuesuint64bykey, _ = vi.GetSCIDValuesByKey(variables, line_parts[1], uint64(intCheck), vi.ChainHeight)
					}
					for _, sval := range valuesstringbykey {
						logger.Printf("%v", sval)
					}
					for _, uval := range valuesuint64bykey {
						logger.Printf("%v", uval)
					}

					// TODO: We can break, it's using the daemon to return the results. TODO Could pass mainnet/testnet and check indexers for different endpoints on different chains etc. but may not be needed
					break
				}
			} else {
				logger.Printf("listscidkey_byvalue needs two values: single scid and value to match as arguments")
			}
		case command == "validatesc":
			if len(line_parts) == 2 && len(line_parts[1]) == 64 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					variables, code, _, _ := vi.RPC.GetSCVariables(line_parts[1], vi.ChainHeight, nil, nil, nil, false)
					keysstring, _, _ := vi.GetSCIDValuesByKey(variables, line_parts[1], "signature", vi.ChainHeight)

					// Check  if keysstring is nil or not to avoid any sort of panics
					var sigstr string
					if len(keysstring) > 0 {
						sigstr = keysstring[0]
					}
					validated, signer, err := vi.ValidateSCSignature(code, sigstr)

					if err != nil {
						logger.Printf("[validatesc] ERR - %v", err)
					} else {
						logger.Printf("Validated: %v", validated)
						logger.Printf("Signer: %v", signer)
					}
					// TODO: We can break, it's using the daemon to return the results. TODO Could pass mainnet/testnet and check indexers for different endpoints on different chains etc. but may not be needed
					break
				}
			}
		case command == "addscid_toindex":
			// TODO: Perhaps add indexer id to a param so you can add it to specific search_filter/indexer. Supported by a 'status' (tbd) command which returns details of each indexer
			if len(line_parts) == 2 && len(line_parts[1]) == 64 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					scidstoadd := make(map[string]*structures.FastSyncImport)
					scidstoadd[line_parts[1]] = &structures.FastSyncImport{}
					err = vi.AddSCIDToIndex(scidstoadd, false, true)
					if err != nil {
						logger.Printf("Err - %v", err)
					}
				}
			} else {
				logger.Printf("addscid_toindex needs 1 values: single scid to match as arguments")
			}

		case command == "inspecttxns_byheight":
			// TODO: Perhaps add indexer id to a param so you can add it to specific search_filter/indexer. Supported by a 'status' (tbd) command which returns details of each indexer
			if len(line_parts) == 2 && len(line_parts[1]) == 64 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)

					var blTxns *structures.BlockTxns
					blTxns.Topoheight = 1352506
					var h crypto.Hash
					copy(h[:crypto.HashLength], []byte(line_parts[1])[:])
					blTxns.Tx_hashes = append(blTxns.Tx_hashes, h)
					vi.IndexTxn(blTxns, true)
					if err != nil {
						logger.Printf("Err - %v", err)
					}
				}
			} else {
				logger.Printf("addscid_toindex needs 1 values: single scid to match as arguments")
			}

		case command == "getscidlist_byaddr":
			// TODO: Perhaps add indexer id to a param so you can add it to specific search_filter/indexer. Supported by a 'status' (tbd) command which returns details of each indexer
			if len(line_parts) == 2 && len(line_parts[1]) == 66 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					var scidinteracts []string
					switch vi.DBType {
					case "gravdb":
						scidinteracts = vi.GravDBBackend.GetSCIDInteractionByAddr(line_parts[1])
					case "boltdb":
						scidinteracts = vi.BBSBackend.GetSCIDInteractionByAddr(line_parts[1])
					}
					for _, v := range scidinteracts {
						logger.Printf("%v", v)
					}
				}
			} else {
				logger.Printf("getscidlist_byaddr needs 1 values: single address to match as arguments")
			}
		case command == "countinvoke_burnvalue":
			// Takes same inputs and filters as listscinvoke_byscid
			// Example: countinvoke_burnvalue 8289c6109f41cbe1f6d5f27a419db537bf3bf30a25eff285241a36e1ae3e48a4
			if len(line_parts) >= 2 && len(line_parts[1]) == 64 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					var sclist map[string]string
					switch vi.DBType {
					case "gravdb":
						sclist = vi.GravDBBackend.GetAllOwnersAndSCIDs()
					case "boltdb":
						sclist = vi.BBSBackend.GetAllOwnersAndSCIDs()
					}
					var count int64
					for k, v := range sclist {
						if k == line_parts[1] {
							logger.Printf("SCID: %v ; Owner: %v", k, v)
							var invokedetails []*structures.SCTXParse
							switch vi.DBType {
							case "gravdb":
								invokedetails = vi.GravDBBackend.GetAllSCIDInvokeDetails(k)
							case "boltdb":
								invokedetails = vi.BBSBackend.GetAllSCIDInvokeDetails(k)
							}

							// Filter line inputs (if applicable) and return a trimmed list to print out to cli
							filteredResults := vi.PipeFilter(line_parts, invokedetails)

							bvcalc := make(map[string]uint64)

							for _, invoke := range filteredResults {
								bv := invoke.Payloads[0].BurnValue
								if !(bv > 1) && !invoke.Payloads[0].SCID.IsZero() {
									continue
								} else {
									bvcalc[invoke.Payloads[0].SCID.String()] += bv
								}
							}

							for k, v := range bvcalc {
								logger.Printf("SCID '%s' - %s", k, globals.FormatMoney(v))
							}

							count++
						}
					}

					if count == 0 {
						logger.Printf("No SCIDs installed matching %v", line_parts[1])
					}
				}
			} else {
				logger.Printf("listsc_byscid needs a single scid as argument")
			}
		case command == "diffscid_code":
			// Could be re-used/modified to be a diff on any string-based store

			// TODO: Break down diff by the Function to which differences reside within
			if len(line_parts) == 4 && len(line_parts[1]) == 64 {
				intStart, err := strconv.Atoi(line_parts[2])
				if err != nil {
					logger.Printf("Start Height argument is not a proper int")
				} else {
					intEnd, err2 := strconv.Atoi(line_parts[3])
					if err2 != nil {
						logger.Printf("End Height argument is not a proper int")
					} else {
						for ki, vi := range g.Indexers {
							logger.Printf(" - Indexer '%v'", ki)

							var valuesstringbykey []string
							var beginCode []string
							var endCode []string

							switch vi.DBType {
							case "gravdb":
								owner := vi.GravDBBackend.GetOwner(line_parts[1])
								if owner != "" {
									valuesstringbykey, _ = vi.GravDBBackend.GetSCIDValuesByKey(line_parts[1], "C", int64(intStart), false)
									if len(valuesstringbykey) > 0 {
										beginCode = strings.Split(strings.ReplaceAll(valuesstringbykey[0], "\r\n", "\n"), "\n")
									}

									valuesstringbykey, _ = vi.GravDBBackend.GetSCIDValuesByKey(line_parts[1], "C", int64(intEnd), false)
									if len(valuesstringbykey) > 0 {
										endCode = strings.Split(strings.ReplaceAll(valuesstringbykey[0], "\r\n", "\n"), "\n")
									}
								}
							case "boltdb":
								owner := vi.BBSBackend.GetOwner(line_parts[1])
								if owner != "" {
									valuesstringbykey, _ = vi.BBSBackend.GetSCIDValuesByKey(line_parts[1], "C", int64(intStart), false)
									if len(valuesstringbykey) > 0 {
										beginCode = strings.Split(strings.ReplaceAll(valuesstringbykey[0], "\r\n", "\n"), "\n")
									}

									valuesstringbykey, _ = vi.BBSBackend.GetSCIDValuesByKey(line_parts[1], "C", int64(intEnd), false)
									if len(valuesstringbykey) > 0 {
										endCode = strings.Split(strings.ReplaceAll(valuesstringbykey[0], "\r\n", "\n"), "\n")
									}
								}
							}

							if len(beginCode) != 0 && len(endCode) != 0 {

								before := difference(beginCode, endCode)
								after := difference(endCode, beginCode)
								if len(before) > 0 || len(after) > 0 {
									logger.Printf("Code from height '%d' is different than height '%d'", intStart, intEnd)
									if len(before) == len(after) {
										for i := 0; i < len(after); i++ {
											logger.Printf("Before: %s ; After: %s", before[i], after[i])
										}
									} else {
										logger.Printf("Slices before/after compare are different lengths. Printing indepentently:")
										logger.Printf("Before (what doesn't exist now): %v", before)
										logger.Printf("After (what now exists): %v", after)
									}
								} else {
									logger.Printf("Code from height '%d' is the same at height '%d'", intStart, intEnd)
								}
							}
						}
					}
				}
			} else {
				logger.Printf("diffscid_code needs 3 values: scid, start height and end height")
			}
		case command == "list_randominteractionaddrs":
			if len(line_parts) == 2 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					intCheck, err := strconv.ParseInt(line_parts[1], 10, 64)
					if err == nil {
						rAddr, err := vi.GetRandInteractionAddresses(intCheck, &structures.InteractionAddrs_Params{Integrator: true, Installs: true, Invokes: true})

						for _, v := range rAddr {
							logger.Printf("%s", v)
						}

						if err != nil {
							logger.Errorf("%v", err)
						}
					} else {
						logger.Printf("list_randominteractionaddrs needs 1 value: count of addresses to return. Supplied count is not an int.")
					}
				}
			} else {
				logger.Printf("list_randominteractionaddrs needs 1 value: count of addresses to return")
			}
		case command == "pop":
			switch len(line_parts) {
			case 1:
				// Change back 1 height
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					if int64(1) > vi.LastIndexedHeight {
						vi.LastIndexedHeight = 1
					} else {
						vi.LastIndexedHeight = vi.LastIndexedHeight - int64(1)
					}
				}
			case 2:
				pop_count := 0
				if s, err := strconv.Atoi(line_parts[1]); err == nil {
					pop_count = s

					// Change back pop_count height
					for ki, vi := range g.Indexers {
						logger.Printf("- Indexer '%v'", ki)
						if int64(pop_count) > vi.LastIndexedHeight {
							vi.LastIndexedHeight = 1
						} else {
							vi.LastIndexedHeight = vi.LastIndexedHeight - int64(pop_count)
						}
					}
				} else {
					logger.Printf("POP needs argument n to pop this many blocks from the top")
				}

			default:
				logger.Printf("POP needs argument n to pop this many blocks from the top")
			}
		case line == "status":
			for ki, vi := range g.Indexers {
				logger.Printf("- Indexer '%v' - Generating status metrics...", ki)
				var regTxCount, burnTxCount, normTxCount, gnomon_count int64
				var interCounts *structures.IATrack
				validatedSCIDs := make(map[string]string)
				addrList := make(map[string]*structures.IATrack)

				switch vi.DBType {
				case "gravdb":
					validatedSCIDs = vi.GravDBBackend.GetAllOwnersAndSCIDs()
					gnomon_count = int64(len(validatedSCIDs))

					regTxCount = vi.GravDBBackend.GetTxCount("registration")
					burnTxCount = vi.GravDBBackend.GetTxCount("burn")
					normTxCount = vi.GravDBBackend.GetTxCount("normal")
				case "boltdb":
					validatedSCIDs = vi.BBSBackend.GetAllOwnersAndSCIDs()
					gnomon_count = int64(len(validatedSCIDs))

					regTxCount = vi.BBSBackend.GetTxCount("registration")
					burnTxCount = vi.BBSBackend.GetTxCount("burn")
					normTxCount = vi.BBSBackend.GetTxCount("normal")
				}

				addrList, interCounts = vi.GetInteractionAddresses(&structures.InteractionAddrs_Params{Integrator: true, Installs: true, Invokes: true})

				logger.Printf("GNOMON [%d/%d] R:%d >>", vi.LastIndexedHeight, vi.ChainHeight, gnomon_count)
				logger.Printf("TXCOUNTS [%d/%d] R:%d B:%d N:%d >>", vi.LastIndexedHeight, vi.ChainHeight, regTxCount, burnTxCount, normTxCount)
				logger.Printf("SCACTIONS [%d/%d] I:%d A:%d >>", vi.LastIndexedHeight, vi.ChainHeight, interCounts.Installs, interCounts.Invokes-interCounts.Installs)
				logger.Printf("INTERADDRS [%d/%d] I:%d S:%d >>", vi.LastIndexedHeight, vi.ChainHeight, interCounts.Integrator, int64(len(addrList))-interCounts.Integrator)
				if len(vi.SearchFilter) == 0 {
					logger.Printf("SEARCHFILTER(S) [%d/%d] >> %s", vi.LastIndexedHeight, vi.ChainHeight, "ALL SCs")
				} else {
					logger.Printf("SEARCHFILTER(S) [%d/%d] >> %s", vi.LastIndexedHeight, vi.ChainHeight, strings.Join(vi.SearchFilter, ";;;"))
				}
				logger.Printf("STATUS >> %s", vi.Status)
			}
		case line == "gnomonsc":
			logger.Printf("[Mainnet] %s", structures.MAINNET_GNOMON_SCID)
			logger.Printf("[Testnet] %s", structures.TESTNET_GNOMON_SCID)
		case line == "quit":
			logger.Printf("'quit' received, putting gnomes to sleep. This will take ~5sec.")
			g.Close()
			return nil
		case line == "bye":
			logger.Printf("'bye' received, putting gnomes to sleep. This will take ~5sec.")
			g.Close()
			return nil
		case line == "exit":
			logger.Printf("'exit' received, putting gnomes to sleep. This will take ~5sec.")
			g.Close()
			return nil
		default:
			logger.Printf("You said: %v", strconv.Quote(line))
		}
	}
}

func usage(w io.Writer) {
	io.WriteString(w, "commands:\n")
	io.WriteString(w, "\t\033[1mhelp\033[0m\t\tthis help\n")
	io.WriteString(w, "\t\033[1mversion\033[0m\t\tShow gnomon version\n")
	io.WriteString(w, "\t\033[1mlistsc\033[0m\t\tLists all indexed scids that match original search filter and optionally filtered by owner or scid via input. listsc || listsc <owneraddress> || listsc <scid> | ... | grep <stringmatch>\n")
	io.WriteString(w, "\t\033[1mlistsc_hardcoded\033[0m\t\tLists all hardcoded scids\n")
	io.WriteString(w, "\t\033[1mlistsc_code\033[0m\t\tLists SCID code, listsc_code <scid>\n")
	io.WriteString(w, "\t\033[1mlistsc_codematch\033[0m\t\tLists SCIDs that match a given search string, listsc_codematch <Test Search String>\n")
	io.WriteString(w, "\t\033[1mlistsc_variables\033[0m\t\tLists SCID variables at latest height unless optionally defining a height, listsc_variables <scid> <height>\n")
	io.WriteString(w, "\t\033[1mlistsc_byheight\033[0m\tList all indexed scids that match original search filter including height deployed and optionally filter by maxheight, listsc_byheight || listsc_byheight <maxheight> || ... | grep <stringmatch>\n")
	io.WriteString(w, "\t\033[1mlistsc_balances\033[0m\tLists balances of SCIDs that are greater than 0 or of a specific scid if specified, listsc_balances || listsc_balances <scid>\n")
	io.WriteString(w, "\t\033[1mlistscinvoke_byscid\033[0m\tLists a scid/owner pair of a defined scid and any invokes. Optionally limited to a specified minimum height, listscinvoke_byscid <scid> || listscinvoke_byscid <scid> <minheight> || ... | grep <stringmatch>\n")
	io.WriteString(w, "\t\033[1mlistscinvoke_byentrypoint\033[0m\tLists sc invokes by entrypoint, listscinvoke_byentrypoint <scid> <entrypoint> || ... | grep <stringmatch>\n")
	io.WriteString(w, "\t\033[1mlistscinvoke_byinitialize\033[0m\tLists all calls to SCs that attempted to run Initialize() or InitializePrivate() or to a specific SC is defined, listscinvoke_byinitialize || listscinvoke_byinitialize <scid> || ... | grep <stringmatch>\n")
	io.WriteString(w, "\t\033[1mlistscinvoke_bysigner\033[0m\tLists all sc invokes that match a given signer or partial signer address and optionally by scid, listscinvoke_bysigner <signerstring> || listscinvoke_bysigner <signerstring> <scid> || ... | grep <stringmatch>\n")
	io.WriteString(w, "\t\033[1mlistscidkey_byvaluestored\033[0m\tList keys in a SC that match a given value by pulling from gnomon database, listscidkey_byvaluestored <scid> <value>\n")
	io.WriteString(w, "\t\033[1mlistscidkey_byvaluelive\033[0m\tList keys in a SC that match a given value by pulling from daemon, listscidkey_byvaluelive <scid> <value>\n")
	io.WriteString(w, "\t\033[1mlistscidvalue_bykeystored\033[0m\tList keys in a SC that match a given value by pulling from gnomon database, listscidvalue_bykeystored <scid> <key>\n")
	io.WriteString(w, "\t\033[1mlistscidvalue_bykeylive\033[0m\tList keys in a SC that match a given value by pulling from daemon, listscidvalue_bykeylive <scid> <key>\n")
	io.WriteString(w, "\t\033[1mvalidatesc\033[0m\tValidates a SC looking for a 'signature' k/v pair containing DERO signature validating the code matches the signature, validatesc <scid>\n")
	io.WriteString(w, "\t\033[1maddscid_toindex\033[0m\tAdd a SCID to index list/validation filter manually, addscid_toindex <scid>\n")
	//io.WriteString(w, "\t\033[1mindex_txn\033[0m\tIndex a specific txid (alpha), addscid_toindex <scid>\n")
	io.WriteString(w, "\t\033[1mgetscidlist_byaddr\033[0m\tGets list of scids that addr has interacted with, getscidlist_byaddr <addr>\n")
	io.WriteString(w, "\t\033[1mcountinvoke_burnvalue\033[0m\tLists a scid/owner pair of a defined scid and any invokes then calculates any burnvalue for them. Optionally limited to a specified minimum height or string match filter on args, countinvoke_burnvalue <scid> || countinvoke_burnvalue <scid> <minheight> || ... | grep <stringmatch>\n")
	io.WriteString(w, "\t\033[1mdiffscid_code\033[0m\tRuns a difference for SC code at one height vs another, diffscid_code <scid> <startHeight> <endHeight>\n")
	io.WriteString(w, "\t\033[1mlist_randominteractionaddrs\033[0m\tReturns a defined set length of random interaction addresses, list_randominteractionaddrs <10>\n")
	io.WriteString(w, "\t\033[1mpop\033[0m\tRolls back lastindexheight, pop <100>\n")
	io.WriteString(w, "\t\033[1mstatus\033[0m\t\tShow general information\n")
	io.WriteString(w, "\t\033[1mgnomonsc\033[0m\t\tShow scid of gnomon index scs\n")

	io.WriteString(w, "\t\033[1mbye\033[0m\t\tQuit the daemon\n")
	io.WriteString(w, "\t\033[1mexit\033[0m\t\tQuit the daemon\n")
	io.WriteString(w, "\t\033[1mquit\033[0m\t\tQuit the daemon\n")
}

// difference returns the elements in `a` that aren't in `b`.
func difference(a, b []string) []string {
	mb := make(map[string]struct{}, len(b))
	for _, x := range b {
		mb[x] = struct{}{}
	}
	var diff []string
	for _, x := range a {
		if _, found := mb[x]; !found {
			diff = append(diff, x)
		}
	}
	return diff
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

func (g *GnomonServer) Close() {
	g.Closing = true

	for _, v := range g.Indexers {
		go v.Close()
	}

	time.Sleep(time.Second * 5)

	os.Exit(0)
}
