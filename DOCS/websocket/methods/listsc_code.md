### `listsc_code`

> Lists sc code at current index height or a given height

#### Params

|Name|Type|Required|Description|
|:--:|:--:|:------:|:---------:|
|scid|String|Required|Supply a scid to return the code of|
|height|Int64|Optional|Supply a specific height to check the SCID code at|

#### Request

```go
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
```

#### Response

[Methods](../README.md#methods)