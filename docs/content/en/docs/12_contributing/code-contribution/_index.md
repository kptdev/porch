---
title: "Code Contribution"
type: docs
weight: 1
description: How to contribute to the Porch codebase
---

This guide explains how to set up your Git environment and contribute code to Porch.

## Fork the Repository

Porch uses a fork-based workflow. This protects the upstream repository from accidental changes and ensures all modifications go through the pull request review process.

1. Navigate to [github.com/kptdev/porch](https://github.com/kptdev/porch)
2. Click the **Fork** button in the top-right corner
3. Select your GitHub account as the destination

This creates a copy of the Porch repository under your GitHub account (e.g., `github.com/YOUR_USERNAME/porch`).

## Clone Your Fork

Clone your fork using SSH (required for pushing changes):

```bash
git clone git@github.com:YOUR_USERNAME/porch.git
cd porch
```

SSH authentication links your commits to your GitHub account, which is required for CLA verification. If you haven't set up SSH keys, see [GitHub's SSH documentation](https://docs.github.com/en/authentication/connecting-to-github-with-ssh).

## Configure Remotes

Add the upstream repository and prevent accidental pushes to it:

```bash
git remote add upstream https://github.com/kptdev/porch.git
git remote set-url --push upstream no_push
```

Verify your remotes:

```bash
git remote -v
```

Expected output:
```
origin    git@github.com:YOUR_USERNAME/porch.git (fetch)
origin    git@github.com:YOUR_USERNAME/porch.git (push)
upstream  https://github.com/kptdev/porch.git (fetch)
upstream  no_push (push)
```

## The Golden Rules

1. **Always update from upstream**, never from origin
2. **Always push to origin** (your fork), never to upstream
3. **Never commit directly to main branch** - always work on feature branches

## Keep Your Fork Updated

Before creating a new branch, sync your fork's main branch with upstream:

```bash
git checkout main
git fetch upstream
git rebase upstream/main
git push origin main
```

Do this regularly to keep your fork up to date.

## Create a Feature Branch

Create a branch for your changes:

```bash
git checkout -b feature-add-package-validation
```

Branch naming conventions:
- `feature-` - New features
- `fix-` - Bug fixes
- `refactor-` - Code refactoring
- `docs-` - Documentation changes

## Make Your Changes

1. Edit code, add tests, update documentation
2. Follow the [Development Environment]({{% relref "development-environment" %}}) guide to build and test locally
3. Ensure all checks and tests pass: `make check`, `make test` and `make test-e2e`
## Update Copyright on files

If you have added any new golang files, add [this golang copyright header](https://github.com/kptdev/porch/blob/main/scripts/boilerplate.go.txt) to each new golang file you have added. If you have added any other files (Yaml, scripts, test data), add [this text copyright header](https://github.com/kptdev/porch/blob/main/scripts/boilerplate.yaml.txt) file to each new file.

If you have updated existing files, **amend the dates on the copyright notice**. Assuming you are updating the code in 2026, use the following guide.

| Existing dates      | New Dates                |
| ------------------- | ------------------------ |
| 2026                | 2026                     |
| 2025                | 2025-2026                |
| 2024                | 2024, 2026               |
| 2023                | 2023, 2026               |
| 2025-2026           | 2025-2026                |
| 2024-2026           | 2024-2026                |
| 2022-2026           | 2022-2026                |
| 2022-2025           | 2022-2026                |
| 2022-2024           | 2022-2024, 2026          |
| 2018-2020,2022-2025 | 2018-2020,2022-2026      |
| 2018-2020,2022-2024 | 2018-2020,2022-2024,2026 |
|         ...         |           ...            |


## Commit Your Changes

```bash
git add .
git commit -sm "feat: add package validation for lifecycle transitions"
```

Commit message format:
- `feat:` - New feature
- `fix:` - Bug fix
- `refactor:` - Code refactoring
- `test:` - Test changes
- `docs:` - Documentation
- `chore:` - Maintenance tasks

Use imperative mood ("add" not "added").

## Push to Your Fork

Push your branch to your fork (origin):

```bash
git push origin feature-add-package-validation
```

## Create a Pull Request

1. Navigate to [github.com/kptdev/porch](https://github.com/kptdev/porch)
2. GitHub will show a prompt to create a PR from your recently pushed branch
3. Click **Compare & pull request**
4. Fill in the PR template:
   - Clear title describing the change
   - Description explaining what, why, and how
   - Reference related issues: "Fixes #123"
   - Include test results if applicable

The EasyCLA bot will prompt you to sign the CLA if you haven't already (see [Before You Start]({{% relref "../" %}})).

## Declare any use of AI

{{% alert title="Warning" color="warning" %}}
The use of AI in the creation of PRs is allowed but you must declare any use of AI and you
must be able to explain the PR code independently of any AI tools.
{{% /alert %}}


Update the PR description to state whether you used AI to help you create this PR; if so, list the AI tools you have used and in what areas.

For example:

```
I have used AI in the creation of this PR.

I have used the following AI tools:
- Microsoft Copilot to analyse the code
- Claude code to generate the function someNewFunctionIAdded()
- Amazon Q to generate unit tests
```

## CI Checks on Your PR

When you create a Pull Request, the Continuous Integration (CI) framework will run some checks on your PR. It will:

1. Compile, build and test the code including the code changes in your PR
2. Build and verify the documentation including the documentation changes in your PR, carrying out checks such as making sure there are no dead internal links in the documentation
3. Run linters and other checks on your code
4. Run SonarCloud to check that the code in your PR meets quality criteria such as required test coverage percentage
5. Run some AI tools (such as Copilot or [Dosu](https://dosu.dev/)) to review the code

## Update Your PR

If the CI checks fail or raise comments, or if the maintainers request changes:

1. Make the requested changes on your local branch
2. Commit the changes: `git commit -sm "fix: address review feedback"`
3. Push to the upstream branch on your fork: `git push origin feature-add-package-validation`

The PR will automatically update with your new commits.

## Reviews of Your PR

Before your PR is merged, it must be reviewed by community members and maintainers. Generally, in order to make the best
use of their time, they will review your PR when:

1. The code including the code changes in your PR is compiling and building
2. The documentation is building and verified
3. All lint checks are passing
4. SonarCloud quality checks such as code coverage levels are passing
5. The comments from the first run of AI on the commit of the PR are addressed (Further re-runs of AI are optional)

{{% alert title="Note" color="primary" %}}
If you are having difficulty in getting tests to pass, need guidance in how to address an AI-generated comment, or
want to request an exception on a quality metric such as code coverage, please add a comment on your PR and the
community members and maintainers will consider your request.
{{% /alert %}}

## Rebase on Latest Main

If your PR becomes outdated with the main branch:

```bash
git checkout feature-add-package-validation
git fetch upstream
git rebase upstream/main
git push origin feature-add-package-validation --force-with-lease
```

Use `--force-with-lease` instead of `--force` for safer force-pushing.

## After Your PR is Merged

Clean up your local and remote branches:

```bash
git checkout main
git fetch upstream
git rebase upstream/main
git push origin main
git branch -d feature-add-package-validation
git push origin --delete feature-add-package-validation
```

## Common Git Operations

### Switch Between Branches

```bash
git checkout main
git checkout feature/my-feature
```

### View Branch Status

```bash
git status
git branch -a  # List all branches
```

### Discard Local Changes

```bash
git checkout -- <file>  # Discard changes to specific file
git reset --hard        # Discard all local changes
```

### View Commit History

```bash
git log --oneline
git log --graph --oneline --all
```

## Getting Help

If you encounter Git issues:
- Check existing GitHub issues
- Ask in the Kubernetes Slack #kpt channel
- Consult [GitHub documentation](https://docs.github.com)
- For issues with Git itself, consult [its documentation](https://git-scm.com/docs)
