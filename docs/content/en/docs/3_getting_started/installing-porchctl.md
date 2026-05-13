---
title: "Installing Porchctl CLI"
type: docs
weight: 1
description: Install guide for the Porchctl CLI.
---

## Download the latest porchctl binary

{{< tabpane lang="bash" >}}
{{< tab header="Linux AMD64" >}}
curl -LO "https://github.com/kptdev/porch/releases/download/v{{<params"latestTag">}}/porchctl_{{<params"latestTag">}}_linux_amd64.tar.gz"
{{< /tab >}}
{{< tab header="Linux ARM64" >}}
curl -LO "https://github.com/kptdev/porch/releases/download/v{{<params"latestTag">}}/porchctl_{{<params"latestTag">}}_linux_arm64.tar.gz"
{{< /tab >}}
{{< tab header="macOS AMD64" >}}
curl -LO "https://github.com/kptdev/porch/releases/download/v{{<params"latestTag">}}/porchctl_{{<params"latestTag">}}_darwin_amd64.tar.gz"
{{< /tab >}}
{{< tab header="macOS ARM64" >}}
curl -LO "https://github.com/kptdev/porch/releases/download/v{{<params"latestTag">}}/porchctl_{{<params"latestTag">}}_darwin_arm64.tar.gz"
{{< /tab >}}
{{< /tabpane >}}

{{% alert color="primary" title="Note:" %}}
To download a specific version of porch and its porchctl binary you can do so by replacing the version number and machine type its for in the curl link above.

For example, to download the **[1.5.0](https://github.com/kptdev/porch/releases/tag/v1.5.0)** release version of porch on **macOS AMD64** the URL would be:

```bash
curl -LO "https://github.com/kptdev/porch/releases/download/v1.5.0/porchctl_1.5.0_darwin_amd64.tar.gz"
```

{{% /alert %}}

## Install the porchctl binary

This extracts the tar file containting the binary executable and installs it into the root binary directory of the machine.

{{% alert color="primary" title="Note:" %}}
This requires **root** permissions on the host machine.
{{% /alert %}}

```bash
tar -xzf porchctl_{{% params "latestTag" %}}_linux_amd64.tar.gz | sudo install -o root -g root -m 0755 porchctl /usr/local/bin/
```

{{% alert color="primary" title="Note:" %}}
If you do not have root access on the target system, you can still install porchctl to the `~/.local/bin` directory:
{{% /alert %}}

```bash
tar -xzf porchctl_{{% params "latestTag" %}}_linux_amd64.tar.gz
chmod +x ./porchctl
mkdir -p ~/.local/bin
mv ./porchctl ~/.local/bin/porchctl
# and then append (or prepend) ~/.local/bin to $PATH
```

You can test that the CLI has been installed correctly with the `porchctl version` command. The output should be a printout that looks similar to this.

```bash
Version: {{% params "latestTag" %}}
Git commit: cddc13bdcd569141142e2b632f09eb7a3e4988c9 (dirty)
```

## Enable porchctl autocompletion (optional)

Create the completions directory (if it doesn't already exist):

```bash
mkdir -p ~/.local/share/bash-completion/completions
```

{{% alert color="primary" title="Note:" %}}
This is the auto-completion directory for Ubuntu 24.04 LTS and a few other distributions.
Please do your due diligence and use/create the directory for your appropriate OS/distribution.
{{% /alert %}}

Generate and install the completion script:

```bash
porchctl completion bash > ~/.local/share/bash-completion/completions/porchctl
```

Reload your shell:

```bash
exec bash
```

{{% alert color="primary" title="Note:" %}}
You can reload/refresh your terminal manually without the command by just closing the terminal and starting a new one. Either works as intended.
{{% /alert %}}

Test that the auto-completion works with the following command and pressing the auto-complete key, which is usually `<TAB>`, twice.

```bash
porchctl
```

If auto-completion is working as intended, this should return a similar output to the one below:

```bash
completion  (Generate the autocompletion script for the specified shell)
help        (Help about any command)
repo        (Manage package repositories.)
rpkg        (Manage packages.)
version     (Print the version number of porchctl)
```
