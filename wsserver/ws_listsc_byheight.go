package wsserver

import (
	"context"
	"sort"

	"github.com/civilware/Gnomon/indexer"
	"github.com/civilware/Gnomon/structures"
	"github.com/sirupsen/logrus"
)

// Lists indexed SCs (scid, owner/sender, deployheight if possible) and optionally up to a given height
// deployheight is returned where possible, an invoke of the installation must have been indexed to validate this data and provide it
// - When no parameters are defined, scid data is returned in a formatted list by height
// - When height is defined, scid data is returned up until the given height
// - TODO: This function could be expanded for a variety of height / data inputs e.g. ranges or other to return
func ListSCByHeight(ctx context.Context, p structures.WS_ListSCByHeight_Params, indexer *indexer.Indexer) (result structures.WS_ListSCByHeight_Result, err error) {
	logger = structures.Logger.WithFields(logrus.Fields{})

	listsc, err := ListSC(ctx, structures.WS_ListSC_Params{GetInstalls: true}, indexer)

	// Sort heights so most recent is index 0 [if preferred reverse, just swap > with <]
	sort.SliceStable(listsc.ListSC, func(i, j int) bool {
		return listsc.ListSC[i].Height < listsc.ListSC[j].Height
	})

	// Loop through and filter installations by the height paramter defined
	l := 0
	if p.Height != 0 {
		for _, scinst := range listsc.ListSC {
			if scinst.Height <= int64(p.Height) && scinst.Height != 0 {
				result.ListSCByHeight.ListSC = append(result.ListSCByHeight.ListSC, scinst)
				l++
			}
		}
	} else {
		l = len(listsc.ListSC)
		result.ListSCByHeight.ListSC = listsc.ListSC
	}

	logger.Debugf("scinstalls: %v", l)

	return
}
