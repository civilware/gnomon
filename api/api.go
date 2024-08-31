package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"sync/atomic"
	"time"

	store "github.com/civilware/Gnomon/storage"
	"github.com/civilware/Gnomon/structures"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

type ApiServer struct {
	Config        *structures.APIConfig
	Stats         atomic.Value
	StatsIntv     time.Duration
	GravDBBackend *store.GravitonStore
	BBSBackend    *store.BboltStore
	DBType        string
}

// local logger
var logger *logrus.Entry

// Configures a new API server to be used
func NewApiServer(cfg *structures.APIConfig, gravdbbackend *store.GravitonStore, bbsbackend *store.BboltStore, dbtype string) *ApiServer {

	logger = structures.Logger.WithFields(logrus.Fields{})

	return &ApiServer{
		Config:        cfg,
		GravDBBackend: gravdbbackend,
		BBSBackend:    bbsbackend,
		DBType:        dbtype,
	}
}

// Starts the api server
func (apiServer *ApiServer) Start() {

	apiServer.StatsIntv, _ = time.ParseDuration(apiServer.Config.StatsCollectInterval)
	statsTimer := time.NewTimer(apiServer.StatsIntv)
	logger.Printf("[API] Set stats collect interval to %v", apiServer.StatsIntv)

	apiServer.collectStats()

	go func() {
		for {
			select {
			case <-statsTimer.C:
				apiServer.collectStats()
				statsTimer.Reset(apiServer.StatsIntv)
			}
		}
	}()

	// If SSL is configured, due to nature of listenandserve, put HTTP in go routine then call SSL afterwards so they can run in parallel. Otherwise, run http as normal
	if apiServer.Config.SSL {
		go apiServer.listen()
		go apiServer.listenSSL()
		apiServer.getInfoListenSSL()
	} else {
		apiServer.listen()
	}
}

// Sets up the non-SSL API listener
func (apiServer *ApiServer) listen() {
	logger.Printf("[API] Starting API on %v", apiServer.Config.Listen)
	router := mux.NewRouter()
	router.HandleFunc("/api/indexedscs", apiServer.StatsIndex)
	router.HandleFunc("/api/indexbyscid", apiServer.InvokeIndexBySCID)
	router.HandleFunc("/api/scvarsbyheight", apiServer.InvokeSCVarsByHeight)
	router.HandleFunc("/api/invalidscids", apiServer.InvalidSCIDStats)
	router.HandleFunc("/api/scidprivtx", apiServer.NormalTxWithSCID)
	if apiServer.Config.MBLLookup {
		router.HandleFunc("/api/getmbladdrsbyhash", apiServer.MBLLookupByHash)
		router.HandleFunc("/api/getmblcountbyaddr", apiServer.MBLLookupByAddr)
	}
	router.HandleFunc("/api/getinfo", apiServer.GetInfo)
	router.NotFoundHandler = http.HandlerFunc(notFound)
	err := http.ListenAndServe(apiServer.Config.Listen, router)
	if err != nil {
		logger.Fatalf("[API] Failed to start API: %v", err)
	}
}

// Sets up the SSL API listener
func (apiServer *ApiServer) listenSSL() {
	logger.Printf("[API] Starting SSL API on %v", apiServer.Config.SSLListen)
	routerSSL := mux.NewRouter()
	routerSSL.HandleFunc("/api/indexedscs", apiServer.StatsIndex)
	routerSSL.HandleFunc("/api/indexbyscid", apiServer.InvokeIndexBySCID)
	routerSSL.HandleFunc("/api/scvarsbyheight", apiServer.InvokeSCVarsByHeight)
	routerSSL.HandleFunc("/api/invalidscids", apiServer.InvalidSCIDStats)
	routerSSL.HandleFunc("/api/scidprivtx", apiServer.NormalTxWithSCID)
	if apiServer.Config.MBLLookup {
		routerSSL.HandleFunc("/api/getmbladdrsbyhash", apiServer.MBLLookupByHash)
		routerSSL.HandleFunc("/api/getmblcountbyaddr", apiServer.MBLLookupByAddr)
	}
	routerSSL.HandleFunc("/api/getinfo", apiServer.GetInfo)
	routerSSL.NotFoundHandler = http.HandlerFunc(notFound)
	err := http.ListenAndServeTLS(apiServer.Config.SSLListen, apiServer.Config.CertFile, apiServer.Config.KeyFile, routerSSL)
	if err != nil {
		logger.Fatalf("[API] Failed to start SSL API: %v", err)
	}
}

