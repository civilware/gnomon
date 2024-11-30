package storage

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/civilware/Gnomon/structures"
	"github.com/deroproject/graviton"
	"github.com/sirupsen/logrus"
)

type GravitonStore struct {
	DB            *graviton.Store
	DBPath        string
	DBTrees       []string
	migrating     int
	DBMaxSnapshot uint64
	DBMigrateWait time.Duration
	Writing       int
	Closing       bool
}

type TreeKV struct {
	k []byte
	v []byte
}

// TODO/NOTE: Lots of optimization/modifications are to be had here. Commit handling, tree structures, folder structure etc to reduce disk usage. It's higher now than will be in future

// ---- Application Graviton/Backend functions ---- //
// Builds new Graviton DB based on input from main()
func NewGravDB(dbPath, dbmigratewait string) (*GravitonStore, error) {
	var err error
	var graviton_backend *GravitonStore = &GravitonStore{}

	logger = structures.Logger.WithFields(logrus.Fields{})

	graviton_backend.DBMigrateWait, _ = time.ParseDuration(dbmigratewait)

	graviton_backend.DBPath = dbPath

	graviton_backend.DB, err = graviton.NewDiskStore(graviton_backend.DBPath)
	if err != nil {
		return graviton_backend, fmt.Errorf("[NewGravDB] Could not create db store: %v", err)
	}

	return graviton_backend, err
}

// Builds new Graviton DB based on input from main() RAM store
func NewGravDBRAM(dbmigratewait string) (*GravitonStore, error) {
	var err error
	var graviton_backend *GravitonStore = &GravitonStore{}

	logger = structures.Logger.WithFields(logrus.Fields{})

	graviton_backend.DBMigrateWait, _ = time.ParseDuration(dbmigratewait)

	//graviton_backend.DB, err = graviton.NewDiskStore(graviton_backend.DBPath)
	graviton_backend.DB, err = graviton.NewMemStore()
	if err != nil {
		return graviton_backend, fmt.Errorf("[NewGravDB] Could not create db store: %v", err)
	}

	return graviton_backend, err
}

// Stores gnomon's last indexed height - this is for stateful stores on close and reference on open
func (g *GravitonStore) StoreLastIndexHeight(last_indexedheight int64, nocommit bool) (tree *graviton.Tree, changes bool, err error) {
	store := g.DB
	ss, err := store.LoadSnapshot(0) // load most recent snapshot
	if err != nil {
		return
	}

	topoheight := strconv.FormatInt(last_indexedheight, 10)

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		logger.Debugf("[StoreLastIndexHeight] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, err = store.LoadSnapshot(0) // load most recent snapshot
		if err != nil {
			return
		}
	}

	tree, _ = ss.GetTree("stats")
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-StoreLastIndexHeight] ERROR: Tree is nil for 'stats'. Attempting to rollback 1 snapshot")
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return
		}
		tree, terr = prevss.GetTree("stats")
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return tree, changes, terr
		}
	}
	tree.Put([]byte("lastindexedheight"), []byte(topoheight)) // insert a value
	changes = true
	if !nocommit {
		_, cerr := graviton.Commit(tree)
		if cerr != nil {
			logger.Errorf("[Graviton] ERROR: %v", cerr)
			return tree, changes, cerr
		}
	}
	return tree, changes, nil
}

// Gets gnomon's last indexed height - this is for stateful stores on close and reference on open
func (g *GravitonStore) GetLastIndexHeight() (topoheight int64, err error) {
	store := g.DB
	ss, err := store.LoadSnapshot(0) // load most recent snapshot
	if err != nil {
		return topoheight, err
	}

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		logger.Debugf("[GetLastIndexHeight] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, err = store.LoadSnapshot(0) // load most recent snapshot
		if err != nil {
			return topoheight, err
		}
	}

	tree, _ := ss.GetTree("stats") // use or create tree named by poolhost in config
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-GetLastIndexHeight] ERROR: Tree is nil for 'stats'. Attempting to rollback 1 snapshot")
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return topoheight, preverr
		}
		tree, terr = prevss.GetTree("stats")
		if tree == nil {
			return topoheight, fmt.Errorf("[Graviton] ERROR: %v", terr)
		}
	}
	key := "lastindexedheight"

	v, _ := tree.Get([]byte(key))

	if v != nil {
		topoheight, err = strconv.ParseInt(string(v), 10, 64)
		if err != nil {
			return topoheight, fmt.Errorf("ERR - Error parsing stored int for lastindexheight: %v", err)
		}
		return topoheight, err
	}

	logger.Printf("[GetLastIndexHeight] No stored last index height. Starting from 0 or latest if fastsync is enabled")

	return topoheight, err
}

// Stores gnomon's txcount by a given txType - this is for stateful stores on close and reference on open
func (g *GravitonStore) StoreTxCount(count int64, txType string, nocommit bool) (tree *graviton.Tree, changes bool, err error) {
	store := g.DB
	ss, err := store.LoadSnapshot(0) // load most recent snapshot
	if err != nil {
		return
	}

	txCount := strconv.FormatInt(count, 10)

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		logger.Debugf("[StoreTxCount] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, err = store.LoadSnapshot(0) // load most recent snapshot
		if err != nil {
			return
		}
	}

	key := txType + "txcount"

	tree, _ = ss.GetTree("stats")
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-StoreTxCount] ERROR: Tree is nil for 'stats'. Attempting to rollback 1 snapshot")
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return tree, changes, preverr
		}
		tree, terr = prevss.GetTree("stats")
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return tree, changes, terr
		}
	}
	tree.Put([]byte(key), []byte(txCount)) // insert a value
	changes = true
	if !nocommit {
		_, cerr := graviton.Commit(tree)
		if cerr != nil {
			logger.Errorf("[Graviton] ERROR: %v", cerr)
			return tree, changes, cerr
		}
	}
	return tree, changes, nil
}

// Gets gnomon's txcount by a given txType - this is for stateful stores on close and reference on open
func (g *GravitonStore) GetTxCount(txType string) int64 {
	store := g.DB
	ss, err := store.LoadSnapshot(0) // load most recent snapshot
	if err != nil {
		return 0
	}

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		logger.Debugf("[GetTxCount] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, err = store.LoadSnapshot(0) // load most recent snapshot
		if err != nil {
			return 0
		}
	}

	tree, _ := ss.GetTree("stats") // use or create tree named by poolhost in config
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-GetTxCount] ERROR: Tree is nil for 'stats'. Attempting to rollback 1 snapshot")
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return 0
		}
		tree, terr = prevss.GetTree("stats")
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return 0
		}
	}
	key := txType + "txcount"

	v, _ := tree.Get([]byte(key))

	if v != nil {
		txCount, err := strconv.ParseInt(string(v), 10, 64)
		if err != nil {
			logger.Errorf("ERR - Error parsing stored int for txcount: %v", err)
			return 0
		}
		return txCount
	}

	//logger.Printf("[GetTxCount] No txcount stored for '%v'. Starting from 0", txType)

	return 0
}

// Stores the owner (who deployed it) of a given scid
func (g *GravitonStore) StoreOwner(scid string, owner string, nocommit bool) (tree *graviton.Tree, changes bool, err error) {
	store := g.DB
	ss, err := store.LoadSnapshot(0) // load most recent snapshot
	if err != nil {
		return
	}

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		logger.Debugf("[StoreOwner] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, err = store.LoadSnapshot(0) // load most recent snapshot
		if err != nil {
			return
		}
	}

	tree, _ = ss.GetTree("owner")
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-StoreOwner] ERROR: Tree is nil for 'owner'. Attempting to rollback 1 snapshot")
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return tree, changes, preverr
		}
		tree, terr = prevss.GetTree("owner")
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return tree, changes, terr
		}
	}
	tree.Put([]byte(scid), []byte(owner)) // insert a value
	changes = true
	if !nocommit {
		_, cerr := graviton.Commit(tree)
		if cerr != nil {
			logger.Errorf("[Graviton] ERROR: %v", cerr)
			return tree, changes, cerr
		}
	}
	return tree, changes, nil
}

