package wsserver

import (
	"context"
	"fmt"

	"github.com/civilware/Gnomon/indexer"
	"github.com/civilware/Gnomon/structures"
	"github.com/sirupsen/logrus"
)

// Lists sc code at current index height or a given height
// - When SCID is provided and no height, code is returned from store or daemon RPC directly at the latest value.
// - - TODO: Check handling of stored bits for non-fs => fs skipping over possible invokes. Ensure latest is pulled/compared on fs procedures
// - When SCID and height are both provided, code is returned from store or daemon RPC directly at the nearest value.
func ListSCCode(ctx context.Context, p structures.WS_ListSCCode_Params, indexer *indexer.Indexer) (result structures.WS_ListSCCode_Result, err error) {
	logger = structures.Logger.WithFields(logrus.Fields{})

	if p.Height <= 0 && p.SCID != "" {
		var owner string
		var sccode string

		switch indexer.DBType {
		case "gravdb":
			owner = indexer.GravDBBackend.GetOwner(p.SCID)
			hVars := indexer.GravDBBackend.GetSCIDVariableDetailsAtTopoheight(p.SCID, indexer.ChainHeight)
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
			owner = indexer.BBSBackend.GetOwner(p.SCID)
			hVars := indexer.BBSBackend.GetSCIDVariableDetailsAtTopoheight(p.SCID, indexer.ChainHeight)
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
			_, sccode, _, err = indexer.RPC.GetSCVariables(p.SCID, indexer.ChainHeight, nil, nil, nil, true)
		}
		if err != nil {
			logger.Errorf("%v", err)
		}

		if sccode != "" {
			return structures.WS_ListSCCode_Result{Code: sccode, Owner: owner}, nil
		} else {
			return structures.WS_ListSCCode_Result{}, fmt.Errorf("SCID '%s' code was unable to be retrieved. Is it installed?", p.SCID)
		}
	} else if p.Height > 0 && p.SCID != "" {
		var owner string
		var sccode string

		switch indexer.DBType {
		case "gravdb":
			owner = indexer.GravDBBackend.GetOwner(p.SCID)
			hVars := indexer.GravDBBackend.GetSCIDVariableDetailsAtTopoheight(p.SCID, p.Height)
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
			owner = indexer.BBSBackend.GetOwner(p.SCID)
			hVars := indexer.BBSBackend.GetSCIDVariableDetailsAtTopoheight(p.SCID, p.Height)
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
			_, sccode, _, err = indexer.RPC.GetSCVariables(p.SCID, p.Height, nil, nil, nil, true)
		}
		if err != nil {
			logger.Errorf("%v", err)
		}

		if sccode != "" {
			return structures.WS_ListSCCode_Result{Code: sccode, Owner: owner}, nil
		} else {
			return structures.WS_ListSCCode_Result{}, fmt.Errorf("SCID '%s' code was unable to be retrieved at height '%v'. Is it installed?", p.SCID, p.Height)
		}
	} else {
		return structures.WS_ListSCCode_Result{}, fmt.Errorf("No SCID provided")
	}
}