// Sets up a separate getinfo SSL listener. Use cases is for things like benchmark.dero.network and others that may want to consume a https endpoint of derod getinfo or other future command output
func (apiServer *ApiServer) getInfoListenSSL() {
	logger.Printf("[API] Starting GetInfo SSL API on %v", apiServer.Config.GetInfoSSLListen)
	routerSSL := mux.NewRouter()
	routerSSL.HandleFunc("/api/getinfo", apiServer.GetInfo)
	routerSSL.NotFoundHandler = http.HandlerFunc(notFound)
	err := http.ListenAndServeTLS(apiServer.Config.GetInfoSSLListen, apiServer.Config.GetInfoCertFile, apiServer.Config.GetInfoKeyFile, routerSSL)
	if err != nil {
		logger.Fatalf("[API] Failed to start GetInfo SSL API: %v", err)
	}
}

// Default 404 not found response if api entry wasn't caught
func notFound(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusNotFound)
}

// Continuous check on number of validated scs etc. for base stats of service.
func (apiServer *ApiServer) collectStats() {
	switch apiServer.DBType {
	case "gravdb":
		if apiServer.GravDBBackend.Closing {
			return
		}
	case "boltdb":
		if apiServer.BBSBackend.Closing {
			return
		}
	}

	stats := make(map[string]interface{})
	sclist := make(map[string]string)

	// TODO: Removeme
	var scinstalls []*structures.SCTXParse
	switch apiServer.DBType {
	case "gravdb":
		sclist = apiServer.GravDBBackend.GetAllOwnersAndSCIDs()
	case "boltdb":
		sclist = apiServer.BBSBackend.GetAllOwnersAndSCIDs()
	}
	for k, _ := range sclist {
		switch apiServer.DBType {
		case "gravdb":
			if apiServer.GravDBBackend.Closing {
				return
			}
		case "boltdb":
			if apiServer.BBSBackend.Closing {
				return
			}
		}

		var invokedetails []*structures.SCTXParse
		switch apiServer.DBType {
		case "gravdb":
			// Check to see if installsc details are present - implemented in gnomon v2.1.0-alpha.1
			invokedetail := apiServer.GravDBBackend.GetSCIDInstallSCDetails(k)
			if invokedetail != nil {
				invokedetails = append(invokedetails, invokedetail)
			} else {
				invokedetails = apiServer.GravDBBackend.GetAllSCIDInvokeDetails(k)
			}
		case "boltdb":
			// Check to see if installsc details are present - implemented in gnomon v2.1.0-alpha.1
			invokedetail := apiServer.BBSBackend.GetSCIDInstallSCDetails(k)
			if invokedetail != nil {
				invokedetails = append(invokedetails, invokedetail)
			} else {
				invokedetails = apiServer.BBSBackend.GetAllSCIDInvokeDetails(k)
			}
		}
		i := 0
		// Double check for legacy - could be phased out by a version check
		for _, v := range invokedetails {
			sc_action := fmt.Sprintf("%v", v.Sc_args.Value("SC_ACTION", "U"))
			if sc_action == "1" {
				i++
				scinstalls = append(scinstalls, v)
			}
		}
	}

	if len(scinstalls) > 0 {
		// Sort heights so most recent is index 0 [if preferred reverse, just swap > with <]
		sort.SliceStable(scinstalls, func(i, j int) bool {
			return scinstalls[i].Height < scinstalls[j].Height
		})
	}

	var lastQueries []*structures.GnomonSCIDQuery

	for _, v := range scinstalls {
		curr := &structures.GnomonSCIDQuery{Owner: v.Sender, Height: uint64(v.Height), SCID: v.Scid}
		lastQueries = append(lastQueries, curr)
	}

	// Get all scid:owner
	// TODO: Re-add
	//sclist := apiServer.Backend.GetAllOwnersAndSCIDs()
	var regTxCount, burnTxCount, normTxCount int64
	switch apiServer.DBType {
	case "gravdb":
		regTxCount = apiServer.GravDBBackend.GetTxCount("registration")
		burnTxCount = apiServer.GravDBBackend.GetTxCount("burn")
		normTxCount = apiServer.GravDBBackend.GetTxCount("normal")
	case "boltdb":
		regTxCount = apiServer.BBSBackend.GetTxCount("registration")
		burnTxCount = apiServer.BBSBackend.GetTxCount("burn")
		normTxCount = apiServer.BBSBackend.GetTxCount("normal")
	}

	stats["numscs"] = len(sclist)
	stats["indexedscs"] = sclist
	stats["indexdetails"] = lastQueries
	stats["regTxCount"] = regTxCount
	stats["burnTxCount"] = burnTxCount
	stats["normTxCount"] = normTxCount

	apiServer.Stats.Store(stats)
}