// Returns the owner (who deployed it) of a given scid
func (g *GravitonStore) GetOwner(scid string) string {
	store := g.DB
	ss, err := store.LoadSnapshot(0) // load most recent snapshot
	if err != nil {
		return ""
	}

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		logger.Debugf("[GetOwner] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, err = store.LoadSnapshot(0) // load most recent snapshot
		if err != nil {
			return ""
		}
	}

	tree, _ := ss.GetTree("owner") // use or create tree named by poolhost in config
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-GetOwner] ERROR: Tree is nil for 'owner'. Attempting to rollback 1 snapshot")
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return ""
		}
		tree, terr = prevss.GetTree("owner")
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return ""
		}
	}
	key := scid

	v, _ := tree.Get([]byte(key))

	if v != nil {
		return string(v)
	}

	logger.Printf("[GetOwner] No owner for %v", scid)

	return ""
}

// Returns all of the deployed SCIDs with their corresponding owners (who deployed it)
func (g *GravitonStore) GetAllOwnersAndSCIDs() (results map[string]string) {
	results = make(map[string]string)

	store := g.DB
	ss, err := store.LoadSnapshot(0)
	if err != nil {
		return
	}
	tree, _ := ss.GetTree("owner")
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-GetAllOwnersAndSCIDs] ERROR: Tree is nil for 'owner'. Attempting to rollback 1 snapshot")
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return
		}
		tree, terr = prevss.GetTree("owner")
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return
		}
	}

	c := tree.Cursor()
	// Duplicate the LATEST (snapshot 0) to the new DB, this starts the DB over again, but still retaining X number of old DBs for version in future use cases. Here we get the vals before swapping to new db in mem
	for k, v, err := c.First(); err == nil; k, v, err = c.Next() {
		results[string(k)] = string(v)
	}

	return
}

// Stores the install height of a given scid
func (g *GravitonStore) StoreInstallHeight(scid string, height int64, nocommit bool) (tree *graviton.Tree, changes bool, err error) {
	store := g.DB
	ss, err := store.LoadSnapshot(0) // load most recent snapshot
	if err != nil {
		return
	}

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		logger.Debugf("[StoreInstallHeight] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, err = store.LoadSnapshot(0) // load most recent snapshot
		if err != nil {
			return
		}
	}

	tree, _ = ss.GetTree("sciheight")
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-StoreInstallHeight] ERROR: Tree is nil for 'owner'. Attempting to rollback 1 snapshot")
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return tree, changes, preverr
		}
		tree, terr = prevss.GetTree("sciheight")
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return tree, changes, terr
		}
	}

	iHeight := strconv.FormatInt(height, 10)

	tree.Put([]byte(scid), []byte(iHeight)) // insert a value
	changes = true
	if !nocommit {
		_, cerr := graviton.Commit(tree)
		if cerr != nil {
			logger.Errorf("[Graviton] ERROR: %v", cerr)
			return tree, changes, cerr
		}
	}
	return tree, changes, nil
}

// Returns the install height of a given scid
func (g *GravitonStore) GetInstallHeight(scid string) (iHeight int64) {
	store := g.DB
	ss, err := store.LoadSnapshot(0) // load most recent snapshot
	if err != nil {
		return
	}

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		logger.Debugf("[GetInstallHeight] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, err = store.LoadSnapshot(0) // load most recent snapshot
		if err != nil {
			return
		}
	}

	tree, _ := ss.GetTree("sciheight") // use or create tree named by poolhost in config
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-GetInstallHeight] ERROR: Tree is nil for 'owner'. Attempting to rollback 1 snapshot")
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return
		}
		tree, terr = prevss.GetTree("sciheight")
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return
		}
	}
	key := scid

	v, _ := tree.Get([]byte(key))

	if v != nil {
		iHeight, err = strconv.ParseInt(string(v), 10, 64)
		if err != nil {
			logger.Errorf("ERR - Error parsing stored int for iHeight: %v", err)
			return
		}
		return
	}

	return
}

// Returns all of the deployed SCIDs with their corresponding install heights
func (g *GravitonStore) GetAllSCIDsAndInstallHeights() (results map[string]int64) {
	results = make(map[string]int64)

	store := g.DB
	ss, err := store.LoadSnapshot(0)
	if err != nil {
		return
	}
	tree, _ := ss.GetTree("sciheight")
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-GetAllSCIDsAndInstallHeights] ERROR: Tree is nil for 'owner'. Attempting to rollback 1 snapshot")
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return
		}
		tree, terr = prevss.GetTree("sciheight")
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return
		}
	}

	c := tree.Cursor()
	// Duplicate the LATEST (snapshot 0) to the new DB, this starts the DB over again, but still retaining X number of old DBs for version in future use cases. Here we get the vals before swapping to new db in mem
	for k, v, err := c.First(); err == nil; k, v, err = c.Next() {
		results[string(k)], err = strconv.ParseInt(string(v), 10, 64)
		if err != nil {
			logger.Errorf("[Graviton-GetAllSCIDsAndInstallHeights] ERR - Error parsing stored int for install height: %v", err)
		}
	}

	return
}

// Stores all normal txs with SCIDs and their respective ring members for future balance/interaction reference
func (g *GravitonStore) StoreNormalTxWithSCIDByAddr(addr string, normTxWithSCID *structures.NormalTXWithSCIDParse, nocommit bool) (tree *graviton.Tree, changes bool, err error) {
	store := g.DB
	ss, err := store.LoadSnapshot(0) // load most recent snapshot
	if err != nil {
		return
	}

	// Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		logger.Debugf("[StoreNormalTxWithSCIDByAddr] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, err = store.LoadSnapshot(0) // load most recent snapshot
		if err != nil {
			return
		}
	}

	treename := "normaltxwithscid"
	tree, _ = ss.GetTree(treename)
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-StoreNormalTxWithSCIDByAddr] ERROR: Tree is nil for 'normaltxwithscid'. Attempting to rollback 1 snapshot")
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return tree, changes, preverr
		}
		tree, terr = prevss.GetTree("normaltxwithscid")
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return tree, changes, terr
		}
	}
	key := addr
	currNormTxsWithSCID, err := tree.Get([]byte(key))
	var normTxsWithSCID []*structures.NormalTXWithSCIDParse

	var newNormTxsWithSCID []byte

	if err != nil {
		normTxsWithSCID = append(normTxsWithSCID, normTxWithSCID)
	} else {
		// Retrieve value and conovert to SCIDInteractionHeight, so that you can manipulate and update db
		_ = json.Unmarshal(currNormTxsWithSCID, &normTxsWithSCID)

		for _, v := range normTxsWithSCID {
			if v.Txid == normTxWithSCID.Txid {
				// Return nil if already exists in array.
				// Clause for this is in event we pop backwards in time and already have this data stored.
				// TODO: What if interaction happened on false-chain and pop to retain correct chain. Bad data may be stored here still, as it isn't removed. Need fix for this in future.
				return tree, changes, nil
			}
		}

		normTxsWithSCID = append(normTxsWithSCID, normTxWithSCID)
	}
	newNormTxsWithSCID, err = json.Marshal(normTxsWithSCID)
	if err != nil {
		return tree, changes, fmt.Errorf("[Graviton] could not marshal normTxsWithSCID info: %v", err)
	}

	tree.Put([]byte(key), newNormTxsWithSCID)
	changes = true
	if !nocommit {
		_, cerr := graviton.Commit(tree)
		if cerr != nil {
			logger.Errorf("[Graviton] ERROR: %v", cerr)
			return tree, changes, cerr
		}
	}
	return tree, changes, nil
}

