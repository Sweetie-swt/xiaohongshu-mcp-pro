import base64
import importlib.util
import pathlib
import sys
import unittest
from unittest import mock


SCRIPTS_DIR = pathlib.Path(__file__).parent
HELPER_PATH = SCRIPTS_DIR / "consumer_qr_login_local.py"
RESUME_PATH = SCRIPTS_DIR / "consumer_qr_resume_local.py"
sys.path.insert(0, str(SCRIPTS_DIR))
try:
    spec = importlib.util.spec_from_file_location("consumer_qr_login_local", HELPER_PATH)
    assert spec and spec.loader
    helper = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = helper
    spec.loader.exec_module(helper)

    resume_spec = importlib.util.spec_from_file_location(
        "consumer_qr_resume_local", RESUME_PATH
    )
    assert resume_spec and resume_spec.loader
    resume_helper = importlib.util.module_from_spec(resume_spec)
    sys.modules[resume_spec.name] = resume_helper
    resume_spec.loader.exec_module(resume_helper)
finally:
    sys.path.pop(0)


PNG = b"\x89PNG\r\n\x1a\nfixture"


def result(text: str, is_error: bool = False, image: bytes | None = None) -> dict:
    content = [{"type": "text", "text": text}]
    if image is not None:
        content.append(
            {
                "type": "image",
                "mimeType": "image/png",
                "data": base64.b64encode(image).decode("ascii"),
            }
        )
    return {"result": {"isError": is_error, "content": content}}


class ConsumerQRLoginLocalTests(unittest.TestCase):
    def test_client_wait_exceeds_server_wait(self) -> None:
        self.assertGreater(
            helper.QR_CLIENT_TIMEOUT_SECONDS, helper.QR_SERVER_WAIT_SECONDS
        )

    def test_complete_request_uses_long_client_timeout(self) -> None:
        response = mock.Mock(status=200)
        with (
            mock.patch.object(helper, "post_message", return_value=response) as post_mock,
            mock.patch.object(helper, "response_message", return_value=result("status=timeout")),
        ):
            helper.call_consumer_complete_qr_login("session")
        request, session_id = post_mock.call_args.args[:2]
        self.assertEqual(session_id, "session")
        self.assertEqual(request["params"]["name"], "consumer_complete_qr_login")
        self.assertEqual(
            post_mock.call_args.kwargs["timeout_seconds"], helper.QR_CLIENT_TIMEOUT_SECONDS
        )

    def test_happy_path_calls_qr_and_complete_once(self) -> None:
        calls: list[str] = []
        qr_message = result("status=qr_ready", image=PNG)
        complete_message = result("status=success\nconsumer 登录成功")

        with (
            mock.patch.object(
                helper, "initialize", side_effect=lambda flow: calls.append("initialize") or "session"
            ),
            mock.patch.object(
                helper,
                "call_consumer_qr_login",
                side_effect=lambda session_id: calls.append("qr") or qr_message,
            ) as qr_call,
            mock.patch.object(
                helper,
                "write_and_open_qr_image",
                side_effect=lambda images: calls.append("open") or "temp.png",
            ),
            mock.patch.object(
                helper,
                "call_consumer_complete_qr_login",
                side_effect=lambda session_id: calls.append("complete") or complete_message,
            ) as complete_call,
            mock.patch.object(helper, "print_tool_result"),
            mock.patch.object(
                helper, "best_effort_remove_temp_image", side_effect=lambda path: calls.append("remove")
            ),
            mock.patch.object(
                helper, "close_session", side_effect=lambda session_id, client: calls.append("close")
            ),
            mock.patch.object(helper.sys, "argv", [str(HELPER_PATH)]),
        ):
            self.assertEqual(helper.run_consumer_qr_flow(), 0)

        self.assertEqual(calls, ["initialize", "qr", "open", "complete", "remove", "close"])
        qr_call.assert_called_once_with("session")
        complete_call.assert_called_once_with("session")

    def test_failed_qr_does_not_complete_or_retry(self) -> None:
        with (
            mock.patch.object(helper, "initialize", return_value="session"),
            mock.patch.object(
                helper,
                "call_consumer_qr_login",
                return_value=result("status=failed", is_error=True),
            ) as qr_call,
            mock.patch.object(helper, "call_consumer_complete_qr_login") as complete_call,
            mock.patch.object(helper, "print_tool_result"),
            mock.patch.object(helper, "close_session"),
            mock.patch.object(helper.sys, "argv", [str(HELPER_PATH)]),
        ):
            self.assertEqual(helper.run_consumer_qr_flow(), 1)
        qr_call.assert_called_once_with("session")
        complete_call.assert_not_called()

    def test_qr_image_requires_png(self) -> None:
        with self.assertRaises(helper.MCPClientError):
            helper.write_and_open_qr_image(
                [("image/jpeg", base64.b64encode(PNG).decode("ascii"))]
            )
        with self.assertRaises(helper.MCPClientError):
            helper.write_and_open_qr_image([("image/png", "not-base64")])

    def test_resume_calls_only_complete_once(self) -> None:
        calls: list[str] = []
        complete_message = result("status=success\nconsumer 登录成功")
        with (
            mock.patch.object(
                resume_helper, "initialize", side_effect=lambda flow: calls.append("initialize") or "session"
            ),
            mock.patch.object(
                resume_helper,
                "call_consumer_complete_qr_login",
                side_effect=lambda session_id: calls.append("complete") or complete_message,
            ) as complete_call,
            mock.patch.object(resume_helper, "print_tool_result"),
            mock.patch.object(
                resume_helper, "close_session", side_effect=lambda session_id, client: calls.append("close")
            ),
            mock.patch.object(resume_helper.sys, "argv", [str(RESUME_PATH)]),
        ):
            self.assertEqual(resume_helper.run_resume_flow(), 0)

        self.assertEqual(calls, ["initialize", "complete", "close"])
        complete_call.assert_called_once_with("session")

    def test_helpers_do_not_reference_phone_or_otp_login_calls(self) -> None:
        source = HELPER_PATH.read_text(encoding="utf-8")
        resume_source = RESUME_PATH.read_text(encoding="utf-8")
        for tool_name in (
            "consumer_phone_login",
            "consumer_verify_otp",
            "consumer_complete_security_verification",
        ):
            self.assertNotIn(tool_name, source)
            self.assertNotIn(tool_name, resume_source)
        self.assertNotIn("call_consumer_qr_login", resume_source)


if __name__ == "__main__":
    unittest.main()