func (apiServer *ApiServer) StatsIndex(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	reply := make(map[string]interface{})

	stats := apiServer.getStats()
	if stats != nil {
		reply["numscs"] = stats["numscs"]
		reply["indexedscs"] = stats["indexedscs"]
		reply["indexdetails"] = stats["indexdetails"]
		reply["regTxCount"] = stats["regTxCount"]
		reply["burnTxCount"] = stats["burnTxCount"]
		reply["normTxCount"] = stats["normTxCount"]
	} else {
		// Default reply - for testing etc.
		reply["hello"] = "world"
	}

	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		logger.Errorf("[API] Error serializing API response: %v", err)
	}
}

func (apiServer *ApiServer) InvokeIndexBySCID(writer http.ResponseWriter, r *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	reply := make(map[string]interface{})

	stats := apiServer.getStats()
	if stats != nil {
		reply["numscs"] = stats["numscs"]
		reply["regTxCount"] = stats["regTxCount"]
		reply["burnTxCount"] = stats["burnTxCount"]
		reply["normTxCount"] = stats["normTxCount"]
	} else {
		// Default reply - for testing etc.
		reply["hello"] = "world"
	}

	// Query for SCID
	scidkeys, ok := r.URL.Query()["scid"]
	var scid string
	var address string

	if !ok || len(scidkeys[0]) < 1 {
		logger.Debugf("[API] URL Param 'scid' is missing. Debugging only.")
	} else {
		scid = scidkeys[0]
	}

	// Query for address
	addresskeys, ok := r.URL.Query()["address"]

	if !ok || len(addresskeys[0]) < 1 {
		logger.Debugf("[API] URL Param 'address' is missing.")
	} else {
		address = addresskeys[0]
	}

	// Get all scid:owner
	sclist := make(map[string]string)
	switch apiServer.DBType {
	case "gravdb":
		sclist = apiServer.GravDBBackend.GetAllOwnersAndSCIDs()
	case "boltdb":
		sclist = apiServer.BBSBackend.GetAllOwnersAndSCIDs()
	}

	if address != "" && scid != "" {
		// Return results that match both address and scid
		var addrscidinvokes []*structures.SCTXParse

		for k := range sclist {
			if k == scid {
				switch apiServer.DBType {
				case "gravdb":
					addrscidinvokes = apiServer.GravDBBackend.GetAllSCIDInvokeDetailsBySigner(scid, address)
				case "boltdb":
					addrscidinvokes = apiServer.BBSBackend.GetAllSCIDInvokeDetailsBySigner(scid, address)
				}
				break
			}
		}

		// Case to ignore large variable returns
		if len(addrscidinvokes) > structures.MAX_API_VAR_RETURN && apiServer.Config.ApiThrottle {
			logger.Printf("[API-InvokeIndexBySCID] Tried to return more than %d sc indexes for %s... DENIED! Too much data...", structures.MAX_API_VAR_RETURN, scid)
			reply["addrscidinvokescount"] = 0
			reply["addrscidinvokes"] = nil

			err := json.NewEncoder(writer).Encode(reply)
			if err != nil {
				logger.Errorf("[API] Error serializing API response: %v", err)
			}
			return
		}
		reply["addrscidinvokescount"] = len(addrscidinvokes)
		reply["addrscidinvokes"] = addrscidinvokes
	} else if address != "" && scid == "" {
		// If address and no scid, return combined results of all instances address is defined (invokes and installs)
		var addrinvokes [][]*structures.SCTXParse

		for k := range sclist {
			var currinvokedetails []*structures.SCTXParse
			switch apiServer.DBType {
			case "gravdb":
				currinvokedetails = apiServer.GravDBBackend.GetAllSCIDInvokeDetailsBySigner(k, address)
			case "boltdb":
				currinvokedetails = apiServer.BBSBackend.GetAllSCIDInvokeDetailsBySigner(k, address)
			}

			if currinvokedetails != nil {
				addrinvokes = append(addrinvokes, currinvokedetails)
			}
		}

		// Case to ignore large variable returns
		if len(addrinvokes) > structures.MAX_API_VAR_RETURN && apiServer.Config.ApiThrottle {
			logger.Printf("[API-InvokeIndexBySCID] Tried to return more than %d sc indexes for %s... DENIED! Too much data...", structures.MAX_API_VAR_RETURN, scid)
			reply["addrinvokescount"] = 0
			reply["addrinvokes"] = nil

			err := json.NewEncoder(writer).Encode(reply)
			if err != nil {
				logger.Errorf("[API] Error serializing API response: %v", err)
			}
			return
		}

		reply["addrinvokescount"] = len(addrinvokes)
		reply["addrinvokes"] = addrinvokes
	} else if address == "" && scid != "" {
		// If no address and scid only, return invokes of scid
		var scidinvokes []*structures.SCTXParse
		switch apiServer.DBType {
		case "gravdb":
			scidinvokes = apiServer.GravDBBackend.GetAllSCIDInvokeDetails(scid)
		case "boltdb":
			scidinvokes = apiServer.BBSBackend.GetAllSCIDInvokeDetails(scid)
		}

		// Case to ignore large variable returns
		if len(scidinvokes) > structures.MAX_API_VAR_RETURN && apiServer.Config.ApiThrottle {
			logger.Printf("[API-InvokeIndexBySCID] Tried to return more than %d sc indexes for %s... DENIED! Too much data...", structures.MAX_API_VAR_RETURN, scid)
			reply["scidinvokescount"] = 0
			reply["scidinvokes"] = nil

			err := json.NewEncoder(writer).Encode(reply)
			if err != nil {
				logger.Errorf("[API] Error serializing API response: %v", err)
			}
			return
		}

		reply["scidinvokescount"] = len(scidinvokes)
		reply["scidinvokes"] = scidinvokes
	}

	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		logger.Errorf("[API] Error serializing API response: %v", err)
	}
}

