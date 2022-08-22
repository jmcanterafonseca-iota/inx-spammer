---
description: This section describes the configuration parameters and their types for INX-Spammer.
keywords:
- IOTA Node
- Hornet Node
- Spammer
- Configuration
- JSON
- Customize
- Config
- reference
---


# Core Configuration

INX-Spammer uses a JSON standard format as a config file. If you are unsure about JSON syntax, you can find more information in the [official JSON specs](https://www.json.org).

You can change the path of the config file by using the `-c` or `--config` argument while executing `inx-spammer` executable.

For example:
```bash
inx-spammer -c config_defaults.json
```

You can always get the most up-to-date description of the config parameters by running:

```bash
inx-spammer -h --full
```
## <a id="app"></a> 1. Application

| Name            | Description                                                                                            | Type    | Default value |
| --------------- | ------------------------------------------------------------------------------------------------------ | ------- | ------------- |
| checkForUpdates | Whether to check for updates of the application or not                                                 | boolean | true          |
| stopGracePeriod | The maximum time to wait for background processes to finish during shutdown before terminating the app | string  | "5m"          |

Example:

```json
  {
    "app": {
      "checkForUpdates": true,
      "stopGracePeriod": "5m"
    }
  }
```

## <a id="inx"></a> 2. INX

| Name    | Description                            | Type   | Default value    |
| ------- | -------------------------------------- | ------ | ---------------- |
| address | The INX address to which to connect to | string | "localhost:9029" |

Example:

```json
  {
    "inx": {
      "address": "localhost:9029"
    }
  }
```

## <a id="spammer"></a> 3. Spammer

| Name                                  | Description                                                                        | Type    | Default value                  |
| ------------------------------------- | ---------------------------------------------------------------------------------- | ------- | ------------------------------ |
| autostart                             | Automatically start the spammer on startup                                         | boolean | false                          |
| bpsRateLimit                          | The blocks per second rate limit for the spammer (0 = no limit)                    | float   | 0.0                            |
| cpuMaxUsage                           | Workers remains idle for a while when cpu usage gets over this limit (0 = disable) | float   | 0.8                            |
| workers                               | The amount of parallel running spammers                                            | int     | 0                              |
| message                               | The message to embed within the spam blocks                                        | string  | "We are all made of stardust." |
| tag                                   | The tag of the block                                                               | string  | "HORNET Spammer"               |
| tagSemiLazy                           | The tag of the block if the semi-lazy pool is used (uses "tag" if empty)           | string  | "HORNET Spammer Semi-Lazy"     |
| [valueSpam](#spammer_valuespam)       | Configuration for Value Spam                                                       | object  |                                |
| [tipselection](#spammer_tipselection) | Configuration for tipselection                                                     | object  |                                |

### <a id="spammer_valuespam"></a> Value Spam

| Name               | Description                                                        | Type    | Default value |
| ------------------ | ------------------------------------------------------------------ | ------- | ------------- |
| enabled            | Whether to spam with transaction payloads instead of data payloads | boolean | false         |
| sendBasicOutput    | Whether to send basic outputs                                      | boolean | true          |
| collectBasicOutput | Whether to collect basic outputs                                   | boolean | true          |
| createAlias        | Whether to create aliases                                          | boolean | true          |
| destroyAlias       | Whether to destroy aliases                                         | boolean | true          |
| createFoundry      | Whether to create foundries                                        | boolean | true          |
| destroyFoundry     | Whether to destroy foundries                                       | boolean | true          |
| mintNativeToken    | Whether to mint native tokens                                      | boolean | true          |
| meltNativeToken    | Whether to melt native tokens                                      | boolean | true          |
| createNFT          | Whether to create NFTs                                             | boolean | true          |
| destroyNFT         | Whether to destroy NFTs                                            | boolean | true          |

### <a id="spammer_tipselection"></a> Tipselection

| Name                  | Description                                                                                                 | Type | Default value |
| --------------------- | ----------------------------------------------------------------------------------------------------------- | ---- | ------------- |
| nonLazyTipsThreshold  | The maximum amount of tips in the non-lazy tip-pool before the spammer tries to reduce these (0 = always)   | uint | 0             |
| semiLazyTipsThreshold | The maximum amount of tips in the semi-lazy tip-pool before the spammer tries to reduce these (0 = disable) | uint | 30            |

Example:

```json
  {
    "spammer": {
      "autostart": false,
      "bpsRateLimit": 0,
      "cpuMaxUsage": 0.8,
      "workers": 0,
      "message": "We are all made of stardust.",
      "tag": "HORNET Spammer",
      "tagSemiLazy": "HORNET Spammer Semi-Lazy",
      "valueSpam": {
        "enabled": false,
        "sendBasicOutput": true,
        "collectBasicOutput": true,
        "createAlias": true,
        "destroyAlias": true,
        "createFoundry": true,
        "destroyFoundry": true,
        "mintNativeToken": true,
        "meltNativeToken": true,
        "createNFT": true,
        "destroyNFT": true
      },
      "tipselection": {
        "nonLazyTipsThreshold": 0,
        "semiLazyTipsThreshold": 30
      }
    }
  }
```

## <a id="restapi"></a> 4. RestAPI

| Name                      | Description                                               | Type    | Default value    |
| ------------------------- | --------------------------------------------------------- | ------- | ---------------- |
| bindAddress               | The bind address on which the Spammer HTTP server listens | string  | "localhost:9092" |
| debugRequestLoggerEnabled | Whether the debug logging for requests should be enabled  | boolean | false            |

Example:

```json
  {
    "restAPI": {
      "bindAddress": "localhost:9092",
      "debugRequestLoggerEnabled": false
    }
  }
```

## <a id="pow"></a> 5. Pow

| Name                | Description                             | Type   | Default value |
| ------------------- | --------------------------------------- | ------ | ------------- |
| refreshTipsInterval | Interval for refreshing tips during PoW | string | "5s"          |

Example:

```json
  {
    "pow": {
      "refreshTipsInterval": "5s"
    }
  }
```

## <a id="profiling"></a> 6. Profiling

| Name        | Description                                       | Type    | Default value    |
| ----------- | ------------------------------------------------- | ------- | ---------------- |
| enabled     | Whether the profiling plugin is enabled           | boolean | false            |
| bindAddress | The bind address on which the profiler listens on | string  | "localhost:6060" |

Example:

```json
  {
    "profiling": {
      "enabled": false,
      "bindAddress": "localhost:6060"
    }
  }
```

## <a id="prometheus"></a> 7. Prometheus

| Name            | Description                                                     | Type    | Default value    |
| --------------- | --------------------------------------------------------------- | ------- | ---------------- |
| enabled         | Whether the prometheus plugin is enabled                        | boolean | false            |
| bindAddress     | The bind address on which the Prometheus HTTP server listens on | string  | "localhost:9312" |
| spammerMetrics  | Whether to include the spammer metrics                          | boolean | true             |
| goMetrics       | Whether to include go metrics                                   | boolean | false            |
| processMetrics  | Whether to include process metrics                              | boolean | false            |
| promhttpMetrics | Whether to include promhttp metrics                             | boolean | false            |

Example:

```json
  {
    "prometheus": {
      "enabled": false,
      "bindAddress": "localhost:9312",
      "spammerMetrics": true,
      "goMetrics": false,
      "processMetrics": false,
      "promhttpMetrics": false
    }
  }
```

