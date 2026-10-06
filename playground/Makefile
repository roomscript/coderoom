.PHONY: pg-claude-pty
pg-claude-pty:
	cd claude-pty && go run .

.PHONY: pg-claude-msg-continuation
pg-claude-msg-continuation:
	cd claude-msg-continuation && go run .

.PHONY: pg-claude-simple-env
pg-claude-simple-env:
	cd claude-simple-env && go run .

.PHONY: pg-codex-pty
pg-codex-pty:
	cd codex-pty && go run .

.PHONY: pg-codex-msg-continuation
pg-codex-msg-continuation:
	cd codex-msg-continuation && go run .

.PHONY: pg-codex-simple-env
pg-codex-simple-env:
	cd codex-simple-env && go run .

.PHONY: pg-codex-stdio
pg-codex-stdio:
	cd codex-stdio && go run .

.PHONY: pg-sandbox-bwrap pg-sandbox-bwrap-filesystem pg-sandbox-bwrap-gh pg-sandbox-bwrap-sockets
pg-sandbox-bwrap:
	python3 sandbox-bwrap/experiment.py all

pg-sandbox-bwrap-filesystem:
	python3 sandbox-bwrap/experiment.py filesystem

pg-sandbox-bwrap-gh:
	python3 sandbox-bwrap/experiment.py gh

pg-sandbox-bwrap-sockets:
	python3 sandbox-bwrap/experiment.py sockets

.PHONY: pg-sandbox-bwrap-gh-auth pg-sandbox-bwrap-gh-auth-export-token
pg-sandbox-bwrap-gh-auth:
	python3 sandbox-bwrap/experiment.py gh-auth

pg-sandbox-bwrap-gh-auth-export-token:
	python3 sandbox-bwrap/experiment.py gh-auth --export-host-token

.PHONY: pg-sandbox-bwrap-home-read-only pg-sandbox-bwrap-keyring-proxy
pg-sandbox-bwrap-home-read-only:
	python3 sandbox-bwrap/session_auth.py home-read-only

pg-sandbox-bwrap-keyring-proxy:
	python3 sandbox-bwrap/session_auth.py keyring-proxy
