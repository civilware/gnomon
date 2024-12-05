### `listsc_byheight`

> Lists indexed SCs (scid, owner/sender, deployheight if possible) and optionally within parameters of input height data

#### Params

|Name|Type|Required|Description|
|:--:|:--:|:------:|:---------:|
|heightmax|Int64|Optional|Supply a specific height to check for all known installations up to|
|heightmin|Int64|Optional|Supply a specific height to check for all known installations since specified height|
|sortdesc|Bool|Optional|If set to true, height list return will be sorted in descending order (e.g. index 0 is the lowest/oldest height)|

#### Request

```go
var pingpong structures.WS_ListSCByHeight_Result

params := structures.WS_ListSCByHeight_Params{
    HeightMax: 20,
    SortDesc: true,
}

err = Client.RPC.CallResult(context.Background(), "listsc_byheight", params, &pingpong)
if err != nil {
    logger.Errorf("ERR - %v", err)
    Client.Connect("127.0.0.1:9190")
}

for _, v := range pingpong.ListSCByHeight {
    logger.Printf("[Return] %v - %v - %v", v.SCID, v.Height, v.Owner)
}
```

#### Response

[Methods](../README.md#methods)