#!/usr/bin/env python3
"""Open one consumer QR login image and wait for its completion.

This helper accepts no secrets or positional arguments. The QR image is
received as MCP image content, written only to a temporary PNG for the local
Windows image viewer, and removed on a best-effort basis afterward.
"""

from __future__ import annotations

import base64
import os
import re
import sys
import tempfile
from typing import Any

from creator_verify_otp_local import (
    MCPClientError,
    OTPFlow,
    best_effort_remove_temp_image,
    close_session,
    initialize,
    post_message,
    print_tool_result,
    response_message,
    tool_result_parts,
)


QR_SERVER_WAIT_SECONDS = 180
QR_CLIENT_TIMEOUT_SECONDS = 195
QR_CLIENT_NAME = "bunny-local-consumer-qr"
PNG_SIGNATURE = b"\x89PNG\r\n\x1a\n"

CONSUMER_QR_FLOW = OTPFlow(
    key="consumer-qr",
    client_name=QR_CLIENT_NAME,
    verify_tool="",
    complete_security_tool="",
    success_marker="consumer 登录成功",
    security_image_prefix="bunny-consumer-qr-",
)


def _call_tool(
    tool_name: str,
    request_id: str,
    session_id: str | None,
    timeout_seconds: float,
) -> dict[str, Any] | None:
    request = {
        "jsonrpc": "2.0",
        "id": request_id,
        "method": "tools/call",
        "params": {"name": tool_name, "arguments": {}},
    }
    response = post_message(
        request,
        session_id,
        client_name=QR_CLIENT_NAME,
        timeout_seconds=timeout_seconds,
    )
    if response.status != 200:
        raise MCPClientError(f"{tool_name} 请求失败：HTTP {response.status}")
    return response_message(response)


def call_consumer_qr_login(session_id: str | None) -> dict[str, Any] | None:
    return _call_tool(
        "consumer_qr_login",
        "bunny-local-consumer-qr-login",
        session_id,
        75,
    )


def call_consumer_complete_qr_login(
    session_id: str | None,
) -> dict[str, Any] | None:
    return _call_tool(
        "consumer_complete_qr_login",
        "bunny-local-consumer-complete-qr-login",
        session_id,
        QR_CLIENT_TIMEOUT_SECONDS,
    )


def tool_status(message: dict[str, Any] | None) -> str:
    _, texts, _ = tool_result_parts(message)
    joined = " ".join(texts)
    match = re.search(r"\bstatus=(qr_ready|success|expired|timeout|failed)\b", joined)
    return match.group(1) if match else "failed"


def write_and_open_qr_image(
    images: list[tuple[str, str]],
) -> str:
    if not images:
        raise MCPClientError("consumer QR 登录结果未包含二维码图片")

    mime_type, encoded = images[0]
    if mime_type.lower() != "image/png":
        raise MCPClientError("consumer QR 登录图片不是 PNG")
    try:
        image_data = base64.b64decode(encoded, validate=True)
    except Exception as exc:
        raise MCPClientError(f"consumer QR 登录图片不是有效的 Base64 PNG：{exc}") from None
    if not image_data.startswith(PNG_SIGNATURE):
        raise MCPClientError("consumer QR 登录图片不是有效的 PNG")

    fd, path = tempfile.mkstemp(
        prefix="bunny-consumer-qr-",
        suffix=".png",
        dir=tempfile.gettempdir(),
    )
    try:
        with os.fdopen(fd, "wb") as image_file:
            image_file.write(image_data)
        if not hasattr(os, "startfile"):
            raise MCPClientError("当前系统没有 Windows 默认图片查看器接口")

        os.startfile(path)  # type: ignore[attr-defined]
    except Exception:
        best_effort_remove_temp_image(path)
        raise
    return path


def run_consumer_qr_flow() -> int:
    if len(sys.argv) != 1:
        print("此工具不接受命令行参数；二维码登录不需要手机号或验证码。", file=sys.stderr)
        return 2

    session_id: str | None = None
    image_path: str | None = None
    try:
        print("正在连接远端 MCP 并完成 initialize…")
        session_id = initialize(CONSUMER_QR_FLOW)
        print("MCP initialize 成功。")

        qr_message = call_consumer_qr_login(session_id)
        print_tool_result(qr_message, "")
        qr_is_error, _, images = tool_result_parts(qr_message)
        if qr_is_error or tool_status(qr_message) != "qr_ready":
            print("consumer QR 登录未准备成功；不会自动重试。", file=sys.stderr)
            return 1

        image_path = write_and_open_qr_image(images)
        print("二维码已用 Windows 默认图片查看器打开，请使用小红书 App 扫码并在手机确认。")
        complete_message = call_consumer_complete_qr_login(session_id)
        print_tool_result(complete_message, "")
        complete_is_error, _, _ = tool_result_parts(complete_message)
        return 0 if not complete_is_error and tool_status(complete_message) == "success" else 1
    except KeyboardInterrupt:
        print("\n已取消本地等待；未重新创建二维码、未发送短信、未提交 OTP。")
        return 130
    except MCPClientError as exc:
        print(f"错误：{exc}", file=sys.stderr)
        return 1
    finally:
        best_effort_remove_temp_image(image_path)
        close_session(session_id, QR_CLIENT_NAME)


if __name__ == "__main__":
    raise SystemExit(run_consumer_qr_flow())
