package main

import (
	"context"
	"log"
	"os"
	"strconv"

	"github.com/civilware/Gnomon/indexer"
	"github.com/civilware/Gnomon/structures"
	"github.com/docopt/docopt-go"
	"github.com/sirupsen/logrus"
)

var command_line string = `Gnomon
Websocket Server test

Usage:
  wsservertest [options]
  wsservertest -h | --help

Options:
  -h --help     Show this screen.
  --method=<listsc>    Defines the method to call
  --iterations=<1>     Defines number of iterations to call said function`

// local logger
var logger *logrus.Entry

func main() {
	var err error
	var Client indexer.Client
	var method string

	// Inspect argument(s)
	arguments, err := docopt.ParseArgs(command_line, nil, structures.Version.String())
	if err != nil {
		log.Fatalf("[Main] Error while parsing arguments err: %s", err)
	}

	// Handle args
	if arguments["--method"] != nil {
		method = arguments["--method"].(string)
	}

	iteration_count := int64(1)
	if arguments["--iterations"] != nil {
		iteration_count, err = strconv.ParseInt(arguments["--iterations"].(string), 10, 64)
		if err != nil {
			logger.Fatalf("[Main] ERROR while converting --iterations to int64")
			return
		}
	}

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
	i := int64(0)
	for {
		if i >= iteration_count {
			Client.WS.Close()
			break
		}

		switch method {
		case "listsc":
			var pingpong structures.WS_ListSC_Result

			params := structures.WS_ListSC_Params{
				Address: "dero1qytygwq00ppnef59l6r5g96yhcvgpzx0ftc0ctefs5td43vkn0p72qqlqrn8z",
				SCID:    "805ade9294d01a8c9892c73dc7ddba012eaa0d917348f9b317b706131c82a2d5",
			}

			err = Client.RPC.CallResult(context.Background(), method, params, &pingpong)

			if err != nil {
				logger.Errorf("ERR - %v", err)
				Client.Connect("127.0.0.1:9190")
			}

			for _, v := range pingpong.ListSC {
				logger.Printf("[Return] %v", v.Txid)
			}
		case "listsc_hardcoded":
			var pingpong structures.WS_ListSCHardcoded_Result

			err = Client.RPC.CallResult(context.Background(), method, nil, &pingpong)
			if err != nil {
				logger.Errorf("ERR - %v", err)
				Client.Connect("127.0.0.1:9190")
			}

			for _, v := range pingpong.SCHardcoded {
				logger.Printf("[Return] %v", v)
			}
		case "listsc_code":
			var pingpong structures.WS_ListSCCode_Result

			params := structures.WS_ListSCCode_Params{
				SCID: structures.Hardcoded_SCIDS[0],
				//Height: 0,
			}

			err = Client.RPC.CallResult(context.Background(), method, params, &pingpong)
			if err != nil {
				logger.Errorf("ERR - %v", err)
				Client.Connect("127.0.0.1:9190")
			}

			logger.Printf("Owner: %s", pingpong.Owner)
			logger.Printf("Code: %s", pingpong.Code)
		default:
		}
		i++
	}
}
