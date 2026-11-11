# Transcript testing: where Codex and Claude differ

Both backends share the transcript format and the recording and replay
tooling. This lists only where their behaviour differs.

1. **Re-recording older versions.** Codex can re-record any retained version,
   because its version can be pinned. Claude can only record with the installed
   CLI, so older Claude fixtures can be replayed but not re-recorded.
2. **IDs that change between runs.** Claude replay rewrites the prompt ID the
   client generates each run. Codex replay rewrites nothing: its IDs come from
   Codex, and the client only echoes them.
3. **Interrupting a model change.** Codex only, because its model change runs a
   turn. A Claude model change has no turn to interrupt.
4. **Startup settings.** Codex replay checks the model and system prompt,
   because they go over the wire. Claude replay cannot check them, because both
   are launch arguments; unit and live tests cover them. Claude recordings
   cannot set a startup effort.
5. **Approvals.** The Claude recorder can decline approvals; the Codex recorder
   always accepts.
6. **Personal data.** Claude recordings are redacted (account email,
   organization, home paths). Codex recordings carry no such data.
7. **Your configuration.** Your Claude allow rules decide which tools ask for
   approval, so they change what a recording contains. Codex recordings do not
   depend on your configuration this way.
8. **Detecting a new CLI version's changes.** Codex compares a new
   version's recordings with the previous baseline when upgrading. Claude
   compares a live run of the scenarios with the fixtures on every
   integration run, because its CLI updates without an upgrade step.
