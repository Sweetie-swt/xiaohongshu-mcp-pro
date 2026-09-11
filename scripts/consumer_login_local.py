#!/usr/bin/env python3
"""Complete the www consumer phone login from Bunny's local terminal.

The phone number and OTP are read interactively with getpass. Neither is
accepted as a command-line argument, printed, logged, or written to a file.
This script performs exactly one consumer_phone_login call and never retries
the SMS request automatically.
"""

from __future__ import annotations

import getpass
import re
import sys
from typing import Any

from creator_verify_otp_local import (
    CONSUMER_FLOW,
    MCPClientError,
    best_effort_remove_temp_image,
    call_complete_security_verification,
    call_phone_login,
    call_verify_otp,
    close_session,
    initialize,
    is_direct_success_for_flow,
    is_security_verification_required,
    print_tool_result,
    read_otp,
    tool_result_parts,
    write_and_open_security_image,
)


PHONE_PATTERN = re.compile(r"1[3-9]\d{9}")


def is_valid_phone(phone: str) -> bool:
    return bool(PHONE_PATTERN.fullmatch(phone))


def read_phone() -> str:
    while True:
        phone = getpass.getpass("请输入 11 位手机号（输入不会回显）：").strip()
        if is_valid_phone(phone):
            return phone
        print("手机号必须是中国大陆 11 位手机号，请重新输入。")


def phone_login_status(message: dict[str, Any] | None) -> str:
    is_error, texts, _ = tool_result_parts(message)
    if is_error:
        return "failed"
    joined = " ".join(texts)
    match = re.search(r"\bstatus=(confirmed|uncertain|failed)\b", joined)
    if match:
        return match.group(1)
    if "无法从页面确认" in joined:
        return "uncertain"
    if "验证码已发送" in joined:
        return "confirmed"
    return "failed"


def run_consumer_flow() -> int:
    if len(sys.argv) != 1:
        print("此工具不接受命令行参数；手机号和验证码必须交互式输入。", file=sys.stderr)
        return 2

    session_id: str | None = None
    phone = ""
    otp = ""
    security_image_path: str | None = None
    try:
        print("正在连接远端 MCP 并完成 initialize…")
        session_id = initialize(CONSUMER_FLOW)
        print("MCP initialize 成功。")

        phone = read_phone()
        message = call_phone_login(phone, session_id, CONSUMER_FLOW)
        status = phone_login_status(message)
        print(f"consumer_phone_login status={status}")
        print_tool_result(message, phone, redact_values=(phone,))
        if status == "failed":
            print("验证码发送失败，helper 结束；不会自动重试。", file=sys.stderr)
            return 1
        if status not in {"confirmed", "uncertain"}:
            print("未收到明确的 confirmed/uncertain 状态，helper 结束。", file=sys.stderr)
            return 1

        print("请在手机收到验证码后继续；验证码只在本地隐藏输入。")
        otp = read_otp()
        message = call_verify_otp(otp, session_id, CONSUMER_FLOW)
        print_tool_result(message, otp, redact_values=(phone,))
        is_error, _, images = tool_result_parts(message)
        if is_error:
            return 1

        if is_security_verification_required(message):
            security_image_path = write_and_open_security_image(
                images, prefix=CONSUMER_FLOW.security_image_prefix
            )
            print("请用手机完成小红书安全验证；完成后回到此终端按 Enter。")
            input("已完成安全验证后按 Enter 继续：")
            complete_message = call_complete_security_verification(
                session_id, CONSUMER_FLOW
            )
            print_tool_result(complete_message, otp, redact_values=(phone,))
            complete_is_error, _, _ = tool_result_parts(complete_message)
            return 1 if complete_is_error else 0

        return 0 if is_direct_success_for_flow(message, CONSUMER_FLOW) else 1
    except KeyboardInterrupt:
        print("\n已取消；未继续提交验证码。")
        return 130
    except MCPClientError as exc:
        print(f"错误：{exc}", file=sys.stderr)
        return 1
    finally:
        # Best effort only. Secrets are cleared and the MCP session is closed.
        phone = ""
        otp = ""
        best_effort_remove_temp_image(security_image_path)
        close_session(session_id, CONSUMER_FLOW.client_name)


if __name__ == "__main__":
    raise SystemExit(run_consumer_flow())
