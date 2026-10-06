# POC: external Bubblewrap sandbox

## Goal

Learn whether the current agent can run inside an externally selected sandbox,
and what configuration/tool dependencies that entails. Keep production agent
code and human approvals unchanged. Findings stay in the experiments and this
report; defer production design updates until the relevant behavior is understood.

## Runnable experiments

```sh
cd playground
make pg-sandbox-bwrap-filesystem
make pg-sandbox-bwrap-gh
make pg-sandbox-bwrap-sockets
# Or all three:
make pg-sandbox-bwrap
```

Requires Linux, Python 3.9+, Bubblewrap, and (for the gh case) GitHub CLI at
`/usr/bin/gh`. The default cases involve no package installation, remote API
calls, model requests, or real credentials. The optional authentication case
below uses existing credentials for read-only GitHub API requests. Every case
creates a temporary disposable project,
control directory, configuration directory, and synthetic host-only sentinel.
Socket probes create only their own services and exchange only the sentinel.
A restricted execution environment may block creating these synthetic sockets;
that is a harness failure, not a negative sandbox observation.

The fixture exposes system runtime trees read-only, the disposable project and
configuration read/write, and `.coderoom` read-only as a nested mount. It creates
private home/tmp, clears environment, retains host networking by default, and
uses a separate PID namespace. It does not inherit unintended file descriptors.
Its flags are deliberately small and exploratory, not a production security
profile. It has no escape fallback when Bubblewrap fails.

### Separation of responsibilities

- `needs.py`: technology-neutral mount/environment fixture values.
- `tool_needs.py`: `gh`'s configuration requirement, bundled with its directory
  selector. This code knows the tool, not Bubblewrap.
- `bubblewrap.py`: provider-specific filesystem construction and process launch.
- `experiment.py`: disposable controller fixtures, negative controls, and checks.
- `session_auth.py`: opt-in read-only-home and filtered platform-service probes.

The fixtures do not prescribe the production API. They test that a caller can
supply tool needs and execute a command through an external provider without
adding sandbox technology to the agent adapter. There is only one provider so
far; cross-provider portability has not been demonstrated.

## Experiment 1: filesystem access

Write/read a file in the project, shared synthetic configuration, private home,
and private `/tmp`. Attempt to read a sentinel outside every grant, overwrite
`.coderoom/policy`, rename `.coderoom`, and overwrite a file in a disposable
host-writable runtime directory mounted read-only. Confirm the host writes the
file before and after the sandbox run, the sandbox can read it, the attempted
overwrite fails with `EROFS`, and the host contents remain unchanged.
Each expected result is checked; a mismatch exits with failure.

Observed on 2026-10-04 with Bubblewrap 0.9.0:

| Check | Observation |
|---|---|
| Project/configuration writes | Succeeded |
| Private home and temporary writes | Succeeded |
| Host-only sentinel lookup | Hidden |
| Control-file overwrite | Blocked |
| Control-directory rename | Blocked |
| Disposable runtime-file read | Succeeded |
| Disposable runtime-file overwrite | Blocked with read-only-filesystem error |
| Host runtime-file writes before and after | Succeeded |
| Runtime contents after sandbox attempt | Unchanged |

The original `/usr` write check was not evidence of sandbox enforcement: an
ordinary host user cannot normally write there either. The disposable writable
fixture replaces that check with positive host controls.

This checks a simple disposable layout. It does not test hard links, alternate
mount aliases, linked git worktrees, mount-source races, all supported toolchains,
or resource exhaustion. Passing does not establish production confinement.

## Experiment 2: gh configuration discovery

Use `gh config get/set editor` against a synthetic directory. Compare:

1. Mount the directory, but do not select it through environment.
2. Set `GH_CONFIG_DIR`, but do not mount that directory.
3. Mount it and set `GH_CONFIG_DIR` together.

Only the combined case read the expected synthetic editor value. After setting
an updated value in the sandbox, a normal host-side `gh` invocation using the
same synthetic directory saw it. After replacing `config.yml` atomically outside
the sandbox, a new sandbox invocation saw the replacement. GitHub CLI version:
2.45.0. No account data was read or changed.

