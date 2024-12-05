package wsserver

import (
	"context"
	"sort"

	"github.com/civilware/Gnomon/indexer"
	"github.com/civilware/Gnomon/structures"
	"github.com/sirupsen/logrus"
)

// Lists indexed SCs (scid, owner/sender, deployheight if possible) and optionally within parameters of input height data
// deployheight is returned where possible, an invoke of the installation must have been indexed to validate this data and provide it
// - When no parameters are defined, scid data is returned in a formatted list by height
// - When SortDesc is defined, scid data is returned in a descending order by height
// - When heightmax is defined and heightmin is not, scid data is returned up until the given heightmax
// - When heightmin is defined and heightmax is not, scid data is returned since the given heightmin
// - When both heightmin and heightmax are defined, scid data is returned within the range of heightmin and heightmax
func ListSCByHeight(ctx context.Context, p structures.WS_ListSCByHeight_Params, indexer *indexer.Indexer) (result structures.WS_ListSCByHeight_Result, err error) {
	logger = structures.Logger.WithFields(logrus.Fields{})

	sclist := make(map[string]string)
	switch indexer.DBType {
	case "gravdb":
		sclist = indexer.GravDBBackend.GetAllOwnersAndSCIDs()
	case "boltdb":
		sclist = indexer.BBSBackend.GetAllOwnersAndSCIDs()
	}

	var tResult structures.WS_ListSCByHeight_Result
	for ki, vi := range sclist {
		switch indexer.DBType {
		case "gravdb":
			// Check to see if install height details are present - implemented in gnomon v2.1.0-alpha.2
			insth := indexer.GravDBBackend.GetInstallHeight(ki)

			// InstallHeight not present directly, fallback to installsc refs
			if insth == 0 {
				//logger.Debugf("[ListSCByHeight] Could not get install height for scid '%s' from GetInstallHeight. Trying ListSC queries.", ki)

				listsc, err := ListSC(ctx, structures.WS_ListSC_Params{GetInstalls: true, SCID: ki}, indexer)
				if err != nil {
					logger.Debugf("[ListSCByHeight] Could not get install height for scid '%s' from either GetInstallHeight nor ListSC queries.", ki)
				}
				insth = listsc.ListSC[0].Height
			} else {
				//logger.Debugf("[ListSCByHeight] Found install height for scid '%s' from GetInstallHeight - %v.", ki, insth)
			}

			tResult.ListSCByHeight = append(tResult.ListSCByHeight, structures.GnomonSCIDQuery{Owner: vi, Height: uint64(insth), SCID: ki})
		case "boltdb":
			// Check to see if install height details are present - implemented in gnomon v2.1.0-alpha.2
			insth := indexer.BBSBackend.GetInstallHeight(ki)

			// InstallHeight not present directly, fallback to installsc refs
			if insth == 0 {
				//logger.Debugf("[ListSCByHeight] Could not get install height for scid '%s' from GetInstallHeight. Trying ListSC queries.", ki)

				listsc, err := ListSC(ctx, structures.WS_ListSC_Params{GetInstalls: true, SCID: ki}, indexer)
				if err != nil {
					logger.Debugf("[ListSCByHeight] Could not get install height for scid '%s' from either GetInstallHeight nor ListSC queries.", ki)
				}
				insth = listsc.ListSC[0].Height
			} else {
				//logger.Debugf("[ListSCByHeight] Found install height for scid '%s' from GetInstallHeight - %v.", ki, insth)
			}

			tResult.ListSCByHeight = append(tResult.ListSCByHeight, structures.GnomonSCIDQuery{Owner: vi, Height: uint64(insth), SCID: ki})
		}
	}

	// Loop through and filter installations by the height paramter defined
	l := 0
	if p.HeightMax != 0 && p.HeightMin == 0 {
		for _, scinst := range tResult.ListSCByHeight {
			if scinst.Height <= uint64(p.HeightMax) && scinst.Height != 0 {
				result.ListSCByHeight = append(result.ListSCByHeight, scinst)
				l++
			}
		}
	} else if p.HeightMax == 0 && p.HeightMin != 0 {
		for _, scinst := range tResult.ListSCByHeight {
			if scinst.Height >= uint64(p.HeightMin) && scinst.Height != 0 {
				result.ListSCByHeight = append(result.ListSCByHeight, scinst)
				l++
			}
		}
	} else if p.HeightMax != 0 && p.HeightMin != 0 {
		for _, scinst := range tResult.ListSCByHeight {
			if scinst.Height >= uint64(p.HeightMin) && scinst.Height <= uint64(p.HeightMax) && scinst.Height != 0 {
				result.ListSCByHeight = append(result.ListSCByHeight, scinst)
				l++
			}
		}
	} else {
		l = len(tResult.ListSCByHeight)
		result.ListSCByHeight = tResult.ListSCByHeight
	}

	// Sort heights so most recent is index 0 [if preferred reverse, just swap > with <]
	sort.SliceStable(result.ListSCByHeight, func(i, j int) bool {
		if p.SortDesc {
			return result.ListSCByHeight[i].Height > result.ListSCByHeight[j].Height
		} else {
			return result.ListSCByHeight[i].Height < result.ListSCByHeight[j].Height
		}
	})

	logger.Debugf("scinstalls: %v", l)

	return
}
