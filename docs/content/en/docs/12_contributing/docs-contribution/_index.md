---
title: "Documentation Contribution"
type: docs
weight: 1
description: How to contribute to Porch documentation
---

This guide explains how to contribute to Porch documentation, including setting up your local environment, previewing changes, and understanding the documentation structure.

## Prerequisites

- Git installed
- Hugo Extended v0.110.0+ installed
- Text editor or IDE
- Basic familiarity with Markdown

## Setup Local Environment

### Clone the Repository

Clone using SSH (required for pushing changes):

```bash
git clone git@github.com:kptdev/porch.git
cd porch/docs
```

SSH authentication links your commits to your GitHub account, which is required for CLA verification. If you haven't set up SSH keys, see [GitHub's SSH documentation](https://docs.github.com/en/authentication/connecting-to-github-with-ssh).

### Install Hugo

Porch documentation uses Hugo Extended edition. Install it for your platform:

**macOS (Homebrew)**
```bash
brew install hugo
```

**Linux (Snap)**
```bash
sudo snap install hugo
```

**Linux (Download)**
```bash
wget https://github.com/gohugoio/hugo/releases/download/v0.139.4/hugo_extended_0.139.4_linux-amd64.tar.gz
tar -xzf hugo_extended_0.139.4_linux-amd64.tar.gz
sudo mv hugo /usr/local/bin/
```

**Windows (Chocolatey)**
```bash
choco install hugo-extended
```

Verify installation:
```bash
hugo version
```

Expected output should include "extended":
```
hugo v0.139.4+extended linux/amd64
```

### Install Dependencies

From the `docs/` directory:

```bash
npm install
```

## Preview Documentation Locally

Start the Hugo development server:

```bash
cd docs
hugo server
```

Open your browser to `http://localhost:1313`. The site auto-reloads when you save changes.

**Useful flags:**
- `hugo server -D` - Include draft pages
- `hugo server --disableFastRender` - Full rebuild on changes
- `hugo server --bind 0.0.0.0` - Allow external access

## Documentation Structure

Porch documentation is organized in numbered sections:

- **Overview** - What Porch is, why it exists, and its role in the ecosystem
- **Concepts** - Core concepts like Package, Repository, and Lifecycle
- **Installation** - Installation, prerequisites, and first steps
- **Guides** - Step-by-step task-oriented guides
- **Architecture** - Internal architecture and component details
- **Configuration** - Configuration options and deployment scenarios
- **CLI & API** - CLI commands and API reference documentation
- **Troubleshooting** - Common issues and solutions
- **Glossaru** - Term definitions organized by topic
- **Contribution** - How to contribute to Porch (this section)

## Writing Guidelines

### File Naming

- Use lowercase with hyphens: `package-lifecycle.md`
- Landing pages: `_index.md`
- Descriptive names: `installing-porch.md` not `install.md`

### Front Matter

Every page needs front matter:

```yaml
---
title: "Page Title"
type: docs
weight: 1
description: Brief description for SEO and navigation
---
```

- `weight`: Controls ordering (lower numbers appear first)
- `description`: Appears in navigation and search results

### Markdown Style

**Headings**: Use sentence case, not title case
```markdown
## Package lifecycle stages
```

**Code blocks**: Always specify language
````markdown
```bash
kubectl get packagerevisions
```
````

**Internal links**: Use [Hugo relref shortcodes](https://gohugo.io/shortcodes/relref/)
```markdown
See [Package Lifecycle]({{%/* relref "package-lifecycle" */%}}).
```

**External links**: Use standard markdown
```markdown
[Kubernetes documentation](https://kubernetes.io/docs/)
```

**Alerts (note/warning/critical boxes)**: There are three alert types available based on the importance of the information:

| Alert type | Code              | Alert color |
|------------|-------------------|-------------|
| Note       | `color="primary"` | Blue        |
| Warning    | `color="warning"` | Yellow      |
| Critical   | `color="danger"`  | Red         |

Make sure not to change the title of the alert type. It should always be either Note, Warning or Critical.

```markdown
{{%/* alert title="Note" color="primary" */%}}
Important information here.
{{%/* /alert */%}}
```

### Content Guidelines

**Be concise**: Get to the point quickly

**Use examples**: Show, don't just tell

**Verify accuracy**: Test commands and examples; include expected command output if reasonably short

**Consider audience**: Assume Kubernetes familiarity; explain Porch-specific concepts

**Avoid jargon**: Define terms/expand acronyms on first use, or link to glossary

## Making Changes

### Create a Branch

```bash
git checkout -b docs-improve-package-lifecycle
```

### Make Your Changes

Edit files in `docs/content/en/docs/`

### Preview Changes

```bash
cd docs
hugo server
```

Verify your changes at `http://localhost:1313`

### Commit Changes

```bash
git add docs/content/en/docs/2_concepts/package-lifecycle.md
git commit -m "docs: clarify package lifecycle transitions"
```

Commit message format:
- Start with `docs:` prefix
- Use imperative mood ("add" not "added")
- Keep first line under 72 characters

### Push and Create PR

```bash
git push origin -u docs-improve-package-lifecycle
```

Open a pull request on GitHub with:
- Clear title describing the change
- Description explaining what and why
- Reference related issues if applicable

The EasyCLA bot will prompt you to sign the CLA if you haven't already (see contributing [Before You Start]({{% relref "../" %}})).

## Special Documentation Tasks

### Generating API Reference

See [API Reference Generation]({{% relref "api-ref-generation" %}}) for instructions on regenerating API documentation from Go source code.

### Adding and editing Diagrams

Diagrams are stored in `docs/static/images/porch/` as `.drawio.svg` files. Diagrams created in this format can be opened in [the draw.io editor](https://github.com/jgraph/drawio-desktop/releases), edited freely, and saved as-is. They can then be referenced in the Markdown files as normal SVG images.

Reference in markdown:
```markdown
![Package Lifecycle](/images/porch/Porch-Architecture.drawio.svg)
```

### Adding New Sections

To add a new top-level section:

1. Create directory: `docs/content/en/docs/13_new_section/`
2. Add landing page: `13_new_section/_index.md`
3. Set appropriate `weight` in front matter
4. Add content files as needed

### Documenting Unreleased Features

When documenting a feature that isn't ready for public release, use Hugo's draft mechanism to keep pages out of the production site while still allowing review in deploy previews.

Add `draft: true` to the page's front matter:

```yaml
---
title: "New Feature"
type: docs
draft: true
---
```

Draft pages are excluded from production builds but included in Netlify deploy previews (pull request previews). This is controlled by environment-specific config files:

- `docs/config/production.toml` — default Hugo behavior (no drafts)
- `docs/config/development.toml` — sets `buildDrafts = true`

Netlify sets `HUGO_ENV=production` for the main deploy and `HUGO_ENV=development` for deploy previews, so draft pages automatically appear in PR previews and stay hidden on the live site.

To preview drafts locally:

```bash
hugo server -D
```

When the feature is ready to publish, remove `draft: true` from the front matter and add any navigation links (e.g. references from parent `_index.md` pages) in the same commit. Avoid adding `relref` links to draft pages from non-draft pages — Hugo will fail the production build with a broken link error.

## Review Process

Documentation PRs are reviewed by maintainers. Expect feedback on:
- Technical accuracy
- Clarity and readability
- Consistency with existing docs
- Proper formatting and links

Address feedback by pushing additional commits to your branch.

## Getting Help

If you have questions:
- Comment on your PR
- Open a [GitHub discussion](https://github.com/kptdev/kpt/discussions?discussions_q=label%3Aarea%2Fporch+)
  - Be sure to add the `area/porch` label when starting your discussion
- Ask in the Kubernetes Slack #kpt channel