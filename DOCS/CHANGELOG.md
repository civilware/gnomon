## CHANGELOG

###2.0.3-alpha.8

* Fastsync force height diff knobs
* SC stored Values handling of various in-built functions (when alone) from #2
* Handling connections for http, https, ws, wss etc. for daemon connectivity
* Multiple CLI filters usable in succession and added the "| last" filter for showing recent results
* Interaction address tracking added
* Custom data directory for gnomondb and derodb (if used) - #26
* Bug fixes

###2.0.3-alpha.1

* Fastsync configuration moved to a structure to better support future changes and customizations.
* GnomonIndexer CLI Updates
    * listsc_codematch, diffscid_code, countinvoke_burnvalue added
    * Added pipe filtering in new filter.go current and future support to be used within cli commands e.g. '| grep dReams' or '| exclude dReams'

###2.0.2-alpha.1

* Indexer 'Status' property added that can be referenced for an easy state check for connected packages, dApps etc.

###2.0.1-alpha.1

* fastsync struct for future state custom configs w/ fastsync
* skipfsrecheck option added for options to skip re-validation of scids ingested from gnomon index sc
* AddSCIDToIndex() is usable outside of just fastsync options and is capable of on-the-fly utilization

###2.0.0-alpha.1

Gnomon version 2, alpha release 1.

* bbolt db support added
* gnomonindexer and gnomonsc separated into individual [Applications](https://github.com/civilware/Gnomon/tree/main/cmd)
* parallel block indexing
* multi-string support for search filter
* cleaner global var definitions and references through structures package
* logrus logging added
* utilized [Diff()](https://github.com/deroproject/graviton/blob/master/diff_tree.go#L26) in support of #9 to optimize diffing the scid variables at each index. This significantly reduced local storage bloat
* pull request template, bug and feature request templates
* optimizations throughout

###1.0.0.

* Gnomon Implemented