func (apiServer *ApiServer) InvokeSCVarsByHeight(writer http.ResponseWriter, r *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	reply := make(map[string]interface{})

	stats := apiServer.getStats()
	if stats != nil {
		reply["numscs"] = stats["numscs"]
		reply["regTxCount"] = stats["regTxCount"]
		reply["burnTxCount"] = stats["burnTxCount"]
		reply["normTxCount"] = stats["normTxCount"]
	} else {
		// Default reply - for testing, initials etc.
		reply["hello"] = "world"
	}

	// Query for SCID
	scidkeys, ok := r.URL.Query()["scid"]
	var scid string
	var height string

	if !ok || len(scidkeys[0]) < 1 {
		logger.Debugf("[API] URL Param 'scid' is missing. Debugging only.")
		reply["variables"] = nil
		err := json.NewEncoder(writer).Encode(reply)
		if err != nil {
			logger.Errorf("[API] Error serializing API response: %v", err)
		}
		return
	} else {
		scid = scidkeys[0]
	}

	// Query for address
	heightkey, ok := r.URL.Query()["height"]

	if !ok || len(heightkey[0]) < 1 {
		logger.Debugf("[API] URL Param 'height' is missing.")
	} else {
		height = heightkey[0]
	}

	if height != "" {
		var variables []*structures.SCIDVariable
		var scidInteractionHeights []int64
		var interactionHeight int64

		var err error

		var topoheight int64
		topoheight, err = strconv.ParseInt(height, 10, 64)
		if err != nil {
			logger.Errorf("[API] Err converting '%v' to int64 - %v", height, err)

			err := json.NewEncoder(writer).Encode(reply)
			if err != nil {
				logger.Errorf("[API] Error serializing API response: %v", err)
			}
		}

		switch apiServer.DBType {
		case "gravdb":
			scidInteractionHeights = apiServer.GravDBBackend.GetSCIDInteractionHeight(scid)

			interactionHeight = apiServer.GravDBBackend.GetInteractionIndex(topoheight, scidInteractionHeights, false)

			// TODO: If there's no interaction height, do we go get scvars against daemon and store?
			variables = apiServer.GravDBBackend.GetSCIDVariableDetailsAtTopoheight(scid, interactionHeight)

			// Case to ignore large variable returns
			if len(variables) > structures.MAX_API_VAR_RETURN && apiServer.Config.ApiThrottle {
				logger.Printf("[API-InvokeSCVarsByHeight] Tried to return more than %d sc vars for %s... DENIED! Too much data...", structures.MAX_API_VAR_RETURN, scid)
				reply["variables"] = nil

				err := json.NewEncoder(writer).Encode(reply)
				if err != nil {
					logger.Errorf("[API] Error serializing API response: %v", err)
				}
				return
			}
		case "boltdb":
			scidInteractionHeights = apiServer.BBSBackend.GetSCIDInteractionHeight(scid)

			interactionHeight = apiServer.BBSBackend.GetInteractionIndex(topoheight, scidInteractionHeights, false)

			// TODO: If there's no interaction height, do we go get scvars against daemon and store?
			variables = apiServer.BBSBackend.GetSCIDVariableDetailsAtTopoheight(scid, interactionHeight)

			// Case to ignore large variable returns
			if len(variables) > structures.MAX_API_VAR_RETURN && apiServer.Config.ApiThrottle {
				logger.Printf("[API-InvokeSCVarsByHeight] Tried to return more than %d sc vars for %s... DENIED! Too much data...", structures.MAX_API_VAR_RETURN, scid)
				reply["variables"] = nil

				err := json.NewEncoder(writer).Encode(reply)
				if err != nil {
					logger.Errorf("[API] Error serializing API response: %v", err)
				}
				return
			}
		}

		reply["variables"] = variables
		reply["scidinteractionheight"] = interactionHeight
	} else {
		// TODO: Do we need this case? Should we always require a height to be defined so as not to slow the api return due to large dataset? Do we keep but put a limit on return amount?

		var variables []*structures.SCIDVariable
		var scidInteractionHeights []int64

		// Case to ignore all variable instance returns for builtin registration tx - large amount of data.
		if (scid == "0000000000000000000000000000000000000000000000000000000000000001" || scid == structures.MAINNET_GNOMON_SCID || scid == structures.TESTNET_GNOMON_SCID) && apiServer.Config.ApiThrottle {
			logger.Printf("[API-InvokeSCVarsByHeight] Tried to return all the sc vars of everything at registration builtin... DENIED! Too much data...")
			reply["variables"] = nil

			err := json.NewEncoder(writer).Encode(reply)
			if err != nil {
				logger.Errorf("[API] Error serializing API response: %v", err)
			}
			return
		}

		switch apiServer.DBType {
		case "gravdb":
			scidInteractionHeights = apiServer.GravDBBackend.GetSCIDInteractionHeight(scid)
			variables = apiServer.GravDBBackend.GetAllSCIDVariableDetails(scid)

			// Case to ignore large variable returns
			if len(variables) > structures.MAX_API_VAR_RETURN && apiServer.Config.ApiThrottle {
				logger.Printf("[API-InvokeSCVarsByHeight] Tried to return more than %d sc vars for %s... DENIED! Too much data...", structures.MAX_API_VAR_RETURN, scid)
				reply["variables"] = nil

				err := json.NewEncoder(writer).Encode(reply)
				if err != nil {
					logger.Errorf("[API] Error serializing API response: %v", err)
				}
				return
			}
		case "boltdb":
			scidInteractionHeights = apiServer.BBSBackend.GetSCIDInteractionHeight(scid)
			variables = apiServer.BBSBackend.GetAllSCIDVariableDetails(scid)

			// Case to ignore large variable returns
			if len(variables) > structures.MAX_API_VAR_RETURN && apiServer.Config.ApiThrottle {
				logger.Printf("[API-InvokeSCVarsByHeight] Tried to return more than %d sc vars for %s... DENIED! Too much data...", structures.MAX_API_VAR_RETURN, scid)
				reply["variables"] = nil

				err := json.NewEncoder(writer).Encode(reply)
				if err != nil {
					logger.Errorf("[API] Error serializing API response: %v", err)
				}
				return
			}
		}

		reply["variables"] = variables
		reply["scidinteractionheights"] = scidInteractionHeights
	}

	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		logger.Errorf("[API] Error serializing API response: %v", err)
	}
}