// Returns all normal txs with SCIDs based on a given address
func (g *GravitonStore) GetAllNormalTxWithSCIDByAddr(addr string) (normTxsWithSCID []*structures.NormalTXWithSCIDParse) {
	store := g.DB
	ss, err := store.LoadSnapshot(0) // load most recent snapshot
	if err != nil {
		return
	}

	treename := "normaltxwithscid"
	tree, _ := ss.GetTree(treename) // use or create tree named by poolhost in config
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-GetAllNormalTxWithSCIDByAddr] ERROR: Tree is nil for 'normaltxwithscid'. Attempting to rollback 1 snapshot")
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return
		}
		tree, terr = prevss.GetTree("normaltxwithscid")
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return
		}
	}
	key := addr

	v, _ := tree.Get([]byte(key))

	if v != nil {
		_ = json.Unmarshal(v, &normTxsWithSCID)
		return
	}

	return nil
}

// Returns all normal txs with SCIDs based on a given SCID
func (g *GravitonStore) GetAllNormalTxWithSCIDBySCID(scid string) (normTxsWithSCID []*structures.NormalTXWithSCIDParse) {
	var resultset []string
	store := g.DB
	ss, err := store.LoadSnapshot(0) // load most recent snapshot
	if err != nil {
		return
	}

	treename := "normaltxwithscid"
	tree, _ := ss.GetTree(treename) // use or create tree named by poolhost in config
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-GetAllNormalTxWithSCIDBySCID] ERROR: Tree is nil for 'normaltxwithscid'. Attempting to rollback 1 snapshot")
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return
		}
		tree, terr = prevss.GetTree("normaltxwithscid")
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return
		}
	}

	c := tree.Cursor()
	// Duplicate the LATEST (snapshot 0) to the new DB, this starts the DB over again, but still retaining X number of old DBs for version in future use cases. Here we get the vals before swapping to new db in mem
	for _, v, err := c.First(); err == nil; _, v, err = c.Next() {
		var currdetails []*structures.NormalTXWithSCIDParse
		_ = json.Unmarshal(v, &currdetails)
		for _, cv := range currdetails {
			if cv.Scid == scid && !idExist(resultset, cv.Txid) {
				normTxsWithSCID = append(normTxsWithSCID, cv)
				resultset = append(resultset, cv.Txid)
			}
		}
	}

	return
}

// Check if value exists within a string array/slice
func idExist(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}

	return false
}

// Stores all scinvoke details of a given scid
func (g *GravitonStore) StoreInvokeDetails(scid string, signer string, entrypoint string, topoheight int64, invokedetails *structures.SCTXParse, nocommit bool) (tree *graviton.Tree, changes bool, err error) {
	confBytes, err := json.Marshal(invokedetails)
	if err != nil {
		return &graviton.Tree{}, changes, fmt.Errorf("[StoreInvokeDetails] could not marshal invokedetails info: %v", err)
	}

	store := g.DB
	ss, err := store.LoadSnapshot(0) // load most recent snapshot
	if err != nil {
		return
	}

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		logger.Debugf("[StoreInvokeDetails] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, err = store.LoadSnapshot(0) // load most recent snapshot
		if err != nil {
			return
		}
	}

	// Tree - SCID // (either string or hex - will be []byte in graviton anyways.. may just go with hex)
	// Key - sender:topoheight:entrypoint // (We know that we can have 1 sender per scid per topoheight - do we need entrypoint appended? does it matter?)
	tree, _ = ss.GetTree(scid)
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-StoreInvokeDetails] ERROR: Tree is nil for '%v'. Attempting to rollback 1 snapshot", scid)
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return tree, changes, preverr
		}
		tree, terr = prevss.GetTree(scid)
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return tree, changes, terr
		}
	}

	// Check if installsc call to determine the key
	var key string
	sc_action := fmt.Sprintf("%v", invokedetails.Sc_args.Value("SC_ACTION", "U"))
	if sc_action == "1" {
		logger.Debugf("[StoreInvokeDetails-%s] Storing invokedetails at height '%v' under key '%s'", scid, invokedetails.Height, "installsc")
		key = "installsc"
	} else {
		txidLen := len(invokedetails.Txid)
		key = signer + ":" + invokedetails.Txid[0:3] + invokedetails.Txid[txidLen-3:txidLen] + ":" + strconv.FormatInt(topoheight, 10) + ":" + entrypoint
	}

	tree.Put([]byte(key), confBytes) // insert a value
	changes = true
	if !nocommit {
		_, cerr := graviton.Commit(tree)
		if cerr != nil {
			logger.Errorf("[Graviton] ERROR: %v", cerr)
			return tree, changes, cerr
		}
	}
	return tree, changes, nil
}

// Stores the owner (who deployed it) of a given scid
func (g *GravitonStore) StoreSCIDInstallSCDetails(scid string, invokedetails *structures.SCTXParse, nocommit bool) (tree *graviton.Tree, changes bool, err error) {
	confBytes, err := json.Marshal(invokedetails)
	if err != nil {
		return &graviton.Tree{}, changes, fmt.Errorf("[StoreSCIDInstallSCDetails] could not marshal invokedetails info: %v", err)
	}

	store := g.DB
	ss, err := store.LoadSnapshot(0) // load most recent snapshot
	if err != nil {
		return
	}

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		logger.Debugf("[StoreSCIDInstallSCDetails] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, err = store.LoadSnapshot(0) // load most recent snapshot
		if err != nil {
			return
		}
	}

	tree, _ = ss.GetTree("owner")
	key := "installsc"
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-StoreSCIDInstallSCDetails] ERROR: Tree is nil for 'owner'. Attempting to rollback 1 snapshot")
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return tree, changes, preverr
		}
		tree, terr = prevss.GetTree("owner")
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return tree, changes, terr
		}
	}
	tree.Put([]byte(key), []byte(confBytes)) // insert a value
	changes = true
	if !nocommit {
		_, cerr := graviton.Commit(tree)
		if cerr != nil {
			logger.Errorf("[Graviton] ERROR: %v", cerr)
			return tree, changes, cerr
		}
	}
	return tree, changes, nil
}

// Returns the owner (who deployed it) of a given scid
func (g *GravitonStore) GetSCIDInstallSCDetails(scid string) (invokedetails *structures.SCTXParse) {
	store := g.DB
	ss, err := store.LoadSnapshot(0) // load most recent snapshot
	if err != nil {
		return nil
	}

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		logger.Debugf("[GetSCIDInstallSCDetails] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, err = store.LoadSnapshot(0) // load most recent snapshot
		if err != nil {
			return nil
		}
	}

	tree, _ := ss.GetTree("owner") // use or create tree named by poolhost in config
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-GetSCIDInstallSCDetails] ERROR: Tree is nil for 'owner'. Attempting to rollback 1 snapshot")
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return nil
		}
		tree, terr = prevss.GetTree("stats")
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return nil
		}
	}
	key := scid

	v, _ := tree.Get([]byte(key))

	if v != nil {
		_ = json.Unmarshal(v, &invokedetails)
		return
	}

	return nil
}

