package wsserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/civilware/Gnomon/indexer"
	"github.com/civilware/Gnomon/structures"
	"github.com/sirupsen/logrus"
	"golang.org/x/time/rate"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

type WSServer struct {
	srv *http.Server
	mux *http.ServeMux
	sync.RWMutex
	Writer  io.WriteCloser
	Reader  io.Reader
	Indexer *indexer.Indexer
}

var WSS *WSServer = &WSServer{}

// RateLimits
var RLRate = time.Second
var RLBurst = 1

// local logger
var logger *logrus.Entry

// Starts websocket listening for web miners
func ListenWS(bindAddr string, indexer *indexer.Indexer) {
	logger = structures.Logger.WithFields(logrus.Fields{})

	//bindAddr := "127.0.0.1:9190"

	// Err check to ensure address resolves fine
	addr, err := net.ResolveTCPAddr("tcp", bindAddr)
	if err != nil {
		logger.Fatalf("[ListenWS] Error: %v", err)
	}
	_ = addr

	WSS.mux = http.NewServeMux()

	WSS.Lock()
	WSS.srv = &http.Server{Addr: bindAddr, Handler: WSS.mux}
	WSS.Unlock()

	// Assign indexer
	if WSS.Indexer == nil {
		WSS.Lock()
		WSS.Indexer = indexer
		WSS.Unlock()
	} else {
		logger.Errorf("[ListenWS] Cannot assign new indexer to WSS as an Indexer is already assigned.")
	}

	wshandler := func(w http.ResponseWriter, r *http.Request) {
		// TODO - do we need to implement the maximum connections here as well? - need upper end testing/stability confirmation

		var err error

		logger.Printf("[wshandler] %v", w.Header())
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			//OriginPatterns: []string{"127.0.0.1:9090", "127.0.0.1:8080"},
		})
		if err != nil {
			logger.Errorf("[wshandler] Err on connection being established. %v", err)
			return
		}

		logger.Printf("[wshandler] Subprotocol: %v", conn.Subprotocol())

		defer conn.CloseNow()

		// Setup rate limiter for function calls
		l := rate.NewLimiter(rate.Every(RLRate), RLBurst)
		for {
			logger.Debugf("[wshandler] Handling client... %s => %s", r.RemoteAddr, fmt.Sprintf("%s%s", r.Host, r.RequestURI))
			err = WSS.wsHandleClient(r.Context(), conn, l)
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure || websocket.CloseStatus(err) == websocket.StatusGoingAway {
				// We're disconnecting because the we wants to
				logger.Debugf("[wshandler] Websocket close status: %v", websocket.CloseStatus(err))
				conn.Close(websocket.StatusInternalError, fmt.Sprintf("disconnected - %s", err.Error()))
				return
			}
			if err != nil {
				// We're disconnecting because of an err (e.g. invalid method or other)
				logger.Debugf("[wshandler] Disconnected %v: %v", r.RemoteAddr, err)
				conn.Close(websocket.StatusPolicyViolation, fmt.Sprintf("disconnected - %s", err.Error()))
				return
			}
		}
	}

	// Setup handler for /ws directory
	WSS.mux.HandleFunc("/ws", wshandler)

	logger.Printf("[ListenWS] Starting WSServer on %v", bindAddr)

	err = WSS.srv.ListenAndServe()
	if err != nil {
		logger.Fatalf("[ListenWS] Failed to start WSServer: %v", err)
	}
}

