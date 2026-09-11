#!/usr/bin/env python3
"""Submit a creator OTP to the user's remote MCP server from a local terminal.

This helper intentionally accepts no command-line arguments. The OTP is read
with getpass, is never logged or written to disk, and is only sent in the
creator_verify_otp request after the MCP initialize handshake succeeds.
"""

from __future__ import annotations

import base64
import getpass
import json
import os
import sys
import tempfile
from dataclasses import dataclass
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen


MCP_URL = "https://xiaohongshu-mcp-probe-production.up.railway.app/mcp"
MCP_PROTOCOL_VERSION = "2025-06-18"
CLIENT_NAME = "bunny-local-creator-otp"
CLIENT_VERSION = "0.1.0"
REQUEST_TIMEOUT_SECONDS = 30


@dataclass(frozen=True)
class OTPFlow:
    key: str
    client_name: str
    verify_tool: str
    complete_security_tool: str
    success_marker: str
    security_image_prefix: str
    phone_tool: str | None = None


CREATOR_FLOW = OTPFlow(
    key="creator",
    client_name="bunny-local-creator-otp",
    verify_tool="creator_verify_otp",
    complete_security_tool="creator_complete_security_verification",
    success_marker="creator 登录成功",
    security_image_prefix="bunny-creator-security-",
)

CONSUMER_FLOW = OTPFlow(
    key="consumer",
    client_name="bunny-local-consumer-otp",
    verify_tool="consumer_verify_otp",
    complete_security_tool="consumer_complete_security_verification",
    success_marker="consumer 登录成功",
    security_image_prefix="bunny-consumer-security-",
    phone_tool="consumer_phone_login",
)


class MCPClientError(RuntimeError):
    """An MCP transport or protocol error that is safe to show to the user."""


@dataclass
class HTTPMessage:
    status: int
    headers: Any
    body: bytes


def post_message(
    payload: dict[str, Any], session_id: str | None = None, client_name: str = CLIENT_NAME
) -> HTTPMessage:
    """POST one MCP JSON-RPC message using Streamable HTTP headers."""

    headers = {
        "Accept": "application/json, text/event-stream",
        "Content-Type": "application/json",
        "Mcp-Protocol-Version": MCP_PROTOCOL_VERSION,
        "User-Agent": f"{client_name}/{CLIENT_VERSION}",
    }
    if session_id:
        headers["Mcp-Session-Id"] = session_id

    body = json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode(
        "utf-8"
    )
    request = Request(MCP_URL, data=body, headers=headers, method="POST")
    try:
        with urlopen(request, timeout=REQUEST_TIMEOUT_SECONDS) as response:
            return HTTPMessage(response.status, response.headers, response.read())
    except HTTPError as exc:
        # Do not include the request body or any echoed arguments in an error.
        raise MCPClientError(f"远端 MCP HTTP 错误：HTTP {exc.code}") from None
    except URLError as exc:
        raise MCPClientError(f"无法连接远端 MCP：{exc.reason}") from None
    except TimeoutError:
        raise MCPClientError("连接远端 MCP 超时") from None


def decode_messages(response: HTTPMessage) -> list[dict[str, Any]]:
    """Decode JSONResponse output and the standard SSE fallback."""

    text = response.body.decode("utf-8", errors="replace")
    if not text.strip():
        return []

    content_type = response.headers.get_content_type()
    if content_type == "application/json" or not text.lstrip().startswith("data:"):
        try:
            message = json.loads(text)
        except json.JSONDecodeError as exc:
            raise MCPClientError("远端 MCP 返回了无法解析的 JSON") from exc
        if not isinstance(message, dict):
            raise MCPClientError("远端 MCP 返回的 JSON 不是对象")
        return [message]

    messages: list[dict[str, Any]] = []
    event_data: list[str] = []
    for line in text.splitlines() + [""]:
        if line == "":
            if event_data:
                try:
                    message = json.loads("\n".join(event_data))
                except json.JSONDecodeError as exc:
                    raise MCPClientError("远端 MCP SSE 返回了无法解析的 JSON") from exc
                if isinstance(message, dict):
                    messages.append(message)
                event_data = []
            continue
        if line.startswith("data:"):
            event_data.append(line[5:].lstrip())
    return messages


def response_message(response: HTTPMessage) -> dict[str, Any] | None:
    messages = decode_messages(response)
    return messages[0] if messages else None


