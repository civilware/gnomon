package wsserver

import (
	"context"
	"fmt"

	"github.com/civilware/Gnomon/indexer"
	"github.com/civilware/Gnomon/structures"
	"github.com/sirupsen/logrus"
)

// Lists indexed SCs (scid, owner/sender, deployheight if possible) with optional param filters by address (sender/installer) and/or scid
// deployheight is returned where possible, an invoke of the installation must have been indexed to validate this data and provide it
// - When address is defined and no SCID, all scid details are returned where the address is the installer
// - When scid is defined and no address, the scid data is returned
// - When scid and address are defined, we tightly filter to the scid and if the address was one that invoked the install
func ListSC(ctx context.Context, p structures.WS_ListSC_Params, indexer *indexer.Indexer) (result structures.WS_ListSC_Result, err error) {
	logger = structures.Logger.WithFields(logrus.Fields{})

	sclist := make(map[string]string)
	switch indexer.DBType {
	case "gravdb":
		sclist = indexer.GravDBBackend.GetAllOwnersAndSCIDs()
	case "boltdb":
		sclist = indexer.BBSBackend.GetAllOwnersAndSCIDs()
	}
	var count int64
	if p.Address != "" && p.SCID == "" { // If address param is passed but no SCID
		for ki, vi := range sclist {
			if vi == p.Address {
				var invokedetails []*structures.SCTXParse
				switch indexer.DBType {
				case "gravdb":
					invokedetails = indexer.GravDBBackend.GetAllSCIDInvokeDetails(ki)
				case "boltdb":
					invokedetails = indexer.BBSBackend.GetAllSCIDInvokeDetails(ki)
				}
				i := 0
				for _, v := range invokedetails {
					sc_action := fmt.Sprintf("%v", v.Sc_args.Value("SC_ACTION", "U"))
					if sc_action == "1" {
						i++
						result.ListSC = append(result.ListSC, v)
					}
				}

				if i == 0 {
					logger.Debugf("No sc_action of '1' for %v", ki)
					result.ListSC = append(result.ListSC, &structures.SCTXParse{Scid: ki, Sender: vi})
					count++
				} else {
					count++
				}
			}
		}
	} else if p.SCID != "" && p.Address == "" { // If SCID param is passed but no address
		for ki, vi := range sclist {
			if ki == p.SCID {
				var invokedetails []*structures.SCTXParse
				switch indexer.DBType {
				case "gravdb":
					invokedetails = indexer.GravDBBackend.GetAllSCIDInvokeDetails(ki)
				case "boltdb":
					invokedetails = indexer.BBSBackend.GetAllSCIDInvokeDetails(ki)
				}
				i := 0
				for _, v := range invokedetails {
					sc_action := fmt.Sprintf("%v", v.Sc_args.Value("SC_ACTION", "U"))
					if sc_action == "1" {
						i++
						result.ListSC = append(result.ListSC, v)
					}
				}

				if i == 0 {
					logger.Debugf("No sc_action of '1' for %v", ki)
					result.ListSC = append(result.ListSC, &structures.SCTXParse{Scid: ki, Sender: vi})
					count++
				} else {
					count++
				}

				// We can break here b/c a SCID is a unique value
				break
			}
		}
	} else if p.SCID != "" && p.Address != "" { // If address and SCID param are passed
		for ki, vi := range sclist {
			if ki == p.SCID {
				var invokedetails []*structures.SCTXParse
				switch indexer.DBType {
				case "gravdb":
					invokedetails = indexer.GravDBBackend.GetAllSCIDInvokeDetails(ki)
				case "boltdb":
					invokedetails = indexer.BBSBackend.GetAllSCIDInvokeDetails(ki)
				}
				i := 0
				for _, v := range invokedetails {
					if v.Sender == p.Address {
						sc_action := fmt.Sprintf("%v", v.Sc_args.Value("SC_ACTION", "U"))
						if sc_action == "1" {
							i++
							result.ListSC = append(result.ListSC, v)
						}
					}
				}

				if i == 0 {
					logger.Debugf("No sc_action of '1' for %v", ki)
					result.ListSC = append(result.ListSC, &structures.SCTXParse{Scid: ki, Sender: vi})
					count++
				} else {
					count++
				}

				// We can break here b/c a SCID is a unique value
				break
			}
		}
	} else { // If no params are passed, return all
		for ki, vi := range sclist {
			result.ListSC = append(result.ListSC, &structures.SCTXParse{Scid: ki, Sender: vi})
		}
	}

	return
}