// Returns all scinvoke calls from a given scid
func (g *GravitonStore) GetAllSCIDInvokeDetails(scid string) (invokedetails []*structures.SCTXParse) {
	store := g.DB
	ss, err := store.LoadSnapshot(0)
	if err != nil {
		return
	}
	tree, _ := ss.GetTree(scid)
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-GetAllSCIDInvokeDetails] ERROR: Tree is nil for '%v'. Attempting to rollback 1 snapshot", scid)
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return
		}
		tree, terr = prevss.GetTree(scid)
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return
		}
	}

	c := tree.Cursor()
	// Duplicate the LATEST (snapshot 0) to the new DB, this starts the DB over again, but still retaining X number of old DBs for version in future use cases. Here we get the vals before swapping to new db in mem
	for _, v, err := c.First(); err == nil; _, v, err = c.Next() {
		var currdetails *structures.SCTXParse
		_ = json.Unmarshal(v, &currdetails)
		invokedetails = append(invokedetails, currdetails)
	}

	// Sort heights so most recent is index 0 [if preferred reverse, just swap > with <]
	sort.SliceStable(invokedetails, func(i, j int) bool {
		return invokedetails[i].Height < invokedetails[j].Height
	})

	return
}

// Retruns all scinvoke calls from a given scid that match a given entrypoint
func (g *GravitonStore) GetAllSCIDInvokeDetailsByEntrypoint(scid string, entrypoint string) (invokedetails []*structures.SCTXParse) {
	store := g.DB
	ss, err := store.LoadSnapshot(0)
	if err != nil {
		return
	}
	tree, _ := ss.GetTree(scid)
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-GetAllSCIDInvokeDetailsByEntrypoint] ERROR: Tree is nil for '%v'. Attempting to rollback 1 snapshot", scid)
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return
		}
		tree, terr = prevss.GetTree(scid)
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return
		}
	}

	c := tree.Cursor()
	// Duplicate the LATEST (snapshot 0) to the new DB, this starts the DB over again, but still retaining X number of old DBs for version in future use cases. Here we get the vals before swapping to new db in mem
	for _, v, err := c.First(); err == nil; _, v, err = c.Next() {
		var currdetails *structures.SCTXParse
		_ = json.Unmarshal(v, &currdetails)
		if currdetails.Entrypoint == entrypoint {
			invokedetails = append(invokedetails, currdetails)
		}
	}

	// Sort heights so most recent is index 0 [if preferred reverse, just swap > with <]
	sort.SliceStable(invokedetails, func(i, j int) bool {
		return invokedetails[i].Height < invokedetails[j].Height
	})

	return
}

// Returns all scinvoke calls from a given scid that match a given signer
func (g *GravitonStore) GetAllSCIDInvokeDetailsBySigner(scid string, signerPart string) (invokedetails []*structures.SCTXParse) {
	store := g.DB
	ss, err := store.LoadSnapshot(0)
	if err != nil {
		return
	}
	tree, _ := ss.GetTree(scid)
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-GetAllSCIDInvokeDetailsBySigner] ERROR: Tree is nil for '%v'. Attempting to rollback 1 snapshot", scid)
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return
		}
		tree, terr = prevss.GetTree(scid)
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return
		}
	}

	c := tree.Cursor()
	// Duplicate the LATEST (snapshot 0) to the new DB, this starts the DB over again, but still retaining X number of old DBs for version in future use cases. Here we get the vals before swapping to new db in mem
	for _, v, err := c.First(); err == nil; _, v, err = c.Next() {
		var currdetails *structures.SCTXParse
		_ = json.Unmarshal(v, &currdetails)
		split := strings.Split(currdetails.Sender, signerPart)
		if len(split) > 1 {
			invokedetails = append(invokedetails, currdetails)
		}
	}

	// Sort heights so most recent is index 0 [if preferred reverse, just swap > with <]
	sort.SliceStable(invokedetails, func(i, j int) bool {
		return invokedetails[i].Height < invokedetails[j].Height
	})

	return
}

// Stores simple getinfo polling from the daemon
func (g *GravitonStore) StoreGetInfoDetails(getinfo *structures.GetInfo, nocommit bool) (tree *graviton.Tree, changes bool, err error) {
	confBytes, err := json.Marshal(getinfo)
	if err != nil {
		return &graviton.Tree{}, changes, fmt.Errorf("[StoreGetInfoDetails] could not marshal getinfo info: %v", err)
	}

	store := g.DB
	ss, err := store.LoadSnapshot(0) // load most recent snapshot
	if err != nil {
		return
	}

	// Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		logger.Debugf("[StoreGetInfoDetails] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, err = store.LoadSnapshot(0) // load most recent snapshot
		if err != nil {
			return
		}
	}

	tree, _ = ss.GetTree("getinfo")
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-StoreGetInfoDetails] ERROR: Tree is nil for 'getinfo'. Attempting to rollback 1 snapshot")
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return tree, changes, preverr
		}
		tree, terr = prevss.GetTree("getinfo")
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return tree, changes, terr
		}
	}
	key := "getinfo"
	tree.Put([]byte(key), confBytes) // insert a value
	changes = true
	if !nocommit {
		_, cerr := graviton.Commit(tree)
		if cerr != nil {
			logger.Errorf("[Graviton] ERROR: %v", cerr)
			return tree, changes, cerr
		}
	}
	return tree, changes, nil
}

// Returns simple getinfo polling from the daemon
func (g *GravitonStore) GetGetInfoDetails() (getinfo *structures.GetInfo) {
	store := g.DB
	ss, err := store.LoadSnapshot(0) // load most recent snapshot
	if err != nil {
		return
	}

	tree, _ := ss.GetTree("getinfo") // use or create tree named by poolhost in config
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-GetGetInfoDetails] ERROR: Tree is nil for 'getinfo'. Attempting to rollback 1 snapshot")
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return
		}
		tree, terr = prevss.GetTree("getinfo")
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return
		}
	}
	key := "getinfo"

	v, _ := tree.Get([]byte(key))

	if v != nil {
		_ = json.Unmarshal(v, &getinfo)
		return
	}

	return nil
}

// Stores SC variables at a given topoheight (called on any new scdeploy or scinvoke actions)
func (g *GravitonStore) StoreSCIDVariableDetails(scid string, variables []*structures.SCIDVariable, topoheight int64, nocommit bool) (tree *graviton.Tree, changes bool, err error) {
	confBytes, err := json.Marshal(variables)
	if err != nil {
		return &graviton.Tree{}, changes, fmt.Errorf("[StoreSCIDVariableDetails] could not marshal getinfo info: %v", err)
	}

	store := g.DB
	ss, err := store.LoadSnapshot(0) // load most recent snapshot
	if err != nil {
		return
	}

	// Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		logger.Debugf("[StoreSCIDVariableDetails] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, err = store.LoadSnapshot(0) // load most recent snapshot
		if err != nil {
			return
		}
	}

	treename := scid + "vars"
	tree, _ = ss.GetTree(treename)
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-StoreSCIDVariableDetails] ERROR: Tree is nil for '%v'. Attempting to rollback 1 snapshot", treename)
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return tree, changes, preverr
		}
		tree, terr = prevss.GetTree(treename)
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return tree, changes, terr
		}
	}
	key := strconv.FormatInt(topoheight, 10)
	tree.Put([]byte(key), confBytes) // insert a value
	changes = true
	if !nocommit {
		_, cerr := graviton.Commit(tree)
		if cerr != nil {
			logger.Errorf("[Graviton] ERROR: %v", cerr)
			return tree, changes, cerr
		}
	}
	return tree, changes, nil
}