def initialize(flow: OTPFlow = CREATOR_FLOW) -> str | None:
    """Perform initialize and notifications/initialized, returning session ID."""

    initialize_request = {
        "jsonrpc": "2.0",
        "id": "bunny-local-init",
        "method": "initialize",
        "params": {
            "protocolVersion": MCP_PROTOCOL_VERSION,
            "capabilities": {},
            "clientInfo": {"name": flow.client_name, "version": CLIENT_VERSION},
        },
    }
    response = post_message(initialize_request, client_name=flow.client_name)
    if response.status != 200:
        raise MCPClientError(f"MCP initialize 失败：HTTP {response.status}")
    message = response_message(response)
    if not message or "error" in message:
        raise MCPClientError("MCP initialize 返回错误")
    if message.get("id") != initialize_request["id"] or "result" not in message:
        raise MCPClientError("MCP initialize 返回格式不正确")

    result = message["result"]
    if not isinstance(result, dict) or not result.get("protocolVersion"):
        raise MCPClientError("MCP initialize 缺少协商后的 protocolVersion")

    session_id = response.headers.get("Mcp-Session-Id")
    initialized_request = {
        "jsonrpc": "2.0",
        "method": "notifications/initialized",
        "params": {},
    }
    initialized_response = post_message(
        initialized_request, session_id, client_name=flow.client_name
    )
    if initialized_response.status not in (200, 202, 204):
        raise MCPClientError(
            f"MCP notifications/initialized 失败：HTTP {initialized_response.status}"
        )
    return session_id


def call_verify_otp(
    otp: str, session_id: str | None, flow: OTPFlow
) -> dict[str, Any] | None:
    request = {
        "jsonrpc": "2.0",
        "id": f"bunny-local-{flow.key}-verify-otp",
        "method": "tools/call",
        "params": {
            "name": flow.verify_tool,
            "arguments": {"otp": otp},
        },
    }
    response = post_message(request, session_id, client_name=flow.client_name)
    if response.status != 200:
        raise MCPClientError(f"{flow.verify_tool} 请求失败：HTTP {response.status}")
    return response_message(response)


def call_creator_verify_otp(otp: str, session_id: str | None) -> dict[str, Any] | None:
    return call_verify_otp(otp, session_id, CREATOR_FLOW)


def call_phone_login(
    phone: str, session_id: str | None, flow: OTPFlow
) -> dict[str, Any] | None:
    if not flow.phone_tool:
        raise MCPClientError(f"{flow.key} flow 没有手机号登录工具")
    request = {
        "jsonrpc": "2.0",
        "id": f"bunny-local-{flow.key}-phone-login",
        "method": "tools/call",
        "params": {
            "name": flow.phone_tool,
            "arguments": {"phone": phone},
        },
    }
    response = post_message(request, session_id, client_name=flow.client_name)
    if response.status != 200:
        raise MCPClientError(f"{flow.phone_tool} 请求失败：HTTP {response.status}")
    return response_message(response)


def call_complete_security_verification(
    session_id: str | None, flow: OTPFlow
) -> dict[str, Any] | None:
    request = {
        "jsonrpc": "2.0",
        "id": f"bunny-local-{flow.key}-complete-security-verification",
        "method": "tools/call",
        "params": {
            "name": flow.complete_security_tool,
            "arguments": {},
        },
    }
    response = post_message(request, session_id, client_name=flow.client_name)
    if response.status != 200:
        raise MCPClientError(f"{flow.complete_security_tool} 请求失败：HTTP {response.status}")
    return response_message(response)


def call_creator_complete_security_verification(
    session_id: str | None,
) -> dict[str, Any] | None:
    return call_complete_security_verification(session_id, CREATOR_FLOW)


def close_session(session_id: str | None, client_name: str = CLIENT_NAME) -> None:
    """Best-effort Streamable HTTP session cleanup; never logs request data."""

    if not session_id:
        return
    request = Request(
        MCP_URL,
        headers={
            "Accept": "application/json, text/event-stream",
            "Mcp-Protocol-Version": MCP_PROTOCOL_VERSION,
            "Mcp-Session-Id": session_id,
            "User-Agent": f"{client_name}/{CLIENT_VERSION}",
        },
        method="DELETE",
    )
    try:
        with urlopen(request, timeout=REQUEST_TIMEOUT_SECONDS):
            pass
    except Exception:
        # Session termination is cleanup only. Do not obscure the tool result.
        pass


def redact(text: str, secret: str) -> str:
    return text.replace(secret, "[已隐藏]") if secret else text


def tool_result_parts(
    message: dict[str, Any] | None,
) -> tuple[bool, list[str], list[tuple[str, str]]]:
    """Return (is_error, text parts, (mime type, base64 data) image parts)."""

    if not message:
        return False, [], []
    if "error" in message:
        return True, [str(message.get("error"))], []

    result = message.get("result")
    if not isinstance(result, dict):
        return False, ["远端 MCP 返回格式不正确"], []

    texts: list[str] = []
    images: list[tuple[str, str]] = []
    content = result.get("content")
    if isinstance(content, list):
        for item in content:
            if not isinstance(item, dict):
                continue
            if item.get("type") == "text":
                texts.append(str(item.get("text", "")))
            elif item.get("type") == "image":
                mime_type = str(
                    item.get("mimeType") or item.get("mime_type") or "image/png"
                )
                data = item.get("data")
                if isinstance(data, str) and data:
                    images.append((mime_type, data))
    return bool(result.get("isError")), texts, images