func (apiServer *ApiServer) NormalTxWithSCID(writer http.ResponseWriter, r *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	reply := make(map[string]interface{})

	stats := apiServer.getStats()
	if stats != nil {
		reply["numscs"] = stats["numscs"]
		reply["regTxCount"] = stats["regTxCount"]
		reply["burnTxCount"] = stats["burnTxCount"]
		reply["normTxCount"] = stats["normTxCount"]
	} else {
		// Default reply - for testing, initials etc.
		reply["hello"] = "world"
	}

	// Query for SCID
	scidkeys, ok := r.URL.Query()["scid"]
	var scid string
	var address string

	if !ok || len(scidkeys[0]) < 1 {
		logger.Debugf("[API] URL Param 'scid' is missing. Debugging only.")
	} else {
		scid = scidkeys[0]
	}

	// Query for address
	addresskeys, ok := r.URL.Query()["address"]

	if !ok || len(addresskeys[0]) < 1 {
		logger.Debugf("[API] URL Param 'address' is missing.")
	} else {
		address = addresskeys[0]
	}

	if address == "" && scid == "" {
		reply["variables"] = nil
		err := json.NewEncoder(writer).Encode(reply)
		if err != nil {
			logger.Errorf("[API] Error serializing API response: %v", err)
		}
		return
	}

	var allNormTxWithSCIDByAddr []*structures.NormalTXWithSCIDParse
	var allNormTxWithSCIDBySCID []*structures.NormalTXWithSCIDParse

	switch apiServer.DBType {
	case "gravdb":
		allNormTxWithSCIDByAddr = apiServer.GravDBBackend.GetAllNormalTxWithSCIDByAddr(address)
		allNormTxWithSCIDBySCID = apiServer.GravDBBackend.GetAllNormalTxWithSCIDBySCID(scid)
	case "boltdb":
		allNormTxWithSCIDByAddr = apiServer.BBSBackend.GetAllNormalTxWithSCIDByAddr(address)
		allNormTxWithSCIDBySCID = apiServer.BBSBackend.GetAllNormalTxWithSCIDBySCID(scid)
	}

	// Case to ignore large variable returns
	if (len(allNormTxWithSCIDByAddr) > structures.MAX_API_VAR_RETURN || len(allNormTxWithSCIDBySCID) > structures.MAX_API_VAR_RETURN) && apiServer.Config.ApiThrottle {
		logger.Printf("[API-NormalTxWithSCID] Tried to return more than %d... DENIED! Too much data...", structures.MAX_API_VAR_RETURN)
		reply["normtxwithscidbyaddr"] = nil
		reply["normtxwithscidbyaddrcount"] = 0
		reply["normtxwithscidbyscid"] = nil
		reply["normtxwithscidbyscidcount"] = 0

		err := json.NewEncoder(writer).Encode(reply)
		if err != nil {
			logger.Errorf("[API] Error serializing API response: %v", err)
		}
		return
	}

	reply["normtxwithscidbyaddr"] = allNormTxWithSCIDByAddr
	reply["normtxwithscidbyaddrcount"] = len(allNormTxWithSCIDByAddr)
	reply["normtxwithscidbyscid"] = allNormTxWithSCIDBySCID
	reply["normtxwithscidbyscidcount"] = len(allNormTxWithSCIDBySCID)

	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		logger.Errorf("[API] Error serializing API response: %v", err)
	}
}

