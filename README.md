# OpenAdmin

OpenAdmin is the administrative interface for **OpenPanel**, providing server administrators with a fast, modern, and lightweight dashboard for managing servers, users, services, system settings, and platform configuration.

Written in **Go**, OpenAdmin is designed to be highly efficient with minimal resource consumption while delivering a modern web experience using **Tremor** UI templates. A typical idle installation uses **approximately 15 MB of RAM**, making it suitable for everything from small VPS instances to large production servers.

The user interface is built using [**rremor**](https://tremor.so/) components and templates, providing a clean, responsive, and accessible administration experience.

## Features

* Written in Go for high performance and low resource usage
* Typical idle memory usage of approximately **15 MB RAM**
* Server and system administration
* User and account management
* Service management
* System monitoring
* Security configuration
* Native integration with OpenPanel
* Plugin and extension support
* REST API integration

## Requirements

OpenAdmin has very few requirements:

* Linux operating system
* AMD64 (x86_64) or ARM64 CPU
* OpenPanel configuration from the [`openpanel-configuration` repository](https://github.com/stefanpejcic/openpanel-configuration)

## Installation

OpenAdmin is installed as part of the **OpenPanel** installation process and is **not intended to be installed separately**.

To install OpenPanel (including OpenAdmin), run the official installer: https://openpanel.com/install

The installer automatically installs, configures, and registers OpenAdmin with the required services and configuration.

## Building from Source

Build for AMD64:

```bash
GOOS=linux GOARCH=amd64 go build -buildvcs=false -o openadmin-amd64 ./cmd/openadmin
```

Build for ARM64:

```bash
GOOS=linux GOARCH=arm64 go build -buildvcs=false -o openadmin-arm64 ./cmd/openadmin
```

## Updating

OpenAdmin is updated through the standard OpenPanel update process, but can still be independently updated using `opencli update --admin` command.

For source builds, pull the latest changes, rebuild the binary, and restart the OpenAdmin service: `service admin restart`.

## Contributing

Contributions are welcome.

You can contribute by:

* Reporting bugs
* Suggesting new features
* Improving documentation
* Submitting pull requests
* Developing plugins and extensions

Before submitting significant changes, please [open an issue](https://github.com/stefanpejcic/openadmin/issues/new/choose) to discuss your proposal.

By contributing to OpenAdmin, you agree that your contributions may be distributed under the project's GPLv3 license.

## Security

If you discover a security vulnerability, please **do not** open a public GitHub issue.

Instead, report it responsibly by contacting: **[info@openpanel.com](mailto:info@openpanel.com)**

## Support

Community support is available through GitHub Issues.

Commercial support, consulting, and enterprise services are available from OpenPanel, LLC. on https://my.openpanel.com

## License

OpenAdmin is free and open-source software licensed under the **GNU General Public License v3.0 or later (GPL-3.0-or-later)**.

Copyright (C) 2025 OpenPanel, LLC.
Original author: Stefan Pejcic

### Plugin Exception

Independent plugins, extensions, themes, and integrations that interact with OpenAdmin through its documented APIs and extension mechanisms are **not required to be licensed under GPLv3**.

However, modifications to the OpenAdmin core software must be licensed under GPLv3 when distributed.

### Commercial Licensing

Organizations requiring rights beyond those granted by GPLv3—such as proprietary modifications, OEM redistribution, white-label deployments, or other commercial arrangements may obtain a separate commercial license from OpenPanel, LLC.

See:

* `LICENSE`
* `COPYING`
* `COMMERCIAL-LICENSE.md`
* `TRADEMARKS.md`

For commercial licensing inquiries:

* Website: https://openpanel.com
* Email: [info@openpanel.com](mailto:info@openpanel.com)