def print_tool_result(
    message: dict[str, Any] | None,
    secret: str,
    redact_values: tuple[str, ...] = (),
) -> None:
    is_error, texts, images = tool_result_parts(message)
    if not message:
        print("远端 MCP 没有返回可显示的结果。")
        return
    if texts:
        prefix = "MCP 调用失败：" if is_error else ""
        output = "\n".join(texts)
        for value in (secret, *redact_values):
            output = redact(output, value)
        print(prefix + output)
    elif images:
        print("远端 MCP 返回了图片，但没有可显示的文字结果。")
    else:
        print("远端 MCP 没有返回可显示的结果。")


def is_security_verification_required(message: dict[str, Any] | None) -> bool:
    is_error, texts, _ = tool_result_parts(message)
    if is_error:
        return False
    return any(
        "security_verification_required" in text
        or "需要扫码完成安全验证" in text
        for text in texts
    )


def is_direct_success_for_flow(
    message: dict[str, Any] | None, flow: OTPFlow
) -> bool:
    is_error, texts, _ = tool_result_parts(message)
    if is_error or is_security_verification_required(message):
        return False
    return any(flow.success_marker in text for text in texts)


def is_direct_success(message: dict[str, Any] | None) -> bool:
    return is_direct_success_for_flow(message, CREATOR_FLOW)


def write_and_open_security_image(
    images: list[tuple[str, str]], prefix: str = CREATOR_FLOW.security_image_prefix
) -> str | None:
    if not images:
        print("安全验证结果未包含可显示的二维码截图。")
        return None

    mime_type, encoded = images[0]
    try:
        image_data = base64.b64decode(encoded, validate=True)
    except Exception as exc:
        raise MCPClientError(f"安全验证截图不是有效的 Base64 图片：{exc}") from None
    if not image_data:
        raise MCPClientError("安全验证截图为空")

    suffix = ".png" if mime_type.lower() == "image/png" else ".png"
    fd, path = tempfile.mkstemp(
        prefix=prefix,
        suffix=suffix,
        dir=tempfile.gettempdir(),
    )
    try:
        with os.fdopen(fd, "wb") as image_file:
            image_file.write(image_data)
        if not hasattr(os, "startfile"):
            raise MCPClientError("当前系统没有 Windows 默认图片查看器接口")
        os.startfile(path)  # type: ignore[attr-defined]
    except Exception:
        try:
            os.unlink(path)
        except OSError:
            pass
        raise
    print(f"二维码截图已用默认图片查看器打开：{path}")
    return path


def best_effort_remove_temp_image(path: str | None) -> None:
    if not path:
        return
    try:
        os.unlink(path)
    except OSError as exc:
        print(f"临时截图暂时无法删除（可能仍被图片查看器占用）：{path}；原因：{exc}")


def read_otp() -> str:
    while True:
        otp = getpass.getpass("请输入 6 位短信验证码（输入不会回显）：").strip()
        if is_valid_otp(otp):
            return otp
        print("验证码必须是 6 位 ASCII 数字，请重新输入。")


def is_valid_otp(otp: str) -> bool:
    return len(otp) == 6 and otp.isascii() and otp.isdigit()


def run_flow(flow: OTPFlow) -> int:
    if len(sys.argv) != 1:
        print("此工具不接受命令行参数；验证码必须交互式输入。", file=sys.stderr)
        return 2

    session_id: str | None = None
    secret = ""
    security_image_path: str | None = None
    try:
        print("正在连接远端 MCP 并完成 initialize…")
        session_id = initialize(flow)
        print("MCP initialize 成功。")
        print("连接已准备好；现在等待你本人输入验证码。")
        secret = read_otp()
        message = call_verify_otp(secret, session_id, flow)
        print_tool_result(message, secret)
        is_error, _, images = tool_result_parts(message)
        if is_error:
            return 1

        if is_security_verification_required(message):
            security_image_path = write_and_open_security_image(
                images, prefix=flow.security_image_prefix
            )
            print("请用手机完成小红书安全验证扫码；完成后回到此终端按 Enter。")
            input("已扫码后按 Enter 继续：")
            complete_message = call_complete_security_verification(session_id, flow)
            print_tool_result(complete_message, secret)
            complete_is_error, _, _ = tool_result_parts(complete_message)
            input("处理完成，按 Enter 关闭：")
            return 1 if complete_is_error else 0

        input("处理完成，按 Enter 关闭：")
        return 0 if is_direct_success_for_flow(message, flow) else 1
    except KeyboardInterrupt:
        print(f"\n已取消，未调用 {flow.verify_tool}。")
        return 130
    except MCPClientError as exc:
        print(f"错误：{exc}", file=sys.stderr)
        return 1
    finally:
        # Best effort only; the OTP is not written to a file, log, or command line.
        best_effort_remove_temp_image(security_image_path)
        secret = ""
        close_session(session_id, flow.client_name)


def main() -> int:
    return run_flow(CREATOR_FLOW)


if __name__ == "__main__":
    raise SystemExit(main())
