"""Tool knowledge is separate from provider flags and controller grants."""

from pathlib import Path

from needs import Mount, Needs


def gh_needs(config: Path) -> Needs:
    # The experiment uses a synthetic directory, never the real gh account.
    return Needs([Mount(config, config, writable=True)], {
        'GH_CONFIG_DIR': str(config),
        'GH_NO_UPDATE_NOTIFIER': '1',
        'GH_PROMPT_DISABLED': '1',
    })
