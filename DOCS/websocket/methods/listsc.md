### `listsc`

> Lists indexed SCs (scid, owner/sender, deployheight if possible) with optional param filters by address (sender/installer) and/or scid. Deployheight is returned where possible, an invoke of the installation must have been indexed to validate this data and provide it

#### Params

|Name|Type|Required|Description|
|:--:|:--:|:------:|:---------:|
|address|String|Optional|Supply an address for filtering on owner/sc deployer|
|scid|String|Optional|Supply a scid for filtering to a specific scid|

#### Request

#### Response

[Return](../README.md)