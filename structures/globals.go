package structures

import (
	"github.com/blang/semver/v4"
	"github.com/sirupsen/logrus"
)

// Global logger
var Logger logrus.Logger

// Gnomon Index SCID
const MAINNET_GNOMON_SCID = "a05395bb0cf77adc850928b0db00eb5ca7a9ccbafd9a38d021c8d299ad5ce1a4"
const TESTNET_GNOMON_SCID = "df3a698af94afb46e7f6de40bbb628df2e10f29f79900928524d97f30a1928a2"

// Max API data return for limiting data / load. This is unused if --remove-api-throttle is defined or ApiThrottle is false for structures.ApiConfig
const MAX_API_VAR_RETURN = 1024

// Force FastSync difference object. When utilized, defines how many blocks difference between stored and chain height to determine if fastsync is forced
const FORCE_FASTSYNC_DIFF = int64(100)

// Major.Minor.Patch-Iteration
var Version = semver.MustParse("2.1.0-alpha.2")

// Hardcoded Smart Contracts of DERO Network
// TODO: Possibly in future we can pull this from derohe codebase
var Hardcoded_SCIDS = []string{"0000000000000000000000000000000000000000000000000000000000000001"}