// Gets SC variables at a given topoheight
func (g *GravitonStore) GetSCIDVariableDetailsAtTopoheight(scid string, topoheight int64) (hVars []*structures.SCIDVariable) {
	results := make(map[int64][]*structures.SCIDVariable)
	var heights []int64

	store := g.DB
	ss, err := store.LoadSnapshot(0)
	if err != nil {
		return
	}
	treename := scid + "vars"
	tree, _ := ss.GetTree(treename)
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-GetAllSCIDVariableDetails] ERROR: Tree is nil for '%v'. Attempting to rollback 1 snapshot", treename)
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return
		}
		tree, terr = prevss.GetTree(treename)
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return
		}
	}

	c := tree.Cursor()
	// Duplicate the LATEST (snapshot 0) to the new DB, this starts the DB over again, but still retaining X number of old DBs for version in future use cases. Here we get the vals before swapping to new db in mem
	for k, v, err := c.First(); err == nil; k, v, err = c.Next() {
		topoheight, _ := strconv.ParseInt(string(k), 10, 64)
		heights = append(heights, topoheight)
		var variables []*structures.SCIDVariable
		_ = json.Unmarshal(v, &variables)
		results[topoheight] = variables
	}

	if results != nil {
		// Sort heights so most recent is index 0 [if preferred reverse, just swap > with <]
		sort.SliceStable(heights, func(i, j int) bool {
			return heights[i] < heights[j]
		})

		vs2k := make(map[interface{}]interface{})
		for _, v := range heights {
			if v > topoheight {
				break
			}
			for _, vs := range results[v] {
				switch ckey := vs.Key.(type) {
				case float64:
					switch cval := vs.Value.(type) {
					case float64:
						vs2k[uint64(ckey)] = uint64(cval)
					case uint64:
						vs2k[uint64(ckey)] = cval
					case string:
						vs2k[uint64(ckey)] = cval
					default:
						if cval != nil {
							logger.Errorf("[GetAllSCIDVariableDetails] Value '%v' does not match string, uint64 or float64.", cval)
						}
					}
				case uint64:
					switch cval := vs.Value.(type) {
					case float64:
						vs2k[ckey] = uint64(cval)
					case uint64:
						vs2k[ckey] = cval
					case string:
						vs2k[ckey] = cval
					default:
						if cval != nil {
							logger.Errorf("[GetAllSCIDVariableDetails] Value '%v' does not match string, uint64 or float64.", cval)
						}
					}
				case string:
					switch cval := vs.Value.(type) {
					case float64:
						vs2k[ckey] = uint64(cval)
					case uint64:
						vs2k[ckey] = cval
					case string:
						vs2k[ckey] = cval
					default:
						if cval != nil {
							logger.Errorf("[GetAllSCIDVariableDetails] Value '%v' does not match string, uint64 or float64.", cval)
						}
					}
				default:
					if ckey != nil {
						logger.Errorf("[GetAllSCIDVariableDetails] Key '%v' does not match string, uint64 or float64.", ckey)
					}
				}
			}
		}

		for k, v := range vs2k {
			// If value is nil, no reason to add.
			if v == nil || k == nil {
				logger.Debugf("[GetAllSCIDVariableDetails] Value '%v' or Key '%v' is nil. Continuing.", fmt.Sprintf("%v", v), fmt.Sprintf("%v", k))
				continue
			}
			co := &structures.SCIDVariable{}

			switch ckey := k.(type) {
			case float64:
				switch cval := v.(type) {
				case float64:
					co.Key = uint64(ckey)
					co.Value = uint64(cval)
				case uint64:
					co.Key = uint64(ckey)
					co.Value = cval
				case string:
					co.Key = uint64(ckey)
					co.Value = cval
				default:
					logger.Errorf("[GetAllSCIDVariableDetails] Value '%v' or Key '%v' does not match string, uint64 or float64.", fmt.Sprintf("%v", cval), fmt.Sprintf("%v", uint64(ckey)))
				}
			case uint64:
				switch cval := v.(type) {
				case float64:
					co.Key = ckey
					co.Value = uint64(cval)
				case uint64:
					co.Key = ckey
					co.Value = cval
				case string:
					co.Key = ckey
					co.Value = cval
				default:
					logger.Errorf("[GetAllSCIDVariableDetails] Value '%v' or Key '%v' does not match string, uint64 or float64.", fmt.Sprintf("%v", cval), fmt.Sprintf("%v", ckey))
				}
			case string:
				switch cval := v.(type) {
				case float64:
					co.Key = ckey
					co.Value = uint64(cval)
				case uint64:
					co.Key = ckey
					co.Value = cval
				case string:
					co.Key = ckey
					co.Value = cval
				default:
					logger.Errorf("[GetAllSCIDVariableDetails] Value '%v' or Key '%v' does not match string, uint64 or float64.", fmt.Sprintf("%v", cval), fmt.Sprintf("%v", ckey))
				}
			}

			hVars = append(hVars, co)
		}
	}

	return
}

// Gets SC variables at all topoheights
func (g *GravitonStore) GetAllSCIDVariableDetails(scid string) (hVars []*structures.SCIDVariable) {
	results := make(map[int64][]*structures.SCIDVariable)
	var heights []int64

	store := g.DB
	ss, err := store.LoadSnapshot(0)
	if err != nil {
		return
	}
	treename := scid + "vars"
	tree, _ := ss.GetTree(treename)
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-GetAllSCIDVariableDetails] ERROR: Tree is nil for '%v'. Attempting to rollback 1 snapshot", treename)
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return
		}
		tree, terr = prevss.GetTree(treename)
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return
		}
	}

	c := tree.Cursor()
	// Duplicate the LATEST (snapshot 0) to the new DB, this starts the DB over again, but still retaining X number of old DBs for version in future use cases. Here we get the vals before swapping to new db in mem
	for k, v, err := c.First(); err == nil; k, v, err = c.Next() {
		topoheight, _ := strconv.ParseInt(string(k), 10, 64)
		heights = append(heights, topoheight)
		var variables []*structures.SCIDVariable
		_ = json.Unmarshal(v, &variables)
		results[topoheight] = variables
	}

	if results != nil {
		// Sort heights so most recent is index 0 [if preferred reverse, just swap > with <]
		sort.SliceStable(heights, func(i, j int) bool {
			return heights[i] < heights[j]
		})

		vs2k := make(map[interface{}]interface{})
		for _, v := range heights {
			for _, vs := range results[v] {
				switch ckey := vs.Key.(type) {
				case float64:
					switch cval := vs.Value.(type) {
					case float64:
						vs2k[uint64(ckey)] = uint64(cval)
					case uint64:
						vs2k[uint64(ckey)] = cval
					case string:
						vs2k[uint64(ckey)] = cval
					default:
						if cval != nil {
							logger.Errorf("[GetAllSCIDVariableDetails] Value '%v' does not match string, uint64 or float64.", cval)
						}
					}
				case uint64:
					switch cval := vs.Value.(type) {
					case float64:
						vs2k[ckey] = uint64(cval)
					case uint64:
						vs2k[ckey] = cval
					case string:
						vs2k[ckey] = cval
					default:
						if cval != nil {
							logger.Errorf("[GetAllSCIDVariableDetails] Value '%v' does not match string, uint64 or float64.", cval)
						}
					}
				case string:
					switch cval := vs.Value.(type) {
					case float64:
						vs2k[ckey] = uint64(cval)
					case uint64:
						vs2k[ckey] = cval
					case string:
						vs2k[ckey] = cval
					default:
						if cval != nil {
							logger.Errorf("[GetAllSCIDVariableDetails] Value '%v' does not match string, uint64 or float64.", cval)
						}
					}
				default:
					if ckey != nil {
						logger.Errorf("[GetAllSCIDVariableDetails] Key '%v' does not match string, uint64 or float64.", ckey)
					}
				}
			}
		}

		for k, v := range vs2k {
			// If value is nil, no reason to add.
			if v == nil || k == nil {
				logger.Debugf("[GetAllSCIDVariableDetails] Value '%v' or Key '%v' is nil. Continuing.", fmt.Sprintf("%v", v), fmt.Sprintf("%v", k))
				continue
			}
			co := &structures.SCIDVariable{}

			switch ckey := k.(type) {
			case float64:
				switch cval := v.(type) {
				case float64:
					co.Key = uint64(ckey)
					co.Value = uint64(cval)
				case uint64:
					co.Key = uint64(ckey)
					co.Value = cval
				case string:
					co.Key = uint64(ckey)
					co.Value = cval
				default:
					logger.Errorf("[GetAllSCIDVariableDetails] Value '%v' or Key '%v' does not match string, uint64 or float64.", fmt.Sprintf("%v", cval), fmt.Sprintf("%v", uint64(ckey)))
				}
			case uint64:
				switch cval := v.(type) {
				case float64:
					co.Key = ckey
					co.Value = uint64(cval)
				case uint64:
					co.Key = ckey
					co.Value = cval
				case string:
					co.Key = ckey
					co.Value = cval
				default:
					logger.Errorf("[GetAllSCIDVariableDetails] Value '%v' or Key '%v' does not match string, uint64 or float64.", fmt.Sprintf("%v", cval), fmt.Sprintf("%v", ckey))
				}
			case string:
				switch cval := v.(type) {
				case float64:
					co.Key = ckey
					co.Value = uint64(cval)
				case uint64:
					co.Key = ckey
					co.Value = cval
				case string:
					co.Key = ckey
					co.Value = cval
				default:
					logger.Errorf("[GetAllSCIDVariableDetails] Value '%v' or Key '%v' does not match string, uint64 or float64.", fmt.Sprintf("%v", cval), fmt.Sprintf("%v", ckey))
				}
			}

			hVars = append(hVars, co)
		}
	}

	return
}