func (wss *WSServer) wsHandleClient(ctx context.Context, c *websocket.Conn, l *rate.Limiter) error {
	var err error

	err = l.Wait(ctx)
	if err != nil {
		logger.Errorf("[wsHandleClient] Wait err: %v", err)
		return err
	}

	var req *structures.JSONRpcReq
	//logger.Debugf("[wsHandleClient] Reader")
	// TODO: If we can't guarantee that it's a json buffer, reader hangs until client-side WS disconnects
	err = wsjson.Read(ctx, c, &req)
	if err != nil {
		if err == io.EOF {
			logger.Errorf("[wsHandleClient] io.EOF - disconnected")
		}

		return err
	}

	switch req.Method {
	case "test":
		var params *structures.GnomonSCIDQuery
		pb, err := req.Params.MarshalJSON()
		if err != nil {
			logger.Errorf("[wsHandleClient] Unable to parse params")
		}
		err = json.Unmarshal(pb, &params)
		if err != nil {
			logger.Errorf("[wsHandleClient] Unable to parse params")
			return err
		}

		logger.Debugf("[wsHandleClient] Method: %v", req.Method)
		logger.Debugf("[wsHandleClient] Query: %v", params)

		lh, _ := wss.Indexer.BBSBackend.GetLastIndexHeight()

		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: lh}
		//logger.Debugf("[wsHandleClient] %v Writer", req.Method)
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			logger.Errorf("[wsHandleClient] err writing message: err: %v", err)

			logger.Errorf("[wsHandleClient] Server disconnect request")
			return fmt.Errorf("server disconnect request")
		}
	case "listsc":
		var params structures.WS_ListSC_Params
		pb, err := req.Params.MarshalJSON()
		if err != nil {
			logger.Errorf("[wsHandleClient] Unable to parse params")
		}
		err = json.Unmarshal(pb, &params)
		if err != nil {
			logger.Errorf("[wsHandleClient] Unable to parse params")
			return err
		}

		logger.Debugf("[wsHandleClient] Method: %v", req.Method)
		logger.Debugf("[wsHandleClient] Query: %v", params)

		lh, _ := ListSC(ctx, params, wss.Indexer)

		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: lh}
		//logger.Debugf("[wsHandleClient] %v Writer", req.Method)
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			logger.Debugf("[wsHandleClient] err writing message: err: %v", err)

			logger.Debugf("[wsHandleClient] Server disconnect request")
			return fmt.Errorf("server disconnect request")
		}
	case "listsc_hardcoded":
		logger.Debugf("[wsHandleClient] Method: %v", req.Method)

		lh, _ := ListSCHardcoded(ctx)

		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: lh}
		//logger.Debugf("[wsHandleClient] %v Writer", req.Method)
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			logger.Debugf("[wsHandleClient] err writing message: err: %v", err)

			logger.Debugf("[wsHandleClient] Server disconnect request")
			return fmt.Errorf("server disconnect request")
		}
	case "listsc_code":
		var params structures.WS_ListSCCode_Params
		pb, err := req.Params.MarshalJSON()
		if err != nil {
			logger.Errorf("[wsHandleClient] Unable to parse params")
		}
		err = json.Unmarshal(pb, &params)
		if err != nil {
			logger.Errorf("[wsHandleClient] Unable to parse params")
			return err
		}

		logger.Debugf("[wsHandleClient] Method: %v", req.Method)
		logger.Debugf("[wsHandleClient] Query: %v", params)

		lh, _ := ListSCCode(ctx, params, wss.Indexer)

		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: lh}
		//logger.Debugf("[wsHandleClient] %v Writer", req.Method)
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			logger.Debugf("[wsHandleClient] err writing message: err: %v", err)

			logger.Debugf("[wsHandleClient] Server disconnect request")
			return fmt.Errorf("server disconnect request")
		}
	case "listsc_codematch":
		var params structures.WS_ListSCCodeMatch_Params
		pb, err := req.Params.MarshalJSON()
		if err != nil {
			logger.Errorf("[wsHandleClient] Unable to parse params")
		}
		err = json.Unmarshal(pb, &params)
		if err != nil {
			logger.Errorf("[wsHandleClient] Unable to parse params")
			return err
		}

		logger.Debugf("[wsHandleClient] Method: %v", req.Method)
		logger.Debugf("[wsHandleClient] Query: %v", params)

		lh, _ := ListSCCodeMatch(ctx, params, wss.Indexer)

		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: lh}
		//logger.Debugf("[wsHandleClient] %v Writer", req.Method)
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			logger.Debugf("[wsHandleClient] err writing message: err: %v", err)

			logger.Debugf("[wsHandleClient] Server disconnect request")
			return fmt.Errorf("server disconnect request")
		}
	case "listsc_variables":
		var params structures.WS_ListSCVariables_Params
		pb, err := req.Params.MarshalJSON()
		if err != nil {
			logger.Errorf("[wsHandleClient] Unable to parse params")
		}
		err = json.Unmarshal(pb, &params)
		if err != nil {
			logger.Errorf("[wsHandleClient] Unable to parse params")
			return err
		}

		logger.Debugf("[wsHandleClient] Method: %v", req.Method)
		logger.Debugf("[wsHandleClient] Query: %v", params)

		lh, _ := ListSCVariables(ctx, params, wss.Indexer)

		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: lh}
		//logger.Debugf("[wsHandleClient] %v Writer", req.Method)
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			logger.Debugf("[wsHandleClient] err writing message: err: %v", err)

			logger.Debugf("[wsHandleClient] Server disconnect request")
			return fmt.Errorf("server disconnect request")
		}
	case "listsc_byheight":
		var params structures.WS_ListSCByHeight_Params
		pb, err := req.Params.MarshalJSON()
		if err != nil {
			logger.Errorf("[wsHandleClient] Unable to parse params")
		}
		err = json.Unmarshal(pb, &params)
		if err != nil {
			logger.Errorf("[wsHandleClient] Unable to parse params")
			return err
		}

		logger.Debugf("[wsHandleClient] Method: %v", req.Method)
		logger.Debugf("[wsHandleClient] Query: %v", params)

		lh, _ := ListSCByHeight(ctx, params, wss.Indexer)

		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: lh}
		//logger.Debugf("[wsHandleClient] %v Writer", req.Method)
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			logger.Debugf("[wsHandleClient] err writing message: err: %v", err)

			logger.Debugf("[wsHandleClient] Server disconnect request")
			return fmt.Errorf("server disconnect request")
		}
	default:
		logger.Debugf("[wsHandleClient] Server disconnect request - invalid request method (%s)", req.Method)
		// Sleep rate limit time for response
		time.Sleep(RLRate)
		return fmt.Errorf("invalid request method (%s)", req.Method)
	}

	return err
}
