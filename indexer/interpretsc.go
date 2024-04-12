package indexer

import (
	"github.com/deroproject/derohe/dvm"
)

var okf = []string{"ASSETVALUE", "DEROVALUE"}
var ibfs = []string{"DERO", "SCID", "TXID", "BLID"}

func (indexer *Indexer) InterpretSC(scid string, code string) {

	// Not ready for use yet
	return

	/*
		params := map[string]interface{}{}
		chain, err := blockchain.Blockchain_Start(params)
		if err != nil {
			logger.Error(err, "Error starting blockchain")
			return
		}

			var result rpc.GasEstimate_Result
			var signer *rpc.Address

			var rpcArgs = rpc.Arguments{}
			rpcArgs = append(rpcArgs, rpc.Argument{Name: "entrypoint", DataType: "S", Value: "Tfunc"})
			rpcArgs = append(rpcArgs, rpc.Argument{Name: "SC_ACTION", DataType: "U", Value: rpc.SC_INSTALL})
			rpcArgs = append(rpcArgs, rpc.Argument{Name: "SC_ID", DataType: "H", Value: string([]byte(scid))})

			incoming_values := map[crypto.Hash]uint64{}

			toporecord, err := chain.Store.Topo_store.Read(chain.Load_TOPO_HEIGHT())
			// we must now fill in compressed ring members
			if err == nil {
				var ss *graviton.Snapshot
				ss, err = chain.Store.Balance_store.LoadSnapshot(toporecord.State_Version)
				if err == nil {
					s := dvm.SimulatorInitialize(ss)
					s.SCInstall(code, incoming_values, rpcArgs, signer, 0)
					_ = result
					//_, result.GasCompute, result.GasStorage, err = s.SCInstall(code, incoming_values, rpcArgs, signer, 0)
					//result.GasCompute, result.GasStorage, err = s.RunSC(incoming_values, rpcArgs, signer, 0)
				}
			}
	*/

	contract, _, err := dvm.ParseSmartContract(code)
	if err != nil {
		logger.Errorf("[InterpretSC] ERR on SC Parse SCID '%s' - %v", scid, err)
		return
	}

	d := 0
	for f := range contract.Functions {

		if len(indexer.IBNoHexFuncLineReturn(contract, f, "STORE", ibfs)) > 0 {
			d++
			logger.Debugf("[InterpretSC] SCID: %s", scid)
			logger.Debugf("[InterpretSC] Code: %s", code)
		}
		/*
			for l := range contract.Functions[f].Lines {
				fline := []string{}
				sfunc := false
				cHex := false
				for i := range contract.Functions[f].Lines[l] {
					if (contract.Functions[f].Lines[l][i] == "STORE" && contract.Functions[f].Lines[l][i+1] == "(") || sfunc {
						sfunc = true
						fline = append(fline, contract.Functions[f].Lines[l][i])
						if (contract.Functions[f].Lines[l][i] == "HEX" && contract.Functions[f].Lines[l][i+1] == "(") || cHex {
							cHex = true
							if contract.Functions[f].Lines[l][i] == "TXID" && contract.Functions[f].Lines[l][i+1] == "(" {
								logger.Debugf("[InterpretSC-%s-%v] Hex TXID at slot %v", contract.Functions[f].Name, l, i)
							} else if contract.Functions[f].Lines[l][i] == "SCID" && contract.Functions[f].Lines[l][i+1] == "(" {
								logger.Debugf("[InterpretSC-%s-%v] Hex SCID at slot %v", contract.Functions[f].Name, l, i)
							} else if contract.Functions[f].Lines[l][i] == "BLID" && contract.Functions[f].Lines[l][i+1] == "(" {
								logger.Debugf("[InterpretSC-%s-%v] Hex BLID at slot %v", contract.Functions[f].Name, l, i)
							} else if contract.Functions[f].Lines[l][i] == "DERO" && contract.Functions[f].Lines[l][i+1] == "(" {
								logger.Debugf("[InterpretSC-%s-%v] Hex DERO at slot %v", contract.Functions[f].Name, l, i)
							} else if contract.Functions[f].Lines[l][i] == ")" && contract.Functions[f].Lines[l][i+1] == ")" {
								{
									cHex = false
								}
							}
						} else if contract.Functions[f].Lines[l][i] == "TXID" && contract.Functions[f].Lines[l][i+1] == "(" {
							logger.Debugf("[InterpretSC-%s-%v] Non-Hex TXID at slot %v", contract.Functions[f].Name, l, i)
						} else if contract.Functions[f].Lines[l][i] == "SCID" && contract.Functions[f].Lines[l][i+1] == "(" {
							logger.Debugf("[InterpretSC-%s-%v] Non-Hex SCID at slot %v", contract.Functions[f].Name, l, i)
						} else if contract.Functions[f].Lines[l][i] == "BLID" && contract.Functions[f].Lines[l][i+1] == "(" {
							logger.Debugf("[InterpretSC-%s-%v] Non-Hex BLID at slot %v", contract.Functions[f].Name, l, i)
						} else if contract.Functions[f].Lines[l][i] == "DERO" && contract.Functions[f].Lines[l][i+1] == "(" {
							logger.Debugf("[InterpretSC-%s-%v] Non-Hex DERO at slot %v", contract.Functions[f].Name, l, i)
						}
					}
				}
				if len(fline) > 0 {
					logger.Debugf("[InterpretSC-%s-%v] %s", contract.Functions[f].Name, l, fline)
				}
			}
		*/
	}
	if d > 0 {
		logger.Debugf("[InterpretSC-pause] Pausing...")
		select {}
	}
}

