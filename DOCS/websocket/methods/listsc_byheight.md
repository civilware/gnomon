### `listsc_byheight`

> Lists indexed SCs (scid, owner/sender, deployheight if possible) and optionally up to a given height

#### Params

|Name|Type|Required|Description|
|:--:|:--:|:------:|:---------:|
|height|Int64|Optional|Supply a specific height to check for all known installations up to|

#### Request

```go
var pingpong structures.WS_ListSCByHeight_Result

params := structures.WS_ListSCByHeight_Params{
    Height: 20,
}

err = Client.RPC.CallResult(context.Background(), "listsc_byheight", params, &pingpong)
if err != nil {
    logger.Errorf("ERR - %v", err)
    Client.Connect("127.0.0.1:9190")
}

for _, v := range pingpong.ListSCByHeight.ListSC {
    logger.Printf("[Return] %v - %v", v.Txid, v.Height)
}
```

#### Response

[Methods](../README.md#methods)