// Gets SC variable keys at given topoheight who's value equates to a given interface{} (string/uint64)
func (g *GravitonStore) GetSCIDKeysByValue(scid string, val interface{}, height int64, rmax bool) (keysstring []string, keysuint64 []uint64) {
	scidInteractionHeights := g.GetSCIDInteractionHeight(scid)

	interactionHeight := g.GetInteractionIndex(height, scidInteractionHeights, rmax)

	// TODO: If there's no interaction height, do we go get scvars against daemon and store? Or do we just ignore and return nil
	variables := g.GetSCIDVariableDetailsAtTopoheight(scid, interactionHeight)

	// Switch against the value passed. If it's a uint64 or string
	switch inpvar := val.(type) {
	case uint64:
		for _, v := range variables {
			switch cval := v.Value.(type) {
			case float64:
				if inpvar == uint64(cval) {
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

	return keysstring, keysuint64
}

// Gets SC values by key at given topoheight who's key equates to a given interface{} (string/uint64)
func (g *GravitonStore) GetSCIDValuesByKey(scid string, key interface{}, height int64, rmax bool) (valuesstring []string, valuesuint64 []uint64) {
	scidInteractionHeights := g.GetSCIDInteractionHeight(scid)

	interactionHeight := g.GetInteractionIndex(height, scidInteractionHeights, rmax)

	// TODO: If there's no interaction height, do we go get scvars against daemon and store? Or do we just ignore and return nil
	variables := g.GetSCIDVariableDetailsAtTopoheight(scid, interactionHeight)

	// Switch against the value passed. If it's a uint64 or string
	switch inpvar := key.(type) {
	case uint64:
		for _, v := range variables {
			switch ckey := v.Key.(type) {
			case float64:
				if inpvar == uint64(ckey) {
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

	return valuesstring, valuesuint64
}

// Stores SC interaction height and detail - height invoked upon and type (scinstall/scinvoke). This is separate tree & k/v since we can query it for other things at less data retrieval
func (g *GravitonStore) StoreSCIDInteractionHeight(scid string, height int64, nocommit bool) (tree *graviton.Tree, changes bool, err error) {
	store := g.DB
	ss, err := store.LoadSnapshot(0) // load most recent snapshot
	if err != nil {
		return
	}

	// Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		logger.Debugf("[StoreSCIDInteractionHeight] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, err = store.LoadSnapshot(0) // load most recent snapshot
		if err != nil {
			return
		}
	}

	treename := scid + "heights"
	tree, _ = ss.GetTree(treename)
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-StoreSCIDInteractionHeight] ERROR: Tree is nil for '%v'. Attempting to rollback 1 snapshot", treename)
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return tree, changes, preverr
		}
		tree, terr = prevss.GetTree(treename)
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return tree, changes, terr
		}
	}
	key := scid
	currSCIDInteractionHeight, err := tree.Get([]byte(key))
	var interactionHeight []int64

	var newInteractionHeight []byte

	if err != nil {
		interactionHeight = append(interactionHeight, height)
	} else {
		// Retrieve value and conovert to SCIDInteractionHeight, so that you can manipulate and update db
		_ = json.Unmarshal(currSCIDInteractionHeight, &interactionHeight)

		for _, v := range interactionHeight {
			if v == height {
				// Return nil if already exists in array.
				// Clause for this is in event we pop backwards in time and already have this data stored.
				// TODO: What if interaction happened on false-chain and pop to retain correct chain. Bad data may be stored here still, as it isn't removed. Need fix for this in future.
				return tree, changes, nil
			}
		}
		interactionHeight = append(interactionHeight, height)
	}
	newInteractionHeight, err = json.Marshal(interactionHeight)
	if err != nil {
		return tree, changes, fmt.Errorf("[Graviton] could not marshal interactionHeight info: %v", err)
	}

	tree.Put([]byte(key), newInteractionHeight)
	changes = true
	if !nocommit {
		_, cerr := graviton.Commit(tree)
		if cerr != nil {
			logger.Errorf("[Graviton] ERROR: %v", cerr)
			return tree, changes, cerr
		}
	}
	return tree, changes, nil
}

// Gets SC interaction height and detail by a given SCID
func (g *GravitonStore) GetSCIDInteractionHeight(scid string) (scidinteractions []int64) {
	store := g.DB
	ss, err := store.LoadSnapshot(0) // load most recent snapshot
	if err != nil {
		return
	}

	treename := scid + "heights"
	tree, _ := ss.GetTree(treename) // use or create tree named by poolhost in config
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-GetSCIDInteractionHeight] ERROR: Tree is nil for '%v'. Attempting to rollback 1 snapshot", treename)
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return
		}
		tree, terr = prevss.GetTree(treename)
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return
		}
	}
	key := scid

	v, _ := tree.Get([]byte(key))

	if v != nil {
		_ = json.Unmarshal(v, &scidinteractions)
		return
	}

	return nil
}

func (g *GravitonStore) GetInteractionIndex(topoheight int64, heights []int64, rmax bool) (height int64) {
	if len(heights) <= 0 {
		return height
	}

	// Sort heights so most recent is index 0 [if preferred reverse, just swap > with <]
	sort.SliceStable(heights, func(i, j int) bool {
		return heights[i] > heights[j]
	})

	if topoheight > heights[0] || rmax {
		return heights[0]
	}

	for i := 1; i < len(heights); i++ {
		if heights[i] < topoheight {
			return heights[i]
		} else if heights[i] == topoheight {
			return heights[i]
		}
	}

	return height
}

