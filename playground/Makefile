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

.PHONY: pg-claude-stream-json pg-claude-stream-json-e1 pg-claude-stream-json-e2 pg-claude-stream-json-e3 pg-claude-stream-json-e4 pg-claude-stream-json-e5 pg-claude-stream-json-e6 pg-claude-stream-json-e7 pg-claude-stream-json-e8 pg-claude-stream-json-e9 pg-claude-stream-json-e10 pg-claude-stream-json-e11 pg-claude-stream-json-e12 pg-claude-stream-json-e13
pg-claude-stream-json:
	cd claude-stream-json && go run . -exp all

pg-claude-stream-json-e1:
	cd claude-stream-json && go run . -exp e1

pg-claude-stream-json-e2:
	cd claude-stream-json && go run . -exp e2

pg-claude-stream-json-e3:
	cd claude-stream-json && go run . -exp e3

pg-claude-stream-json-e4:
	cd claude-stream-json && go run . -exp e4

pg-claude-stream-json-e5:
	cd claude-stream-json && go run . -exp e5

pg-claude-stream-json-e6:
	cd claude-stream-json && go run . -exp e6

pg-claude-stream-json-e7:
	cd claude-stream-json && go run . -exp e7

pg-claude-stream-json-e8:
	cd claude-stream-json && go run . -exp e8

pg-claude-stream-json-e9:
	cd claude-stream-json && go run . -exp e9

pg-claude-stream-json-e10:
	cd claude-stream-json && go run . -exp e10

pg-claude-stream-json-e11:
	cd claude-stream-json && go run . -exp e11

pg-claude-stream-json-e12:
	cd claude-stream-json && go run . -exp e12

pg-claude-stream-json-e13:
	cd claude-stream-json && go run . -exp e13
