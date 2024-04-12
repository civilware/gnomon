![](https://github.com/civilware/Gnomon/blob/main/gnomon.png?raw=true)

## Gnomon <!-- omit in toc -->
### Decentralized Search Engine <!-- omit in toc -->

Gnomon is an open source decentralized search engine built on the DERO Decentralized Application Platform. It aims to provide easy access to blockchain data by reading and indexing data received directly from the connected node (daemon). By allowing user provided search terms, Gnomon can query and index any resulting data for use in decentralized applications and more. There are no trusted third-parties, tracking services, or cloud servers that stand between you and your data.

### Core Features <!-- omit in toc -->

* Smart Contract querying and indexing
* Transaction querying and indexing
* Transaction Markup Language (xtag) support
* No custom daemon, no modified code.

<br>
Built for the masses.

<br>

## Table of Contents <!-- omit in toc -->
- [Contributing](#contributing)
- [Documentation](#documentation)
- [GnomonIndexer (CLI)](#gnomonindexer-cli)
  - [GnomonIndexer Run Options](#gnomonindexer-run-options)
    - [CLI Help](#cli-help)
- [Using Gnomon As A Go Package](#using-gnomon-as-a-go-package)
  - [Search Filter(s)](#search-filters)
  - [Setting Up Database](#setting-up-database)
    - [BoltDB](#boltdb)
    - [GravitonDB](#gravitondb)
  - [Defining Your Indexer(s)](#defining-your-indexers)
  - [Reading From DB(s)](#reading-from-dbs)
    - [BoltDB](#boltdb-1)
    - [GravitonDB](#gravitondb-1)
  - [Defining API(s)](#defining-apis)
- [GnomonSC Index Service](#gnomonsc-index-service)
  - [Running the GnomonSC Index Service](#running-the-gnomonsc-index-service)

## Contributing
[Bug fixes](./.github/ISSUE_TEMPLATE/bug_report.md), [feature requests](./.github/ISSUE_TEMPLATE/feature_request.md) etc. can be submitted through normal issues on this repository. Feel free to follow the [Pull Request Template](./.github/pull_request_template.md) for any code merges.

## Documentation

[Documentation](./DOCS) including guides, [ROADMAP](./DOCS/ROADMAP.md), [CHANGELOG](./DOCS/CHANGELOG.md) and more.

## GnomonIndexer (CLI)
The [gnomonindexer](/cmd/gnomonindexer/gnomonindexer.go) command line interface allows for kicking off an indexer to analyze the DERO blockchain transactions. This indexer is primarily used for indexing smart contract interactions and asset transactions.

You have the ability to index at a single block at a time to multiple blocks at a time. Keep in mind that the more you increase --num-parallel-blocks the greater the load on gnomonindexer as well as the daemon you're connected to. The number of parallel blocks is defaulted to 1.

```bash
cd cmd/gnomonindexer
go build .
./gnomonindexer --daemon-rpc-address=127.0.0.1:10102 --num-parallel-blocks=5 --debug
```

### GnomonIndexer Run Options

```
Gnomon
Gnomon Indexing Service: Index DERO's blockchain for Smart Contract deployments/listings/etc. as well as other data analysis.

Usage:
  gnomonindexer [options]
  gnomonindexer -h | --help

Options:
  -h --help     Show this screen.
  --daemon-rpc-address=<127.0.0.1:40402>    Connect to daemon.
  --api-address=<127.0.0.1:8082>     Host api.
  --enable-api-ssl     Enable ssl.
  --api-ssl-address=<127.0.0.1:9092>     Host ssl api.
  --get-info-ssl-address=<127.0.0.1:9394>     Host GetInfo ssl api. This is to completely isolate it from gnomon api results as a whole. Normal api endpoints also surface the getinfo call if needed.
  --start-topoheight=<31170>     Define a start topoheight other than 1 if required to index at a higher block (pruned db etc.).
  --search-filter=<"Function InputStr(input String, varname String) Uint64">     Defines a search filter to match on installed SCs to add to validated list and index all actions, this will most likely change in the future but can allow for some small variability. Include escapes etc. if required. If nothing is defined, it will pull all (minus hardcoded sc).
  --runmode=<daemon>     Defines the runmode of gnomon (daemon/wallet/asset). By default this is daemon mode which indexes directly from the chain. Wallet mode indexes from wallet tx history (use/store with caution).
  --enable-miniblock-lookup     True/false value to store all miniblocks and their respective details and miner addresses who found them. This currently REQUIRES a full node db in same directory
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
  --debug     Enables debug logging
```

#### CLI Help
By typing ```help``` via the cli gnomonindexer you can print the below to utilize to interact with the index db.

```
commands:
	help		this help
	version		Show gnomon version
	listsc		Lists all indexed scids that match original search filter and optionally filtered by owner or scid via input. listsc || listsc <owneraddress> || listsc <scid> | ... | grep <stringmatch>
	listsc_hardcoded		Lists all hardcoded scids
	listsc_code		Lists SCID code, listsc_code <scid>
	listsc_codematch		Lists SCIDs that match a given search string, listsc_codematch <Test Search String>
	listsc_variables		Lists SCID variables at latest height unless optionally defining a height, listsc_variables <scid> <height>
	listsc_byheight	List all indexed scids that match original search filter including height deployed and optionally filter by maxheight, listsc_byheight || listsc_byheight <maxheight> || ... | grep <stringmatch>
	listsc_balances	Lists balances of SCIDs that are greater than 0 or of a specific scid if specified, listsc_balances || listsc_balances <scid>
	listscinvoke_byscid	Lists a scid/owner pair of a defined scid and any invokes. Optionally limited to a specified minimum height, listscinvoke_byscid <scid> || listscinvoke_byscid <scid> <minheight> || ... | grep <stringmatch>
	listscinvoke_byentrypoint	Lists sc invokes by entrypoint, listscinvoke_byentrypoint <scid> <entrypoint> || ... | grep <stringmatch>
	listscinvoke_byinitialize	Lists all calls to SCs that attempted to run Initialize() or InitializePrivate() or to a specific SC is defined, listscinvoke_byinitialize || listscinvoke_byinitialize <scid> || ... | grep <stringmatch>
	listscinvoke_bysigner	Lists all sc invokes that match a given signer or partial signer address and optionally by scid, listscinvoke_bysigner <signerstring> || listscinvoke_bysigner <signerstring> <scid> || ... | grep <stringmatch>
	listscidkey_byvaluestored	List keys in a SC that match a given value by pulling from gnomon database, listscidkey_byvaluestored <scid> <value>
	listscidkey_byvaluelive	List keys in a SC that match a given value by pulling from daemon, listscidkey_byvaluelive <scid> <value>
	listscidvalue_bykeystored	List keys in a SC that match a given value by pulling from gnomon database, listscidvalue_bykeystored <scid> <key>
	listscidvalue_bykeylive	List keys in a SC that match a given value by pulling from daemon, listscidvalue_bykeylive <scid> <key>
	validatesc	Validates a SC looking for a 'signature' k/v pair containing DERO signature validating the code matches the signature, validatesc <scid>
	addscid_toindex	Add a SCID to index list/validation filter manually, addscid_toindex <scid>
	getscidlist_byaddr	Gets list of scids that addr has interacted with, getscidlist_byaddr <addr>
	countinvoke_burnvalue	Lists a scid/owner pair of a defined scid and any invokes then calculates any burnvalue for them. Optionally limited to a specified minimum height or string match filter on args, countinvoke_burnvalue <scid> || countinvoke_burnvalue <scid> <minheight> || ... | grep <stringmatch>
	diffscid_code	Runs a difference for SC code at one height vs another, diffscid_code <scid> <startHeight> <endHeight>
	list_interactionaddrs	Gets interaction addresses, list_interactionaddrs
	pop	Rolls back lastindexheight, pop <100>
	status		Show general information
	gnomonsc		Show scid of gnomon index scs
	bye		Quit the daemon
	exit		Quit the daemon
	quit		Quit the daemon
```

## Using Gnomon As A Go Package
Gnomon is written with the expectation that the primary use case would be consuming it as a go package. The basis is to be able to leverage it for your own dApps or other configurations which may need to track specific contracts or data and use it appropriately. You can also use it as a [standalone command line interface](#gnomonindexer).

### Search Filter(s)
```go
// Search filter - can be "" for any/everything, or specific to a piece of code in your template.bas files etc.
search_filter := "Function InitializePrivate() Uint64"

// You can also define multiple with the ';;;' separator
search_filter := "InitializePrivate;;;SEND_DERO_TO_ADDRESS;;;SEND_ASSET_TO_ADDRESS"
```

### Setting Up Database
#### BoltDB
```go
import "github.com/civilware/Gnomon/storage"
...

var Graviton_backend *storage.GravitonStore
var Bbs_backend *storage.BboltStore

// Database
var shasum string
if len(search_filter) == 0 {
  shasum = fmt.Sprintf("%x", sha1.Sum([]byte("gnomon")))
} else {
  csearch_filter = strings.Join(search_filter, sf_separator)
  shasum = fmt.Sprintf("%x", sha1.Sum([]byte(csearch_filter)))
}

db_name := fmt.Sprintf("%s_%s.db", "GNOMON", shasum)
wd, err := os.Getwd()
if err != nil {
  logger.Fatalf("[Main] Err getting working directory: %v", err)
}
db_path := filepath.Join(wd, "gnomondb")
Bbs_backend, err = storage.NewBBoltDB(db_path, db_name)
if err != nil {
  logger.Fatalf("[Main] Err creating boltdb: %v", err)
}
```

#### GravitonDB
```go
import "github.com/civilware/Gnomon/storage"
...

var Graviton_backend *storage.GravitonStore
var Bbs_backend *storage.BboltStore

// Database
var shasum string
var shasum string
if len(search_filter) == 0 {
  shasum = fmt.Sprintf("%x", sha1.Sum([]byte("gnomon")))
} else {
  csearch_filter = strings.Join(search_filter, sf_separator)
  shasum = fmt.Sprintf("%x", sha1.Sum([]byte(csearch_filter)))
}
db_folder := fmt.Sprintf("gnomondb\\%s_%s", "GNOMON", shasum)
current_path, err := os.Getwd()
if err != nil {
  logger.Fatalf("[Main] Err getting working directory: %v", err)
}
db_path := filepath.Join(current_path, db_folder)
Graviton_backend := storage.NewGravDB(db_path, "25ms")
if err != nil {
  logger.Fatalf("[Main] Err creating gravdb: %v", err)
}
```

### Defining Your Indexer(s)
```go
import "github.com/civilware/Gnomon/indexer"
...

// dbtype defines what type of backend db will be used for storage. boltdb being the default and gravitondb being another option.
dbtype := "boltdb"

// Last IndexedHeight - This can be used in addition to the db store for picking up where you left off. 
// Graviton_backend.StoreLastIndexHeight(LastIndexedHeight)  - either on-close or in a channel interval etc.
// Good thing about chain data, even if you didn't store, worst that'll happen is it re-scans and stores same info over itself
last_indexedheight := int64(1)

// Daemon Endpoint - 127.0.0.1:10102 (local) or remote IP etc. Change port if using testnet (i.e. 40402)
daemon_endpoint := "127.0.0.1:10102"

// RunMode - Daemon, Asset and Wallet. As of this release Daemon and Asset are the only ones doing any actions - wallet may be in the future.
// Daemon - Normal run operations indexing against the daemon.
// Asset - Lightweight operations indexing against the daemon which sole purpose is to grab initial data on hardcoded scids as well as a list of scids, owners, heights and code which are existing for reference.
runmode := "daemon"

// mbl - More of a 'bonus' feature - tracks/stores miniblock propogation on-chain as blocks are scanned. Boolean value to enable/disable this
// NOTE - This REQUIRES the same directory as your  DERODB etc. I'd suggest checking out binary implementation of this and adjust accordingly, most likely not used through packaging (but could be)
mbl := false

// storeintegrators - 'bonus' feature to store the integrators along the way as blocks are indexed
storeintegrators := false

// closeondisconnect - More of a 'specific' use case feature - if daemon connectivity (after previously being connected) ceases for x time, then close the indexer which eventually panics. Will be cleaner in future or removed with upstream daemons being pooled. Primary use case is to ensure api is disconnected when daemon is not connected for bad data, could accomplish other ways.
closeondisconnect := false

// fastsync - Syncs against gnomon scid for scids and compares against search_filter, starts you at current topoheight
fsc = &structures.FastSyncConfig{
  Enabled:       false,    // fastsyncs SC index from gnomon sc. If no previous lastindexheight then fastsync will start syncing from chain height. Otherwise will continue from lastindexheight as well as pull from gnomonsc, unless forcefastsync is also defined.
  SkipFSRecheck: false,    // skips re-validation efforts on index data when fastsync is enabled
  ForceFastSync: false,    // forces fastsync and stored index catchup when last stored blockheight is > ForceFastSyncDiff value (default 100) from chain height
  ForceFastSyncDiff: 100,  // // Force FastSync difference object. When utilized, defines how many blocks difference between stored and chain height to determine if fastsync is forced
  NoCode:        false,    // defines whether to index sc code or not when skipfsrecheck is utilized
}

// sfscidexclusion - A defined string array of SCIDs to exclude index operations on. This would primarily be used to skip over say the gnomonsc indexes (can also be leveraged with CLI gnomonindexer via --skip-gnomonsc-index) or any other specific SCIDs that may match a search_filter but you don't want to log their indexing operations.
var sfscidexclusion []string
sfscidexclusions = append(sfscidexclusion, structures.MAINNET_GNOMON_SCID)

// logrus logging init (optional)
indexer.InitLog(arguments, os.Stdout)

// Indexer
defaultIndexer := indexer.NewIndexer(Graviton_backend, Bbs_backend, dbtype, search_filter, last_indexedheight, daemon_endpoint, runmode, mbl, closeondisconnect, fsc, sfscidexclusion, storeintegrators)
```

### Reading From DB(s)
#### BoltDB
[boltdb](https://github.com/etcd-io/bbolt)
```go
import "github.com/civilware/Gnomon/storage"
...

// Examples:
// Return search_filter validated SCIDs and their respective owners (if it could be parsed [ringsize = 2])
validatedSCIDs := Bbs_backend.GetAllOwnersAndSCIDs()

// Get last indexed height for continuing along
last_indexedheight, err := Bbs_backend.GetLastIndexHeight()

// Get all SC transfer txs that are 'normal' transfers by a given address
allNormalTxWithSCID := Bbs_backend.GetAllNormalTxWithSCIDByAddr("address")

// Get all SCID invokes 
allSCIDInvokes := Bbs_backend.GetAllSCIDInvokeDetails("scid")

// And so on... get functions within bbolt.go
```

#### GravitonDB
[Graviton](https://github.com/deroproject/graviton)
```go
import "github.com/civilware/Gnomon/storage"
...

// Examples:
// Return search_filter validated SCIDs and their respective owners (if it could be parsed [ringsize = 2])
validatedSCIDs := Graviton_backend.GetAllOwnersAndSCIDs()

// Get last indexed height for continuing along
last_indexedheight, _ := Graviton_backend.GetLastIndexHeight()

// Get all SC transfer txs that are 'normal' transfers by a given address
allNormalTxWithSCID := Graviton_backend.GetAllNormalTxWithSCIDByAddr("address")

// Get all SCID invokes 
allSCIDInvokes := Graviton_backend.GetAllSCIDInvokeDetails("scid")

// And so on... get functions within gravdb.go
```

### Defining API(s)
```go
import "github.com/civilware/Gnomon/structures"
...

// API
api_endpoint := "127.0.0.1:8082"    // Listening non-ssl api endpoint
sslenabled := false // Enable/disable ssl listener
api_ssl_endpoint := "127.0.0.1:9092"    // API SSL endpoint
get_info_ssl_endpoint := "127.0.0.1:9394"   // Extra singular listener for get_info hosting on SSL
mbl := false    // asks api to host these queryable endpoints for mbl details
api_throttle = false  // If enabled, throttle responses from API to a limit defined by structures.MAX_API_VAR_RETURN . This may be valuable for public-facing query nodes.
apic := &structures.APIConfig{
    Enabled:              true,
    Listen:               api_endpoint,
    StatsCollectInterval: "5s",
    SSL:                  sslenabled,
    SSLListen:            api_ssl_endpoint,
    GetInfoSSLListen:     get_info_ssl_endpoint,
    CertFile:             "fullchain.cer",  // Cert file for api ssl
    GetInfoCertFile:      "getinfofullchain.cer",   // Cert file for getinfo ssl
    KeyFile:              "cert.key",   // Key file for api ssl
    GetInfoKeyFile:       "getinfocert.key",    // Key file for getinfo ssl
    MBLLookup:            mbl,
    ApiThrottle:          api_throttle,
}
```

## GnomonSC Index Service
The [gnomonsc](/cmd/gnomonsc/gnomonsc.go) command line interface allows for setting up an index service which will index SCs based on an input search filter (or all if not defined) and store the SC height, owner and scid within the [contract](/cmd/gnomonsc/contracts/contract.bas). Today this is handled by a specific gnomon address which is more widely consumed throughout this package for things such as fastsync etc.

This configuration is pretty bruteforce in nature to define all of the SCID details that are relevant within another SC for lookup. Future states of this component may include a more chunked approach which could potentially be stored off-chain for consumption, however plans and roadmap for that is still a work in progress.

### Running the GnomonSC Index Service

```bash
cd cmd/gnomonsc
go build .
# We are assuming that a daemon, wallet (with no username/pwd [TODO: not ideal and do not use anything outside of your same system]), and gnomon indexer are running
# Say we want to auto-index any SCs that match sending an asset in any form
./gnomonsc --daemon-rpc-address=127.0.0.1:10102 --wallet-rpc-address=127.0.0.1:10103 --gnomon-api-address=127.0.0.1:8082 --block-deploy-buffer=5 --search-filter="SEND_ASSET_TO_ADDRESS"
```