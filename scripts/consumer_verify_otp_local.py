#!/usr/bin/env python3
"""Submit a consumer www OTP through the Bunny local-safe MCP flow.

The OTP is read with getpass, is never accepted as a command-line argument,
and is only passed to consumer_verify_otp after the MCP handshake succeeds.
"""

from __future__ import annotations

from creator_verify_otp_local import CONSUMER_FLOW, run_flow


if __name__ == "__main__":
    raise SystemExit(run_flow(CONSUMER_FLOW))
