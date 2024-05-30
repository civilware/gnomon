package wsserver

import (
	"context"
	"fmt"

	"github.com/civilware/Gnomon/indexer"
	"github.com/civilware/Gnomon/structures"
	"github.com/sirupsen/logrus"
)

// Lists sc variables at current index height or a given height
// - When SCID is provided and no height, variables are returned from store or daemon RPC directly at the latest value.
// - - TODO: Check handling of stored bits for non-fs => fs skipping over possible invokes. Ensure latest is pulled/compared on fs procedures
// - When SCID and height are both provided, variables are returned from store or daemon RPC directly at the nearest value.
// - NOTE: 'C' (aka the sc code) is not returned on purpose for simplicity and smaller data returns. Use ListSCCode for that data if required
// - - TODO: We *could* have a code bool to also include potentially if we wanted to like existing rpc call does, but can evaluate need.
func ListSCVariables(ctx context.Context, p structures.WS_ListSCVariables_Params, indexer *indexer.Indexer) (result structures.WS_ListSCVariables_Result, err error) {
	logger = structures.Logger.WithFields(logrus.Fields{})

	result.VariableStringKeys = make(map[string]interface{})
	result.VariableUint64Keys = make(map[uint64]interface{})

	if p.Height <= 0 && p.SCID != "" {
		vars, _, _, err := indexer.RPC.GetSCVariables(p.SCID, indexer.ChainHeight, nil, nil, nil, false)
		if err != nil {
			logger.Errorf("%v", err)
		}

		if len(vars) > 0 {
			for _, vvar := range vars {
				switch vvar.Key.(type) {
				case string:
					if vvar.Key.(string) == "C" {
						continue
					}

					result.VariableStringKeys[vvar.Key.(string)] = vvar.Value
				default:
					result.VariableUint64Keys[vvar.Key.(uint64)] = vvar.Value
				}
			}
		} else {
			return structures.WS_ListSCVariables_Result{}, fmt.Errorf("No non-C variables found for SCID '%s'", p.SCID)
		}
	} else if p.Height > 0 && p.SCID != "" {
		vars, _, _, err := indexer.RPC.GetSCVariables(p.SCID, p.Height, nil, nil, nil, false)
		if err != nil {
			logger.Errorf("%v", err)
		}

		if len(vars) > 0 {
			for _, vvar := range vars {
				switch vvar.Key.(type) {
				case string:
					if vvar.Key.(string) == "C" {
						continue
					}

					result.VariableStringKeys[vvar.Key.(string)] = vvar.Value
				default:
					result.VariableUint64Keys[vvar.Key.(uint64)] = vvar.Value
				}
			}
		} else {
			return structures.WS_ListSCVariables_Result{}, fmt.Errorf("No non-C variables found for SCID '%s'", p.SCID)
		}
	} else {
		return structures.WS_ListSCVariables_Result{}, fmt.Errorf("No SCID provided")
	}

	return
}
