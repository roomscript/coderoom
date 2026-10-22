# Install coderoom

coderoom release archives are available for macOS and Linux, on AMD64 and Arm64.
Use a terminal and a working Codex CLI or Claude Code setup. Install the
dependencies for the backend you plan to use.

## Download a release

Download the archive for your platform from
[GitHub Releases](https://github.com/roomscript/coderoom/releases/latest).
Each release includes `checksums.txt` for checking the download.

Extract the archive. Replace the placeholders with the downloaded filename:

```bash
tar -xzf coderoom_<version>_<os>_<arch>.tar.gz
```

Move the `coderoom` executable into a directory on your `PATH`, such as
`~/.local/bin` if your shell includes it. Check that the shell can find it:

```bash
coderoom -h
```

You can also keep the executable where you extracted it and launch it by its
absolute path from your repository.

## Codex dependencies

coderoom launches Codex with `npx @openai/codex app-server`. It needs Node.js and
`npx` on `PATH`, even if you installed Codex through another method. The first
launch may download the npm package.

Check these from the terminal where you will launch coderoom:

```bash
node --version
npx --version
npx @openai/codex --version
```

Complete Codex setup and sign-in before inviting an agent. Follow the
[official Codex CLI instructions](https://developers.openai.com/codex/cli/).
coderoom uses Codex's authentication and configured defaults.

## Claude Code dependencies

coderoom launches the `claude` executable on `PATH`. Check it from the terminal
where you will launch coderoom:

```bash
claude --version
```

Complete installation and login using the
[official Claude Code setup instructions](https://code.claude.com/docs/en/setup).
coderoom uses Claude Code's authentication and configured defaults. A room using
only Claude requires the `claude` executable; Node.js and `npx` are Codex
dependencies.

## Start in your repository

For Codex with the built-in default:

```bash
cd /path/to/your/repository
coderoom
```

For Claude Code:

```bash
cd /path/to/your/repository
CODEROOM_BACKEND=claude coderoom
```

Participant files can select a backend for each agent. See
[backend selection](participants.md#backend-selection) for the full rules.
Then follow [your first room](first-room.md).

## Build from source

Install Go at the version required by [`go.mod`](../../go.mod), or newer, and
`make`. From a checkout of coderoom:

```bash
make build
```

The executable is `bin/coderoom`. Add it to `PATH` or launch it by its absolute
path from the repository you want to work on. Backend dependencies still apply.

## Setup problems

| Symptom | Check |
| --- | --- |
| `coderoom` is not found | Add its directory to `PATH`, or use its absolute path. |
| `npx` or `claude` is not found | Run the version checks above in the same shell. |
| Codex tries to start when you expected Claude | Check [backend selection](participants.md#backend-selection). |
| The agent reports a login failure | Complete login in that backend's CLI, then remove and reinvite the agent. |
| coderoom reports configuration errors before opening | Fix the files listed in the error. See [participant validation](participants.md#validation). |

The room checks backend dependencies in the background. These checks report
missing tools. Successful version checks do not establish that login or model
access works.

[Manual](index.md)