// Returns inbuilt function usage that aren't hexed and their relevant lines
// Used for catching things such as TXID(), SCID(), BLID(), DERO(), etc. which will return hex that need to be converted prior to db storage for future query
func (indexer *Indexer) IBNoHexFuncLineReturn(contract dvm.SmartContract, fname string, bfunc string, ifunc []string) (ibnhflr []uint64) {
	// txid() at line x and slot y; blid() at line x and slot z
	for l := range contract.Functions[fname].Lines {
		sfunc := false
		cHex := false
		cNHex := false
		for i := range contract.Functions[fname].Lines[l] {
			// To prevent index out of bounds, since we compare strings against i+1
			tf := i
			if tf+1 >= len(contract.Functions[fname].Lines[l]) {
				continue
			}

			// Check to see if the function call matches input param bfunc and continue checking logic if that occurs once
			// We can further assume that i references within will be at least of value 2 : 0 for bfunc, 1 for '(', 2+ so on
			if (contract.Functions[fname].Lines[l][i] == bfunc && contract.Functions[fname].Lines[l][i+1] == "(") || sfunc {
				sfunc = true

				for _, ifv := range ifunc {
					if (contract.Functions[fname].Lines[l][i] == "HEX" && contract.Functions[fname].Lines[l][i+1] == "(") || cHex {
						cHex = true
						for _, v := range ifunc {
							if contract.Functions[fname].Lines[l][i] == v && contract.Functions[fname].Lines[l][i+1] == "(" {
								logger.Debugf("[InterpretSC-%s-%v] Hex %s at slot %v", contract.Functions[fname].Name, l, v, i)
							} else if contract.Functions[fname].Lines[l][i] == ")" && contract.Functions[fname].Lines[l][i+1] == ")" {
								cHex = false
							}
						}
						/*
							if contract.Functions[fname].Lines[l][i] == "TXID" && contract.Functions[fname].Lines[l][i+1] == "(" {
								logger.Debugf("[InterpretSC-%s-%v] Hex TXID at slot %v", contract.Functions[fname].Name, l, i)
							} else if contract.Functions[fname].Lines[l][i] == "SCID" && contract.Functions[fname].Lines[l][i+1] == "(" {
								logger.Debugf("[InterpretSC-%s-%v] Hex SCID at slot %v", contract.Functions[fname].Name, l, i)
							} else if contract.Functions[fname].Lines[l][i] == "BLID" && contract.Functions[fname].Lines[l][i+1] == "(" {
								logger.Debugf("[InterpretSC-%s-%v] Hex BLID at slot %v", contract.Functions[fname].Name, l, i)
							} else if contract.Functions[fname].Lines[l][i] == "DERO" && contract.Functions[fname].Lines[l][i+1] == "(" {
								logger.Debugf("[InterpretSC-%s-%v] Hex DERO at slot %v", contract.Functions[fname].Name, l, i)
							} else if contract.Functions[fname].Lines[l][i] == ")" && contract.Functions[fname].Lines[l][i+1] == ")" {
								cHex = false
							}
						*/
					} else if contract.Functions[fname].Lines[l][i] == ifv && contract.Functions[fname].Lines[l][i+1] == "(" {
						if contract.Functions[fname].Lines[l][i-2] == "LOAD" {
							logger.Debugf("[InterpretSC-%s-%v] Non-Hex %s at slot %v attempting to be LOAD from memory, need to check stored value type", contract.Functions[fname].Name, l, ifv, i)
						} else if contract.Functions[fname].Lines[l][i-1] == "(" {
							tf := 0
							for _, vf := range okf {
								if vf == contract.Functions[fname].Lines[l][i-2] {
									logger.Debugf("[InterpretSC-%s-%v] Non-Hex %s at slot %v called within approved function %s . Continuing.", contract.Functions[fname].Name, l, ifv, i, vf)
									tf++
								}
							}
							if tf > 0 {
								continue
							}
							logger.Debugf("[InterpretSC-%s-%v] Non-Hex %s at slot %v attempting to be called within function %s . Is this ok?", contract.Functions[fname].Name, l, ifv, i, contract.Functions[fname].Lines[l][i-2])
						} else {
							logger.Debugf("[InterpretSC-%s-%v] Non-Hex %s at slot %v", contract.Functions[fname].Name, l, ifv, i)
						}
						cNHex = true
					}
					/*
							} else if contract.Functions[fname].Lines[l][i] == "TXID" && contract.Functions[fname].Lines[l][i+1] == "(" {
							if contract.Functions[fname].Lines[l][i-2] == "LOAD" {
								logger.Debugf("[InterpretSC-%s-%v] Non-Hex TXID at slot %v attempting to be LOAD from memory, need to check stored value type", contract.Functions[fname].Name, l, i)
							} else if contract.Functions[fname].Lines[l][i-1] == "(" {
								tf := 0
								for _, vf := range okf {
									if vf == contract.Functions[fname].Lines[l][i-2] {
										logger.Debugf("[InterpretSC-%s-%v] Non-Hex TXID at slot %v called within approved function %s . Continuing.", contract.Functions[fname].Name, l, i, vf)
										tf++
									}
								}
								if tf > 0 {
									continue
								}
								logger.Debugf("[InterpretSC-%s-%v] Non-Hex TXID at slot %v attempting to be called within function %s . Is this ok?", contract.Functions[fname].Name, l, i, contract.Functions[fname].Lines[l][i-2])
							} else {
								logger.Debugf("[InterpretSC-%s-%v] Non-Hex TXID at slot %v", contract.Functions[fname].Name, l, i)
							}
							cNHex = true
						} else if contract.Functions[fname].Lines[l][i] == "SCID" && contract.Functions[fname].Lines[l][i+1] == "(" {
							if contract.Functions[fname].Lines[l][i-2] == "LOAD" {
								logger.Debugf("[InterpretSC-%s-%v] Non-Hex SCID at slot %v attempting to be LOAD from memory, need to check stored value type", contract.Functions[fname].Name, l, i)
							} else if contract.Functions[fname].Lines[l][i-1] == "(" {
								tf := 0
								for _, vf := range okf {
									if vf == contract.Functions[fname].Lines[l][i-2] {
										logger.Debugf("[InterpretSC-%s-%v] Non-Hex SCID at slot %v called within approved function %s . Continuing.", contract.Functions[fname].Name, l, i, vf)
										tf++
									}
								}
								if tf > 0 {
									continue
								}
								logger.Debugf("[InterpretSC-%s-%v] Non-Hex SCID at slot %v attempting to be called within function %s . Is this ok?", contract.Functions[fname].Name, l, i, contract.Functions[fname].Lines[l][i-2])
							} else {
								logger.Debugf("[InterpretSC-%s-%v] Non-Hex SCID at slot %v", contract.Functions[fname].Name, l, i)
							}
							cNHex = true
						} else if contract.Functions[fname].Lines[l][i] == "BLID" && contract.Functions[fname].Lines[l][i+1] == "(" {
							if contract.Functions[fname].Lines[l][i-2] == "LOAD" {
								logger.Debugf("[InterpretSC-%s-%v] Non-Hex BLID at slot %v attempting to be LOAD from memory, need to check stored value type", contract.Functions[fname].Name, l, i)
							} else if contract.Functions[fname].Lines[l][i-1] == "(" {
								tf := 0
								for _, vf := range okf {
									if vf == contract.Functions[fname].Lines[l][i-2] {
										logger.Debugf("[InterpretSC-%s-%v] Non-Hex BLID at slot %v called within approved function %s . Continuing.", contract.Functions[fname].Name, l, i, vf)
										tf++
									}
								}
								if tf > 0 {
									continue
								}
								logger.Debugf("[InterpretSC-%s-%v] Non-Hex BLID at slot %v attempting to be called within function %s . Is this ok?", contract.Functions[fname].Name, l, i, contract.Functions[fname].Lines[l][i-2])
							} else {
								logger.Debugf("[InterpretSC-%s-%v] Non-Hex BLID at slot %v", contract.Functions[fname].Name, l, i)
							}
							cNHex = true
						} else if contract.Functions[fname].Lines[l][i] == "DERO" && contract.Functions[fname].Lines[l][i+1] == "(" {
							if contract.Functions[fname].Lines[l][i-2] == "LOAD" {
								logger.Debugf("[InterpretSC-%s-%v] Non-Hex DERO at slot %v attempting to be LOAD from memory, need to check stored value type", contract.Functions[fname].Name, l, i)
							} else if contract.Functions[fname].Lines[l][i-1] == "(" {
								tf := 0
								for _, vf := range okf {
									if vf == contract.Functions[fname].Lines[l][i-2] {
										logger.Debugf("[InterpretSC-%s-%v] Non-Hex DERO at slot %v called within approved function %s . Continuing.", contract.Functions[fname].Name, l, i, vf)
										tf++
									}
								}
								if tf > 0 {
									continue
								}
								logger.Debugf("[InterpretSC-%s-%v] Non-Hex DERO at slot %v attempting to be called within function %s . Is this ok?", contract.Functions[fname].Name, l, i, contract.Functions[fname].Lines[l][i-2])
							} else {
								logger.Debugf("[InterpretSC-%s-%v] Non-Hex DERO at slot %v", contract.Functions[fname].Name, l, i)
							}
							cNHex = true
						}
					*/
				}
			}
		}

		if cNHex {
			logger.Debugf("[InterpretSC-%s-%v] %s", contract.Functions[fname].Name, l, contract.Functions[fname].Lines[l])
			//nh = true
			ibnhflr = append(ibnhflr, 1)
		}
	}

	return
}
