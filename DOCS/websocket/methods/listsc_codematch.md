### `listsc_codematch`

> Lists scs which their current code matches a given input string 'match'

#### Params

|Name|Type|Required|Description|
|:--:|:--:|:------:|:---------:|
|match|String|Optional|Supply a match string to check SC code returns against|
|includecode|Boolean|Optional|Supply back the sccode with the results, this is a larger dataset return|

#### Request

```go
var pingpong structures.WS_ListSCCodeMatch_Result

params := structures.WS_ListSCCodeMatch_Params{
    Match:       "Civilware",
    IncludeCode: false,
}

err = Client.RPC.CallResult(context.Background(), "listsc_codematch", params, &pingpong)
if err != nil {
    logger.Errorf("ERR - %v", err)
    Client.Connect("127.0.0.1:9190")
}

for _, v := range pingpong.Results {
    if params.IncludeCode {
        logger.Printf("SCID: %s, Owner: %s, Code: %s", v.SCID, v.Owner, v.Code)
    } else {
        logger.Printf("SCID: %s, Owner: %s", v.SCID, v.Owner)
    }
}
```

#### Response

[Methods](../README.md#methods)