// Stores any SCIDs that were attempted to be deployed but not correct - log scid/fees burnt attempting it.
func (g *GravitonStore) StoreInvalidSCIDDeploys(scid string, fee uint64, nocommit bool) (tree *graviton.Tree, changes bool, err error) {
	store := g.DB
	ss, err := store.LoadSnapshot(0) // load most recent snapshot
	if err != nil {
		return
	}

	// Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		logger.Debugf("[StoreInvalidSCIDDeploys] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, err = store.LoadSnapshot(0) // load most recent snapshot
		if err != nil {
			return
		}
	}

	treename := "invalidscids"
	tree, _ = ss.GetTree(treename)
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-StoreInvalidSCIDDeploys] ERROR: Tree is nil for '%v'. Attempting to rollback 1 snapshot", treename)
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return tree, changes, preverr
		}
		tree, terr = prevss.GetTree(treename)
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return tree, changes, terr
		}
	}
	key := "invalid"
	currSCIDInteractionHeight, err := tree.Get([]byte(key))
	currInvalidSCIDs := make(map[string]uint64)

	var newInvalidSCIDs []byte

	if err != nil {
		currInvalidSCIDs[scid] = fee
	} else {
		// Retrieve value and conovert to SCIDInteractionHeight, so that you can manipulate and update db
		_ = json.Unmarshal(currSCIDInteractionHeight, &currInvalidSCIDs)

		currInvalidSCIDs[scid] = fee
	}
	newInvalidSCIDs, err = json.Marshal(currInvalidSCIDs)
	if err != nil {
		return tree, changes, fmt.Errorf("[Graviton] could not marshal interactionHeight info: %v", err)
	}

	tree.Put([]byte(key), newInvalidSCIDs)
	changes = true
	if !nocommit {
		_, cerr := graviton.Commit(tree)
		if cerr != nil {
			logger.Errorf("[Graviton] ERROR: %v", cerr)
			return tree, changes, cerr
		}
	}
	return tree, changes, nil
}

// Gets any SCIDs that were attempted to be deployed but not correct and their fees
func (g *GravitonStore) GetInvalidSCIDDeploys() (invalidSCIDs map[string]uint64) {
	invalidSCIDs = make(map[string]uint64)

	store := g.DB
	ss, err := store.LoadSnapshot(0) // load most recent snapshot
	if err != nil {
		return
	}

	treename := "invalidscids"
	tree, _ := ss.GetTree(treename) // use or create tree named by poolhost in config
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-GetInvalidSCIDDeploys] ERROR: Tree is nil for 'invalidscids'. Attempting to rollback 1 snapshot")
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return
		}
		tree, terr = prevss.GetTree("invalidscids")
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return
		}
	}
	key := "invalid"

	v, _ := tree.Get([]byte(key))

	if v != nil {
		_ = json.Unmarshal(v, &invalidSCIDs)
		return
	}

	return nil
}

// Stores the miniblocks within a given blid
func (g *GravitonStore) StoreMiniblockDetailsByHash(blid string, mbldetails []*structures.MBLInfo, nocommit bool) (tree *graviton.Tree, changes bool, err error) {
	for _, v := range mbldetails {
		_, _, err := g.StoreMiniblockCountByAddress(v.Miner, false)
		if err != nil {
			logger.Errorf("[Store] ERR - Error adding miniblock count for address '%v'", v.Miner)
		}
	}

	confBytes, err := json.Marshal(mbldetails)
	if err != nil {
		return &graviton.Tree{}, changes, fmt.Errorf("[StoreMiniblockDetailsByHash] could not marshal getinfo info: %v", err)
	}

	store := g.DB
	ss, err := store.LoadSnapshot(0) // load most recent snapshot
	if err != nil {
		return
	}

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		logger.Debugf("[StoreMiniblockDetailsByHash] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, err = store.LoadSnapshot(0) // load most recent snapshot
		if err != nil {
			return
		}
	}

	tree, _ = ss.GetTree("miniblocks")
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-StoreMiniblockDetailsByHash] ERROR: Tree is nil for 'miniblocks'. Attempting to rollback 1 snapshot")
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return tree, changes, preverr
		}
		tree, terr = prevss.GetTree("miniblocks")
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return tree, changes, terr
		}
	}
	tree.Put([]byte(blid), confBytes) // insert a value
	changes = true
	if !nocommit {
		_, cerr := graviton.Commit(tree)
		if cerr != nil {
			logger.Errorf("[Graviton] ERROR: %v", cerr)
			return tree, changes, cerr
		}
	}
	return tree, changes, nil
}

// Returns all miniblock details for synced chain
func (g *GravitonStore) GetAllMiniblockDetails() (mbldetails map[string][]*structures.MBLInfo) {
	mbldetails = make(map[string][]*structures.MBLInfo)
	store := g.DB
	ss, err := store.LoadSnapshot(0)
	if err != nil {
		return
	}
	tree, _ := ss.GetTree("miniblocks")
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-GetAllMiniblockDetails] ERROR: Tree is nil for 'miniblocks'. Attempting to rollback 1 snapshot")
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return
		}
		tree, terr = prevss.GetTree("miniblocks")
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return
		}
	}

	c := tree.Cursor()
	// Duplicate the LATEST (snapshot 0) to the new DB, this starts the DB over again, but still retaining X number of old DBs for version in future use cases. Here we get the vals before swapping to new db in mem
	for k, v, err := c.First(); err == nil; k, v, err = c.Next() {
		var currdetails []*structures.MBLInfo
		_ = json.Unmarshal(v, &currdetails)
		mbldetails[string(k)] = currdetails
	}

	return
}

// Returns the miniblocks within a given blid if previously stored
func (g *GravitonStore) GetMiniblockDetailsByHash(blid string) (miniblocks []*structures.MBLInfo) {
	store := g.DB
	ss, err := store.LoadSnapshot(0) // load most recent snapshot
	if err != nil {
		return
	}

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		logger.Debugf("[GetMiniblockDetailsByHash] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, err = store.LoadSnapshot(0) // load most recent snapshot
		if err != nil {
			return
		}
	}

	tree, _ := ss.GetTree("miniblocks") // use or create tree named by poolhost in config
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-GetMiniblockDetailsByHash] ERROR: Tree is nil for 'miniblocks'. Attempting to rollback 1 snapshot")
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return
		}
		tree, terr = prevss.GetTree("miniblocks")
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return
		}
	}
	key := blid

	v, _ := tree.Get([]byte(key))

	if v != nil {
		_ = json.Unmarshal(v, &miniblocks)
		return
	}

	return nil
}

// Stores counts of miniblock finders by address
func (g *GravitonStore) StoreMiniblockCountByAddress(addr string, nocommit bool) (tree *graviton.Tree, changes bool, err error) {
	currCount := g.GetMiniblockCountByAddress(addr)

	// Add 1 to currCount
	currCount++

	confBytes, err := json.Marshal(currCount)
	if err != nil {
		return &graviton.Tree{}, changes, fmt.Errorf("[StoreMiniblockCountByAddress] could not marshal getinfo info: %v", err)
	}

	store := g.DB
	ss, err := store.LoadSnapshot(0) // load most recent snapshot
	if err != nil {
		return
	}

	// Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		logger.Debugf("[StoreMiniblockCountByAddress] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, err = store.LoadSnapshot(0) // load most recent snapshot
		if err != nil {
			return
		}
	}

	tree, _ = ss.GetTree("blockcount")
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-StoreMiniblockCountByAddress] ERROR: Tree is nil for 'blockcount'. Attempting to rollback 1 snapshot")
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return tree, changes, preverr
		}
		tree, terr = prevss.GetTree("blockcount")
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return tree, changes, terr
		}
	}
	key := addr
	tree.Put([]byte(key), confBytes) // insert a value
	changes = true
	if !nocommit {
		_, cerr := graviton.Commit(tree)
		if cerr != nil {
			logger.Errorf("[Graviton] ERROR: %v", cerr)
			return tree, changes, cerr
		}
	}
	return tree, changes, nil
}

