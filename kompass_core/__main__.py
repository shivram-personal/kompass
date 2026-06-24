"""Run kompass-core: ``python -m kompass_core``.

Binds 0.0.0.0 because core is the only container fronted by the Service;
ingress terminates here. The engine, by contrast, binds loopback only.
"""

from __future__ import annotations

import os

import uvicorn


def main() -> None:
    uvicorn.run(
        "kompass_core.main:app",
        host=os.environ.get("KOMPASS_HOST", "0.0.0.0"),
        port=int(os.environ.get("KOMPASS_PORT", "8080")),
        proxy_headers=True,
        forwarded_allow_ips="*",
    )


if __name__ == "__main__":
    main()
