"""`python -m library.openapi_export [path]` - write the OpenAPI document generated from the code."""

from __future__ import annotations

import sys
from pathlib import Path

import yaml

from library.main import create_app


def main() -> None:
    target = Path(sys.argv[1] if len(sys.argv) > 1 else "openapi.yaml")
    spec = create_app(manage_logging=False).openapi()
    target.write_text(yaml.safe_dump(spec, sort_keys=False, allow_unicode=True))
    print(f"wrote {target} ({len(spec['paths'])} paths)")


if __name__ == "__main__":
    main()
