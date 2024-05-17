### `listsc_hardcoded`

> Lists hardcoded SCIDs from the structures package

#### Params

N/A

#### Request

```go
var pingpong structures.WS_ListSCHardcoded_Result

err = Client.RPC.CallResult(context.Background(), method, nil, &pingpong)
if err != nil {
    logger.Errorf("ERR - %v", err)
    Client.Connect("127.0.0.1:9190")
}

for _, v := range pingpong.SCHardcoded {
    logger.Printf("[Return] %v", v)
}
```

#### Response

[Methods](../README.md#methods)