// Gets counts of miniblock finders by address
func (g *GravitonStore) GetMiniblockCountByAddress(addr string) (miniblocks int64) {
	store := g.DB
	ss, err := store.LoadSnapshot(0) // load most recent snapshot
	if err != nil {
		return
	}

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		logger.Debugf("[GetMiniblockDetailsByHash] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, err = store.LoadSnapshot(0) // load most recent snapshot
		if err != nil {
			return
		}
	}

	tree, _ := ss.GetTree("blockcount") // use or create tree named by poolhost in config
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-GetMiniblockCountByAddress] ERROR: Tree is nil for 'blockcount'. Attempting to rollback 1 snapshot")
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return
		}
		tree, terr = prevss.GetTree("blockcount")
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return
		}
	}
	key := addr

	v, _ := tree.Get([]byte(key))

	if v != nil {
		_ = json.Unmarshal(v, &miniblocks)
		return
	}

	return
}

// Gets all SCID interacts from a given address - non-builtin/name scids.
func (g *GravitonStore) GetSCIDInteractionByAddr(addr string) (scids []string) {
	normTxsWithSCID := g.GetAllNormalTxWithSCIDByAddr(addr)

	// Append scids list of normtxs scid interaction
	for _, v := range normTxsWithSCID {
		if !idExist(scids, v.Scid) {
			scids = append(scids, v.Scid)
		}
	}

	allSCIDs := g.GetAllOwnersAndSCIDs()
	for k, _ := range allSCIDs {
		// Skip builtin name registration, no need to waste cursor time on this one since it's not pertinent to goal of function
		// TODO: Future state, we'll have much more SCID interaction and this will get slower and slower, will need to speedup. Probably will happen with data re-org in future
		if k == "0000000000000000000000000000000000000000000000000000000000000001" {
			continue
		}
		invokedetails := g.GetAllSCIDInvokeDetailsBySigner(k, addr)
		if len(invokedetails) > 0 {
			if !idExist(scids, k) {
				scids = append(scids, k)
			}
		}
	}

	return scids
}

// Commits multiple trees and returns the commit version and errs
func (g *GravitonStore) CommitTrees(trees []*graviton.Tree) (cv uint64, err error) {
	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		logger.Debugf("[GetMiniblockDetailsByHash] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
	}

	cv, err = graviton.Commit(trees...)

	return
}

// Writes to disk RAM-stored data
func (g *GravitonStore) StoreAltDBInput(treenames []string, altdb *GravitonStore) (err error) {
	//store := g.DB
	//ss, err := store.LoadSnapshot(0) // load most recent snapshot
	altss, err := altdb.DB.LoadSnapshot(0)
	if err != nil {
		return
	}

	// Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		logger.Debugf("[StoreAltDBInput] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		/*
			store = g.DB
			ss, err = store.LoadSnapshot(0) // load most recent snapshot
			if err != nil {
				return
			}
		*/
	}

	// Build set of grav trees to commit at once after being processed from ram store.
	var commitTrees []*graviton.Tree
	for _, v := range treenames {
		store := g.DB
		ss, err := store.LoadSnapshot(0) // load most recent snapshot
		if err != nil {
			return err
		}
		//logger.Printf("[StoreAltDBInput] Getting storage tree '%v'", v)
		tree, _ := ss.GetTree(v)
		// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
		if tree == nil {
			var terr error
			logger.Errorf("[Graviton-StoreAltDBInput] ERROR: Tree is nil for '%v'. Attempting to rollback 1 snapshot", v)
			prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
			if preverr != nil {
				return preverr
			}
			tree, terr = prevss.GetTree(v)
			if tree == nil {
				logger.Errorf("[Graviton] ERROR: %v", terr)
				return terr
			}
		}

		//logger.Printf("[StoreAltDBInput] Getting RAM tree '%v'", v)
		alttree, _ := altss.GetTree(v)
		altc := alttree.Cursor()
		var altTreeKV []*TreeKV // Just rk & rv which are of type []byte
		for rk, rv, err := altc.First(); err == nil; rk, rv, err = altc.Next() {
			temp := &TreeKV{rk, rv}
			//logger.Printf("[StoreAltDBInput] Looping through ramtree cursor k: '%v'; v: '%v'", temp.k, temp.v)
			altTreeKV = append(altTreeKV, temp)
		}

		//logger.Printf("[StoreAltDBInput] Looping through ramtree cursor output to input to storage tree. (%v)", len(altTreeKV))
		for _, val := range altTreeKV {
			perr := tree.Put(val.k, val.v)
			if perr != nil {
				logger.Errorf("[Graviton] ERROR: %v", perr)
				return perr
			}
		}

		commitTrees = append(commitTrees, tree)
	}

	// Commit all changed trees at once (single snapshot rather than many)
	_, cerr := graviton.Commit(commitTrees...)
	if cerr != nil {
		logger.Errorf("[Graviton] ERROR: %v", cerr)
		return cerr
	}

	return nil
}

// Stores any SCIDs that were attempted to be deployed but not correct - log scid/fees burnt attempting it.
func (g *GravitonStore) StoreIntegrators(integrator string, nocommit bool) (tree *graviton.Tree, changes bool, err error) {
	store := g.DB
	ss, err := store.LoadSnapshot(0) // load most recent snapshot
	if err != nil {
		return
	}

	// Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		logger.Debugf("[StoreIntegrators] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, err = store.LoadSnapshot(0) // load most recent snapshot
		if err != nil {
			return
		}
	}

	treename := "integrators"
	tree, _ = ss.GetTree(treename)
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-StoreIntegrators] ERROR: Tree is nil for '%v'. Attempting to rollback 1 snapshot", treename)
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return tree, changes, preverr
		}
		tree, terr = prevss.GetTree(treename)
		if tree == nil {
			logger.Errorf("[Graviton] ERROR: %v", terr)
			return tree, changes, terr
		}
	}
	key := "integrators"
	currIntegrators, err := tree.Get([]byte(key))
	newIntegratorsStag := make(map[string]uint64)

	var newIntegrators []byte

	if err != nil {
		newIntegratorsStag[integrator]++
	} else {
		// Retrieve value and conovert, so that you can manipulate and update db
		_ = json.Unmarshal(currIntegrators, &newIntegratorsStag)

		newIntegratorsStag[integrator]++
	}
	newIntegrators, err = json.Marshal(newIntegratorsStag)
	if err != nil {
		return tree, changes, fmt.Errorf("[Graviton] could not marshal interactionHeight info: %v", err)
	}

	tree.Put([]byte(key), newIntegrators)
	changes = true
	if !nocommit {
		_, cerr := graviton.Commit(tree)
		if cerr != nil {
			logger.Errorf("[Graviton] ERROR: %v", cerr)
			return tree, changes, cerr
		}
	}
	return tree, changes, nil
}

// Gets integrators and their counts
func (g *GravitonStore) GetIntegrators() (integrators map[string]int64, err error) {
	store := g.DB
	ss, err := store.LoadSnapshot(0) // load most recent snapshot
	if err != nil {
		return integrators, err
	}

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		logger.Debugf("[GetIntegrators] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, err = store.LoadSnapshot(0) // load most recent snapshot
		if err != nil {
			return integrators, err
		}
	}

	tree, _ := ss.GetTree("integrators") // use or create tree named by poolhost in config
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		logger.Errorf("[Graviton-GetIntegrators] ERROR: Tree is nil for 'stats'. Attempting to rollback 1 snapshot")
		prevss, preverr := store.LoadSnapshot(ss.GetVersion() - 1)
		if preverr != nil {
			return integrators, preverr
		}
		tree, terr = prevss.GetTree("integrators")
		if tree == nil {
			return integrators, fmt.Errorf("[Graviton] ERROR: %v", terr)
		}
	}
	key := "integrators"

	v, _ := tree.Get([]byte(key))

	if v != nil {
		_ = json.Unmarshal(v, &integrators)
		return integrators, err
	}

	return integrators, err
}

// ---- End Application Graviton/Backend functions ---- //
