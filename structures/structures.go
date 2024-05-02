package structures

import (
	"encoding/json"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/transaction"
)

type SCTXParse struct {
	Txid       string
	Scid       string
	Scid_hex   []byte
	Entrypoint string
	Method     string
	Sc_args    rpc.Arguments
	Sender     string
	Payloads   []transaction.AssetPayload
	Fees       uint64
	Height     int64
}

type BurnTXParse struct {
	Txid string
	Scid string
	Fees string
}

type NormalTXWithSCIDParse struct {
	Txid   string
	Scid   string
	Fees   uint64
	Height int64
}

type MBLInfo struct {
	Hash  string
	Miner string
}

type APIConfig struct {
	Enabled              bool   `json:"enabled"`
	Listen               string `json:"listen"`
	StatsCollectInterval string `json:"statsCollectInterval"`
	HashrateWindow       string `json:"hashrateWindow"`
	Payments             int64  `json:"payments"`
	Blocks               int64  `json:"blocks"`
	SSL                  bool   `json:"ssl"`
	SSLListen            string `json:"sslListen"`
	GetInfoSSLListen     string `json:"getInfoSSLListen"`
	CertFile             string `json:"certFile"`
	GetInfoCertFile      string `json:"getInfoCertFile"`
	KeyFile              string `json:"keyFile"`
	GetInfoKeyFile       string `json:"getInfoKeyFile"`
	MBLLookup            bool   `json:"mbblookup"`
	ApiThrottle          bool   `json:"apithrottle"`
}

type SCIDVariable struct {
	Key   interface{}
	Value interface{}
}

type FastSyncConfig struct {
	Enabled           bool
	SkipFSRecheck     bool
	ForceFastSync     bool
	ForceFastSyncDiff int64
	NoCode            bool
}

type FastSyncImport struct {
	Owner   string
	Height  uint64
	Headers string
}

type GnomonSCIDQuery struct {
	Owner  string
	Height uint64
	SCID   string
}

type SCIDInteractionHeight struct {
	Heights map[int64]string
}

type BlockTxns struct {
	Topoheight int64
	Tx_hashes  []crypto.Hash
}

type GetInfo rpc.GetInfo_Result

type JSONRpcReq struct {
	Id     *json.RawMessage `json:"id"`
	Method string           `json:"method"`
	Params *json.RawMessage `json:"params"`
}

type JSONRpcResp struct {
	Id      *json.RawMessage `json:"id"`
	Version string           `json:"jsonrpc"`
	Result  interface{}      `json:"result"`
	Error   interface{}      `json:"error"`
}

type IATrack struct {
	Integrator int64
	Installs   int64
	Invokes    int64
}

type InteractionAddrs_Params struct {
	Integrator bool
	Installs   bool
	Invokes    bool
}

// WS struct types
type (
	WS_ListSC_Params struct {
		Address string `json:"address"` // can supply an address for filtering on owner/sc deployer
		SCID    string `json:"scid"`    // can supply a scid for filtering to a specific scid
	}

	WS_ListSC_Result struct {
		ListSC []*SCTXParse `json:"listsc"`
	}
)

type WS_ListSCHardcoded_Result struct {
	SCHardcoded []string `json:"schardcoded"`
}

type (
	WS_ListSCCode_Params struct {
		SCID   string `json:"scid"`   // supply a scid to return the code of
		Height int64  `json:"height"` // supply a specific height to check the SCID code at
	}

	WS_ListSCCode_Result struct {
		Code  string `json:"code"`
		Owner string `json:"owner"`
	}
)

// End WS struct types
