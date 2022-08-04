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
