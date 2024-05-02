package wsserver

import (
	"context"

	"github.com/civilware/Gnomon/structures"
	"github.com/sirupsen/logrus"
)

// Lists hardcoded SCIDs from the structures package
func ListSCHardcoded(ctx context.Context) (result structures.WS_ListSCHardcoded_Result, err error) {
	logger = structures.Logger.WithFields(logrus.Fields{})

	return structures.WS_ListSCHardcoded_Result{
		SCHardcoded: structures.Hardcoded_SCIDS,
	}, nil
}
