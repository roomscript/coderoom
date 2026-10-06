"""Technology-independent fixture needs; not a proposed production API."""

from dataclasses import dataclass, field
from pathlib import Path


@dataclass
class Mount:
    source: Path
    destination: Path
    writable: bool = False


@dataclass
class Needs:
    mounts: list[Mount] = field(default_factory=list)
    environment: dict[str, str] = field(default_factory=dict)

