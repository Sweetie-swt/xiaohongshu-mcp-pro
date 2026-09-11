#!/usr/bin/env python3
"""Resume an already-pending consumer security verification from Bunny.

This helper deliberately performs no login setup and accepts no arguments. It
only initializes a new MCP transport session and calls
consumer_complete_security_verification exactly once, allowing the server's
existing in-memory consumer browser/page to finish the pending flow.
"""

from __future__ import annotations

import sys

from creator_verify_otp_local import (
    CONSUMER_FLOW,
    MCPClientError,
    call_complete_security_verification,
    close_session,
    initialize,
    print_tool_result,
    tool_result_parts,
)

RESUME_REQUEST_TIMEOUT_SECONDS = 135


def run_resume_flow() -> int:
    if len(sys.argv) != 1:
        print("此工具不接受命令行参数。", file=sys.stderr)
        return 2

    session_id: str | None = None
    try:
        print("正在连接远端 MCP 并完成 initialize…")
        session_id = initialize(CONSUMER_FLOW)
        print("MCP initialize 成功；仅调用一次 consumer_complete_security_verification。")
        message = call_complete_security_verification(
            session_id,
            CONSUMER_FLOW,
            timeout_seconds=RESUME_REQUEST_TIMEOUT_SECONDS,
        )
        print_tool_result(message, "")
        is_error, texts, _ = tool_result_parts(message)
        if is_error:
            return 1
        if any(CONSUMER_FLOW.success_marker in text for text in texts):
            return 0
        print("未返回明确的 consumer finalize 成功状态。", file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        print("\n已取消；没有重新登录、发送短信或提交验证码。")
        return 130
    except MCPClientError as exc:
        print(f"错误：{exc}", file=sys.stderr)
        return 1
    finally:
        close_session(session_id, CONSUMER_FLOW.client_name)


if __name__ == "__main__":
    raise SystemExit(run_resume_flow())
