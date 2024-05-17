package wsserver

import (
	"context"
	"strings"

	"github.com/civilware/Gnomon/indexer"
	"github.com/civilware/Gnomon/structures"
	"github.com/sirupsen/logrus"
)

// Lists sc code match
// - Checks all stored owner/scid pairs and stored code for the Match string
// - Returns a slice of scid/owner data regarding matching results
func ListSCCodeMatch(ctx context.Context, p structures.WS_ListSCCodeMatch_Params, indexer *indexer.Indexer) (result structures.WS_ListSCCodeMatch_Result, err error) {
	logger = structures.Logger.WithFields(logrus.Fields{})

	sclist := make(map[string]string)
	switch indexer.DBType {
	case "gravdb":
		sclist = indexer.GravDBBackend.GetAllOwnersAndSCIDs()
	case "boltdb":
		sclist = indexer.BBSBackend.GetAllOwnersAndSCIDs()
	}

	for k, v := range sclist {
		var sccode string

		switch indexer.DBType {
		case "gravdb":
			hVars := indexer.GravDBBackend.GetSCIDVariableDetailsAtTopoheight(k, indexer.ChainHeight)
			for _, v := range hVars {
				switch ckey := v.Key.(type) {
				case string:
					if ckey == "C" {
						sccode = v.Value.(string)
					}
				default:
				}
			}
		case "boltdb":
			hVars := indexer.BBSBackend.GetSCIDVariableDetailsAtTopoheight(k, indexer.ChainHeight)
			for _, v := range hVars {
				switch ckey := v.Key.(type) {
				case string:
					if ckey == "C" {
						sccode = v.Value.(string)
					}
				default:
				}
			}
		}

		if sccode == "" {
			_, sccode, _, err = indexer.RPC.GetSCVariables(k, indexer.ChainHeight, nil, nil, nil, true)
		}
		if err != nil {
			logger.Errorf("%v", err)
		}

		if sccode != "" {
			contains := strings.Contains(sccode, p.Match)

			if contains {
				if p.IncludeCode {
					result.Results = append(result.Results, structures.WS_ListSCCodeMatch_SliceResult{SCID: k, Code: sccode, Owner: v})
				} else {
					result.Results = append(result.Results, structures.WS_ListSCCodeMatch_SliceResult{SCID: k, Code: "", Owner: v})
				}
			}
		}
	}

	return
}
