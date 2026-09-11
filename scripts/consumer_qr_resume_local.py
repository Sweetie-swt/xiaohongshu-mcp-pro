#!/usr/bin/env python3
"""Resume an already-pending consumer QR login from Bunny.

No QR is created or refreshed. This helper only calls the completion tool once
against the server-side pending browser/page.
"""

from __future__ import annotations

import sys

from consumer_qr_login_local import (
    CONSUMER_QR_FLOW,
    QR_CLIENT_NAME,
    QR_CLIENT_TIMEOUT_SECONDS,
    call_consumer_complete_qr_login,
    close_session,
    initialize,
    print_tool_result,
    tool_result_parts,
    tool_status,
)


def run_resume_flow() -> int:
    if len(sys.argv) != 1:
        print("此工具不接受命令行参数。", file=sys.stderr)
        return 2

    session_id: str | None = None
    try:
        print("正在连接远端 MCP 并完成 initialize…")
        session_id = initialize(CONSUMER_QR_FLOW)
        print("MCP initialize 成功；仅调用一次 consumer_complete_qr_login。")
        message = call_consumer_complete_qr_login(session_id)
        print_tool_result(message, "")
        is_error, _, _ = tool_result_parts(message)
        return 0 if not is_error and tool_status(message) == "success" else 1
    except KeyboardInterrupt:
        print("\n已取消本地等待；没有重新创建二维码、发送短信或提交 OTP。")
        return 130
    except Exception as exc:
        print(f"错误：{exc}", file=sys.stderr)
        return 1
    finally:
        close_session(session_id, QR_CLIENT_NAME)


if __name__ == "__main__":
    raise SystemExit(run_resume_flow())