A directory mount and its selector must be supplied together. GitHub CLI resolves
configuration from `GH_CONFIG_DIR`, then the documented platform defaults such as
`XDG_CONFIG_HOME/gh` and `HOME/.config/gh`.
[GitHub CLI environment documentation](https://cli.github.com/manual/gh_help_environment).

For real usage, a tool-specific resolver would identify the effective host
configuration location and request the directory plus the corresponding sandbox
selector. That declaration is separate from the backend's own state directory:
mounting Codex state does not automatically provide GitHub CLI state.

**What this does not prove:** authentication can use environment tokens or a
system credential store. Mounting gh configuration alone does not guarantee
successful authentication. The separate authentication experiment below checks
that distinction. Git credential helpers, extensions, and external editors remain
untested. The replacement check uses subsequent invocations, not a long-lived
CLI's in-memory token cache.

### Optional experiment: authenticated GET /user

```sh
cd playground
make pg-sandbox-bwrap-gh-auth
# Explicitly retrieve the host credential and export it to the child environment:
make pg-sandbox-bwrap-gh-auth-export-token
```

Unlike the default fixtures, this reads existing real gh credentials and contacts
GitHub. Both modes call `gh api --hostname github.com --method GET user --silent`
on the host and inside Bubblewrap. GitHub's `/user` endpoint requires authentication;
response bodies, tokens, account names, and raw error output are suppressed.
The harness reports classified outcomes only. A failed host baseline is
inconclusive and stops the comparison; a failed sandbox request exits nonzero.
[GitHub authenticated-user API](https://docs.github.com/en/rest/users/users#get-the-authenticated-user).

The default authentication mode selects the effective host gh configuration and
passes existing `GH_TOKEN`/`GITHUB_TOKEN` variables if present. It also mounts
resolver and CA files required for HTTPS. Real configuration is read-only for
this diagnostic: no login, logout, refresh, or configuration mutation is performed.
The synthetic configuration experiment still tests the intended read/write grant.

The explicit export mode obtains a token through host `gh auth token` and supplies
it as `GH_TOKEN`. It does not print it, write it to disk, or put it in launch argv.
The provider constructs a clean child environment at the subprocess boundary,
instead of placing secret values in Bubblewrap `--setenv` arguments. The token is
nevertheless available to every command in that process tree; this is a separate
credential grant, not an authentication mechanism chosen for production.

Observed on 2026-10-04: host `gh auth status` reported keyring storage, and no gh
authentication environment token was initially present.

| Request context | Observation |
|---|---|
| Host GET /user with existing authentication | Succeeded |
| Sandbox with mounted configuration, resolver, and CA files | Authentication failed |
| Sandbox with those mounts plus explicit host-token export | Succeeded |

This confirms that the directory mount alone is insufficient for this machine's
keyring-backed setup. Passing the credential explicitly enables the authenticated
operation without exposing a host keyring socket. It does not establish that
passing tokens is the right production choice, or prove refresh behavior,
keyring isolation, token scope, or authentication on other installations.
The unprivileged harness run could not establish a host network baseline; these
observations were collected with the outer execution restrictions lifted while
the tested command still ran inside Bubblewrap.


## Experiment 3: read-only home and filtered keyring access

These are opt-in real-authentication probes, separate from the synthetic default
suite. Both use stored host authentication with `GH_TOKEN`/`GITHUB_TOKEN` removed,
and suppress API bodies, credentials, account names, and raw authentication errors.
No login/logout, credential refresh, or secret update is requested.

```sh
cd playground
make pg-sandbox-bwrap-home-read-only
make pg-sandbox-bwrap-keyring-proxy
```

`session_auth.py` supplies these cases without modifying the production agent API.
The proxy case additionally requires `xdg-dbus-proxy`, `dbus-send`, a working host
session bus, and a Secret Service already usable by host gh. The unrelated-service
control currently requires a running user systemd service; if absent, the probe
reports an inconclusive control rather than treating failure as a successful block.
Proxy processes are bounded, stopped, and reaped, and disposable sockets are
removed with the fixture. Reported results below used xdg-dbus-proxy 0.1.5.

### Read-only home

Mount the actual home at its original path read-only and set HOME accordingly.
Also expose the effective gh configuration and resolver/CA files read-only. Do
not forward a token or expose the host session bus. Compare authenticated GET
/user with the host baseline.

Observed on 2026-10-04: host request succeeded; sandbox authentication failed.
Reading home files does not provide the connection to the running keyring service.
The Secret Service is a login-session service accessed through D-Bus, rather than
just a directory of readable credentials.
[Secret Service introduction](https://specifications.freedesktop.org/secret-service/latest/ch01.html).

A read-only home may improve compatibility for file-backed configuration and
credentials. It exposes all readable home data, blocks persistent state writes,
and does not make connections to sockets inside that home read-only. This result
is specific to the tested keyring-backed installation; file-backed auth can differ.

### Filtered platform service, instead of per-tool token export

Start xdg-dbus-proxy outside Bubblewrap against the host session bus. Expose only
its disposable socket to the sandbox and set DBUS_SESSION_BUS_ADDRESS to that
proxy. The sandbox still has a private home and only the gh configuration mount;
the real host bus socket and general runtime directory are not mounted. No token
is fetched or copied into the environment. The tool asks the keyring for its
credential using its normal protocol.

Compare two filters:

- **Service filter:** allow communication with `org.freedesktop.secrets` only.
- **Retrieval filter:** allow explicit Secret Service methods for session setup,
  collection/item search, credential retrieval, property reads, and unlock.
  Omit secret creation, update, deletion, alias-setting, and property-setting.

The method list lives in `retrieval_rules()`. It includes collection SearchItems,
which was missing from the first attempted allowlist; gh authentication failed
until that was added. Unlock is a permitted operation, so this is not a purely
read-only service connection. Interactive prompt handling is not provided by the
current list; locked-keyring behavior remains untested.

xdg-dbus-proxy supports destination/interface/method/path filters. Those filters
are platform-service permissions, not tool-specific credentials or a mechanism
for filtering search attributes and secret contents.
[xdg-dbus-proxy documentation](https://github.com/flatpak/xdg-dbus-proxy/blob/main/xdg-dbus-proxy.xml).

| Check | Service filter | Retrieval filter |
|---|---|---|
| Sandbox connection to proxy bus (ListNames) | Succeeded | Succeeded |
| Authenticated gh GET /user, no forwarded token | Succeeded | Succeeded |
| User systemd Peer.Ping through proxy | Blocked | Blocked |
| Malformed Secret Service SetAlias call | Reached service; arguments rejected | Rejected by proxy |

Controls make these observations meaningful: systemd Peer.Ping succeeds on the
host; ListNames proves the sandbox client works through the proxy; the malformed
SetAlias call reaches the host service and fails type validation. It deliberately
omits mandatory arguments, so neither the host nor the broad-filter control can
change an alias. The narrow filter returns AccessDenied instead.

Initial busctl controls could not connect through this proxy even for allowed
calls and were inconclusive. The repeatable fixture uses dbus-send and verifies a
positive proxy connection before interpreting denied calls as filter enforcement.

**What improves:** native keyring-backed gh authentication works without a
coderoom-specific token-export adapter, a whole-home mount, or unrestricted session
bus access. Other tools using this platform service may benefit, but only gh has
been tested; other keyrings and authentication protocols may need different access.

**What remains:** allowed retrieval calls can access secrets beyond GitHub's, and
the proxy does not scope lookup arguments to a credential or tool. The sandbox
receives the credential through the normal protocol and can still disclose or
use it. Service filtering narrows access to host services; it is not credential
isolation. Method/path filters have not received a complete security audit.
Direct sockets exposed through project/configuration mounts, abstract sockets
with host networking, and host TCP services are separate unresolved routes; the
proxy cannot prevent bypass through those channels.

The next compatibility checks are another Secret Service client, a locked
keyring, and authentication refresh. The next boundary checks must address direct
service/socket access and credential scope. Do not turn this successful gh probe
into approval automation yet.

## Experiment 4: Unix sockets and descriptor passing

A synthetic host server sends a read-only descriptor for the otherwise hidden
sentinel using `SCM_RIGHTS`. The client attempts to connect from the sandbox and
read that descriptor. For the late-created case, wait until a sandbox client
signals readiness before creating the socket in its mounted project.

| Host channel | Network namespace | Connected | Hidden sentinel read through descriptor |
|---|---|---|---|
| Pathname socket in project | Host | Yes | Yes |
| Pathname socket in configuration | Host | Yes | Yes |
| Project socket created after sandbox startup | Host | Yes | Yes |
| Abstract Unix socket | Host | Yes | Yes |
| Abstract Unix socket | Private | No | No |
| Pathname socket in project | Private | Yes | Yes |

These are observations, not expected safety assertions: the fixture reports
access even when that demonstrates a bypass. The synthetic server never executes
commands and never opens actual secrets. Linux supports both pathname and abstract
Unix sockets and descriptor passing through their ancillary data.
[Linux Unix socket documentation](https://man7.org/linux/man-pages/man7/unix.7.html).

A directory mount can expose host service sockets, including ones created later.
Private networking removed abstract socket access in this fixture but did not
remove pathname socket access. Clearing inherited descriptors is insufficient:
a host service can hand the process new ones after launch. A startup socket scan
is insufficient too. Do not call this setup a verified boundary for automatic
approvals until these channels are blocked or deliberately accepted in its contract.

The next security experiment should compare candidate Unix-socket restrictions
with the real CLI's behavior. A broad AF_UNIX restriction may break the CLI or
keyring authentication; do not choose it without checking compatibility. Normal
TCP access to host services is an additional limitation with networking retained.

## Next: real Codex, still outside the adapter

Not run yet. Use an opt-in command wrapper or small driver that launches the
installed app-server through the same external provider. Keep the backend's
JSON-RPC handling unchanged. Avoid adding a production `Agent` method during
this experiment.

1. Record the actual Codex executable and runtime dependencies. On this machine,
   `node`/`npx` resolve through `/snap/bin`, while `codex` resolves into an npm
   package cache. A PATH entry alone will not expose the target of a symlink or
   make a Snap launcher function inside this filesystem view. Prefer a directly
   installed executable for the first check, and report whether npx/Snap needs
   additional runtime grants. Do not expose the entire home as a shortcut.
2. Request the effective Codex state directory read/write at its original absolute
   location. Keep project cwd unchanged to test native outside-session continuity.
   This deliberately accesses real persistent state; label the mode clearly and
   keep it separate from the disposable default fixtures.
3. Start app-server with the provider-selected wrapper, complete initialization,
   create a thread, and run one harmless turn. Reuse the existing integration's
   protocol behavior instead of copying an assumed schema. Record the thread ID
   without logging credentials or raw authentication configuration.
4. Make `gh` available to the same process tree: expose its runtime and effective
   configuration directory, and its selector. First run a non-secret config check,
   then an opt-in authentication check that does not print a token. Test environment
   token and keyring-backed modes separately, recording unsupported cases.
5. Perform external authentication refresh manually and observe whether a running
   backend sees it or needs restart. Continue the created session through the
   native CLI; storage visibility alone is not proof of resume compatibility.
6. Start a disposable detached child, stop the wrapper, and confirm it exits.
   Also test failed startup and parent death. The current simple fixtures use
   timeouts but do not establish whole-tree cleanup for a real persistent backend.
7. Compare the backend's default inner sandbox with the outer provider. Do not
   disable inner protection or relax outer grants just to obtain a green run
   without documenting why and the effect on the boundary.

Exit criteria: a repeatable app-server handshake/turn, explicit tool/configuration
requirements, reported socket/authentication limitations, and proven cleanup.
Only then propose a small opt-in adapter integration. Approval automation remains
out of scope for that integration.

## Resulting questions, not an upfront framework

- Which needs are intrinsic to the backend, and which belong to selected tools
  such as gh, Git, npm, and compilers?
- Can a controller combine small tool declarations without mounting broad parents?
- Which authentication setups require services that weaken isolation?
- Which socket restriction is compatible with the actual backend/toolchain?
- What is the smallest reusable external launch hook after the experiments?

Later Rego work must expose effective grants (including configuration and runtime
exceptions) in controller-generated inputs. A profile name and plan hash alone
cannot explain permissions. Exact schemas, modes, and reload/audit interfaces are
intentionally deferred until the opt-in integration is understood.
