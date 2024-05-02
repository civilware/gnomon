package main

import (
	"context"
	"os"

	"github.com/civilware/Gnomon/indexer"
	"github.com/civilware/Gnomon/structures"
	"github.com/sirupsen/logrus"
)

// local logger
var logger *logrus.Entry

func main() {
	var err error
	var Client indexer.Client

	// setup logging
	indexer.InitLog(nil, os.Stdout)
	logger = structures.Logger.WithFields(logrus.Fields{})

	// Connect to the /ws endpoint
	err = Client.Connect("127.0.0.1:9190")
	if err != nil {
		logger.Fatalf("[Connect] ERR - %v", err)
	}

	// Simple loop test explicitely testing out the listsc function.
	// TODO: go test files instead
	i := 0
	for {
		if i >= 2 {
			Client.WS.Close()
			break
		}
		var pingpong structures.WS_ListSC_Result

		params := structures.WS_ListSC_Params{Address: "dero1qytygwq00ppnef59l6r5g96yhcvgpzx0ftc0ctefs5td43vkn0p72qqlqrn8z", SCID: "805ade9294d01a8c9892c73dc7ddba012eaa0d917348f9b317b706131c82a2d5"}

		if true {
			err = Client.RPC.CallResult(context.Background(), "listsc", params, &pingpong)
		} else {
			err = Client.RPC.CallResult(context.Background(), "test", params, &pingpong)
		}
		if err != nil {
			logger.Errorf("ERR - %v", err)
			Client.Connect("127.0.0.1:9190")
		}

		for _, v := range pingpong.ListSC {
			logger.Printf("[Return] %v", v.Txid)
		}
		i++
	}
}