func (apiServer *ApiServer) InvalidSCIDStats(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	reply := make(map[string]interface{})
	invalidscids := make(map[string]uint64)

	switch apiServer.DBType {
	case "gravdb":
		invalidscids = apiServer.GravDBBackend.GetInvalidSCIDDeploys()
	case "boltdb":
		invalidscids = apiServer.BBSBackend.GetInvalidSCIDDeploys()
	}

	// Case to ignore large variable returns
	if len(invalidscids) > structures.MAX_API_VAR_RETURN && apiServer.Config.ApiThrottle {
		logger.Printf("[API-InvalidSCIDStats] Tried to return more than %d.. DENIED! Too much data...", structures.MAX_API_VAR_RETURN)
		reply["invalidscids"] = nil

		err := json.NewEncoder(writer).Encode(reply)
		if err != nil {
			logger.Errorf("[API] Error serializing API response: %v", err)
		}
		return
	}

	reply["invalidscids"] = invalidscids

	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		logger.Errorf("[API] Error serializing API response: %v", err)
	}
}

func (apiServer *ApiServer) MBLLookupByHash(writer http.ResponseWriter, r *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	reply := make(map[string]interface{})

	stats := apiServer.getStats()
	if stats != nil {
		reply["numscs"] = stats["numscs"]
		reply["regTxCount"] = stats["regTxCount"]
		reply["burnTxCount"] = stats["burnTxCount"]
		reply["normTxCount"] = stats["normTxCount"]
	} else {
		// Default reply - for testing, initials etc.
		reply["hello"] = "world"
	}

	// Query for SCID
	blidkeys, ok := r.URL.Query()["blid"]
	var blid string

	if !ok || len(blidkeys[0]) < 1 {
		logger.Debugf("[API] URL Param 'blid' is missing. Debugging only.")
		reply["mbl"] = nil
		err := json.NewEncoder(writer).Encode(reply)
		if err != nil {
			logger.Errorf("[API] Error serializing API response: %v", err)
		}
		return
	} else {
		blid = blidkeys[0]
	}

	var allMiniBlocksByBlid []*structures.MBLInfo

	switch apiServer.DBType {
	case "gravdb":
		allMiniBlocksByBlid = apiServer.GravDBBackend.GetMiniblockDetailsByHash(blid)
	case "boltdb":
		allMiniBlocksByBlid = apiServer.BBSBackend.GetMiniblockDetailsByHash(blid)
	}

	// Case to ignore large variable returns
	if len(allMiniBlocksByBlid) > structures.MAX_API_VAR_RETURN && apiServer.Config.ApiThrottle {
		logger.Printf("[API-MBLLookupByHash] Tried to return more than %d.. DENIED! Too much data...", structures.MAX_API_VAR_RETURN)
		reply["mbl"] = nil

		err := json.NewEncoder(writer).Encode(reply)
		if err != nil {
			logger.Errorf("[API] Error serializing API response: %v", err)
		}
		return
	}

	reply["mbl"] = allMiniBlocksByBlid

	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		logger.Errorf("[API] Error serializing API response: %v", err)
	}
}

