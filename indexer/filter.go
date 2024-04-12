package indexer

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/civilware/Gnomon/structures"
)

func SplitLineParts(line_parts []string, splitter string) (filt_line_parts [][]string) { //(filt_line_parts []string) {
	// If line_parts is nil, return
	if len(line_parts) == 0 {
		return
	}

	// If line_parts doesn't contain the defined splitter, return back the line_parts
	if !strings.Contains(strings.Join(line_parts, " "), splitter) {
		filt_line_parts = append(filt_line_parts, line_parts[:])
		return
	}

	strjoin := strings.Join(line_parts[:], " ")

	currSubstr := ""
	for _, c := range strjoin {
		if string(c) == splitter {
			filt_line_parts = append(filt_line_parts, strings.Split(strings.Trim(currSubstr, " "), " "))
			currSubstr = splitter
		} else {
			currSubstr += string(c)
		}
	}
	if currSubstr != "|" {
		filt_line_parts = append(filt_line_parts, strings.Split(currSubstr, " "))
	}

	logger.Debugf("[SplitLineParts-0] %s", filt_line_parts[0])
	if len(filt_line_parts) > 0 {
		logger.Debugf("[SplitLineParts-N] %v", filt_line_parts[1:])
	}

	return
}

func (indexer *Indexer) PipeFilter(line_parts []string, invokedetails []*structures.SCTXParse) (details_filtered []*structures.SCTXParse) {
	// Simply check if incoming line parts has '|' to begin, handle multiple within
	if line_parts[0] == "|" {
		if len(line_parts) > 1 {
			// Here we assume there's some modern language usage to filter data which we can manipulate *how* we sort data
			// Example: listscinvoke_byscid d6ad66e39c99520d4ed42defa4643da2d99f297a506d3ddb6c2aaefbe011f3dc | a9bf71fb8561758cd7c5d7507516e05df4fba6c6e7240f9086b7783cfd8a648e
			// Note - Includes references on indexes to d6ad... of string a9bf71...
			// Example2: listscinvoke_byscid d6ad66e39c99520d4ed42defa4643da2d99f297a506d3ddb6c2aaefbe011f3dc | grep a9bf71fb8561758cd7c5d7507516e05df4fba6c6e7240f9086b7783cfd8a648e
			// Note - Includes references on indexes to d6ad... of string a9bf71...
			// Example3: listscinvoke_byscid d6ad66e39c99520d4ed42defa4643da2d99f297a506d3ddb6c2aaefbe011f3dc | exclude a9bf71fb8561758cd7c5d7507516e05df4fba6c6e7240f9086b7783cfd8a648e
			// Note - Excludes references on indexes to d6ad... of string a9bf71...
			// TODO: More elegant + various data type sifting and other. Reference - https://github.com/deroproject/derohe/blob/main/cmd/dero-wallet-cli/easymenu_post_open.go#L290
			switch line_parts[1] {
			case "grep":
				lp := 2
				for _, invoke := range invokedetails {
					if !strings.Contains(fmt.Sprintf("%v", invoke.Sc_args), strings.Join(line_parts[lp:], " ")) && !strings.Contains(fmt.Sprintf("%v", invoke.Sender), strings.Join(line_parts[lp:], " ")) && !strings.Contains(fmt.Sprintf("%v", invoke.Txid), strings.Join(line_parts[lp:], " ")) {
						continue
					}
					details_filtered = append(details_filtered, invoke)
				}
			case "filter":
				lp := 2
				for _, invoke := range invokedetails {
					if !strings.Contains(fmt.Sprintf("%v", invoke.Sc_args), strings.Join(line_parts[lp:], " ")) && !strings.Contains(fmt.Sprintf("%v", invoke.Sender), strings.Join(line_parts[lp:], " ")) && !strings.Contains(fmt.Sprintf("%v", invoke.Txid), strings.Join(line_parts[lp:], " ")) {
						continue
					}
					details_filtered = append(details_filtered, invoke)
				}
			case "find":
				lp := 2
				for _, invoke := range invokedetails {
					if !strings.Contains(fmt.Sprintf("%v", invoke.Sc_args), strings.Join(line_parts[lp:], " ")) && !strings.Contains(fmt.Sprintf("%v", invoke.Sender), strings.Join(line_parts[lp:], " ")) && !strings.Contains(fmt.Sprintf("%v", invoke.Txid), strings.Join(line_parts[lp:], " ")) {
						continue
					}
					details_filtered = append(details_filtered, invoke)
				}
			case "exclude":
				lp := 2
				for _, invoke := range invokedetails {
					if strings.Contains(fmt.Sprintf("%v", invoke.Sc_args), strings.Join(line_parts[lp:], " ")) || strings.Contains(fmt.Sprintf("%v", invoke.Sender), strings.Join(line_parts[lp:], " ")) || strings.Contains(fmt.Sprintf("%v", invoke.Txid), strings.Join(line_parts[lp:], " ")) {
						continue
					}
					details_filtered = append(details_filtered, invoke)
				}
			case "last":
				lp := 2
				details_filtered = append(details_filtered, invokedetails...)

				lh, err := strconv.ParseInt(line_parts[lp], 10, 64)
				if err != nil {
					logger.Errorf("[PipeFilter-last] Cannot convert %s to int64. Not filtering off last.", line_parts[2])
				} else {
					if lh < 0 {
						lh = 0
					} else if lh > int64(len(details_filtered)) {
						lh = int64(len(details_filtered))
					}
					details_filtered = details_filtered[int64(len(details_filtered))-lh:]
				}
			default:
				lp := 1
				for _, invoke := range invokedetails {
					if !strings.Contains(fmt.Sprintf("%v", invoke.Sc_args), strings.Join(line_parts[lp:], " ")) && !strings.Contains(fmt.Sprintf("%v", invoke.Sender), strings.Join(line_parts[lp:], " ")) && !strings.Contains(fmt.Sprintf("%v", invoke.Txid), strings.Join(line_parts[lp:], " ")) {
						continue
					}
					details_filtered = append(details_filtered, invoke)
				}
			}
		} else {
			lp := 1
			for _, invoke := range invokedetails {
				if !strings.Contains(fmt.Sprintf("%v", invoke.Sc_args), strings.Join(line_parts[lp:], " ")) && !strings.Contains(fmt.Sprintf("%v", invoke.Sender), strings.Join(line_parts[lp:], " ")) && !strings.Contains(fmt.Sprintf("%v", invoke.Txid), strings.Join(line_parts[lp:], " ")) {
					continue
				}
				details_filtered = append(details_filtered, invoke)
			}
		}
	} else {
		// Catch all append if no conditions are matched, simply output the expected details that were input to this func
		details_filtered = append(details_filtered, invokedetails...)
	}

	return
}
