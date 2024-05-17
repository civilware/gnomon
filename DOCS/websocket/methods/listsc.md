### `listsc`

> Lists indexed SCs (scid, owner/sender, deployheight if possible) with optional param filters by address (sender/installer) and/or scid. Deployheight is returned where possible, an invoke of the installation must have been indexed to validate this data and provide it

#### Params

|Name|Type|Required|Description|
|:--:|:--:|:------:|:---------:|
|address|String|Optional|Supply an address for filtering on owner/sc deployer|
|scid|String|Optional|Supply a scid for filtering to a specific scid|

#### Request

```go
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
```

#### Response

[Methods](../README.md#methods)