func (apiServer *ApiServer) MBLLookupByAddr(writer http.ResponseWriter, r *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	reply := make(map[string]interface{})

	stats := apiServer.getStats()
	if stats != nil {
		reply["numscs"] = stats["numscs"]
		reply["regTxCount"] = stats["regTxCount"]
		reply["burnTxCount"] = stats["burnTxCount"]
		reply["normTxCount"] = stats["normTxCount"]
	} else {
		// Default reply - for testing, initials etc.
		reply["hello"] = "world"
	}

	// Query for SCID
	addrkeys, ok := r.URL.Query()["address"]
	var addr string

	if !ok || len(addrkeys[0]) < 1 {
		logger.Debugf("[API] URL Param 'address' is missing. Debugging only.")
		reply["mbl"] = nil
		err := json.NewEncoder(writer).Encode(reply)
		if err != nil {
			logger.Errorf("[API] Error serializing API response: %v", err)
		}
		return
	} else {
		addr = addrkeys[0]
	}

	var allMiniBlocksByAddr int64
	switch apiServer.DBType {
	case "gravdb":
		allMiniBlocksByAddr = apiServer.GravDBBackend.GetMiniblockCountByAddress(addr)
	case "boltdb":
		allMiniBlocksByAddr = apiServer.BBSBackend.GetMiniblockCountByAddress(addr)
	}

	reply["mbl"] = allMiniBlocksByAddr

	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		logger.Errorf("[API] Error serializing API response: %v", err)
	}
}

