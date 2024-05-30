### `listsc_variables`

> Lists sc variables at current index height or a given height

#### Params

|Name|Type|Required|Description|
|:--:|:--:|:------:|:---------:|
|scid|String|Required|Supply a scid to return the variables of|
|height|Int64|Optional|Supply a specific height to check the SCID variables at|

#### Request

```go
var pingpong structures.WS_ListSCVariables_Result

params := structures.WS_ListSCVariables_Params{
    SCID:   structures.Hardcoded_SCIDS[0],
    Height: 20,
}

err = Client.RPC.CallResult(context.Background(), method, params, &pingpong)
if err != nil {
    logger.Errorf("ERR - %v", err)
    Client.Connect("127.0.0.1:9190")
}

for k, v := range pingpong.VariableStringKeys {
    logger.Printf("[StringKeys] Key: %s , Value: %v", k, v)
}

for k, v := range pingpong.VariableUint64Keys {
    logger.Printf("[Uint64Keys] Key: %v , Value: %v", k, v)
}
```

#### Response

[Methods](../README.md#methods)