func (apiServer *ApiServer) MBLLookupAll(writer http.ResponseWriter, r *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	reply := make(map[string]interface{})

	stats := apiServer.getStats()
	if stats != nil {
		reply["numscs"] = stats["numscs"]
		reply["regTxCount"] = stats["regTxCount"]
		reply["burnTxCount"] = stats["burnTxCount"]
		reply["normTxCount"] = stats["normTxCount"]
	} else {
		// Default reply - for testing, initials etc.
		reply["hello"] = "world"
	}

	allMiniBlocks := make(map[string][]*structures.MBLInfo)
	switch apiServer.DBType {
	case "gravdb":
		allMiniBlocks = apiServer.GravDBBackend.GetAllMiniblockDetails()
	case "boltdb":
		allMiniBlocks = apiServer.BBSBackend.GetAllMiniblockDetails()
	}

	// Case to ignore large variable returns
	if len(allMiniBlocks) > structures.MAX_API_VAR_RETURN && apiServer.Config.ApiThrottle {
		logger.Printf("[API-MBLLookupAll] Tried to return more than %d.. DENIED! Too much data...", structures.MAX_API_VAR_RETURN)
		reply["mbl"] = nil

		err := json.NewEncoder(writer).Encode(reply)
		if err != nil {
			logger.Errorf("[API] Error serializing API response: %v", err)
		}
		return
	}

	reply["mbl"] = allMiniBlocks

	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		logger.Errorf("[API] Error serializing API response: %v", err)
	}
}

func (apiServer *ApiServer) GetInfo(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	reply := make(map[string]interface{})

	var info *structures.GetInfo
	switch apiServer.DBType {
	case "gravdb":
		info = apiServer.GravDBBackend.GetGetInfoDetails()
	case "boltdb":
		info = apiServer.BBSBackend.GetGetInfoDetails()
	}

	reply["getinfo"] = info

	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		logger.Errorf("[API] Error serializing API response: %v", err)
	}
}

func (apiServer *ApiServer) getStats() map[string]interface{} {
	stats := apiServer.Stats.Load()
	if stats != nil {
		return stats.(map[string]interface{})
	}
	return nil
}
