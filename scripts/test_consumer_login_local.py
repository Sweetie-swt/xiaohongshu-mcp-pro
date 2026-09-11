import contextlib
import importlib.util
import io
import pathlib
import sys
import unittest
from unittest import mock


SCRIPTS_DIR = pathlib.Path(__file__).parent
HELPER_PATH = SCRIPTS_DIR / "consumer_login_local.py"
sys.path.insert(0, str(SCRIPTS_DIR))
try:
    spec = importlib.util.spec_from_file_location("consumer_login_local", HELPER_PATH)
    assert spec and spec.loader
    helper = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = helper
    spec.loader.exec_module(helper)
finally:
    sys.path.pop(0)


def result(text: str, is_error: bool = False) -> dict:
    return {
        "result": {
            "isError": is_error,
            "content": [{"type": "text", "text": text}],
        }
    }


class ConsumerLoginLocalTests(unittest.TestCase):
    def test_phone_is_hidden_local_input_and_not_argv(self) -> None:
        with mock.patch.object(
            helper.getpass, "getpass", return_value="13800138000"
        ) as getpass_mock:
            self.assertEqual(helper.read_phone(), "13800138000")
        getpass_mock.assert_called_once()
        self.assertFalse(helper.is_valid_phone("1380013800"))
        self.assertFalse(helper.is_valid_phone("13800138000 extra"))
        self.assertNotIn("sys.argv[1]", HELPER_PATH.read_text(encoding="utf-8"))

    def test_positional_arguments_are_rejected_before_initialize(self) -> None:
        with (
            mock.patch.object(helper, "initialize") as initialize_mock,
            mock.patch.object(
                helper.sys, "argv", ["consumer_login_local.py", "13800138000"]
            ),
        ):
            self.assertEqual(helper.run_consumer_flow(), 2)
        initialize_mock.assert_not_called()

    def test_uncertain_keeps_flow_and_calls_consumer_phone_login_once(self) -> None:
        phone_call = mock.Mock(
            return_value=result("status=uncertain\n状态无法从页面确认")
        )
        verify_call = mock.Mock(return_value=result("consumer 登录成功"))
        with (
            mock.patch.object(helper, "initialize", return_value="session"),
            mock.patch.object(helper, "read_phone", return_value="13800138000"),
            mock.patch.object(helper, "call_phone_login", phone_call),
            mock.patch.object(helper, "read_otp", return_value="123456") as otp_mock,
            mock.patch.object(helper, "call_verify_otp", verify_call),
            mock.patch.object(helper, "print_tool_result"),
            mock.patch.object(helper, "close_session"),
            mock.patch.object(helper.sys, "argv", ["consumer_login_local.py"]),
        ):
            self.assertEqual(helper.run_consumer_flow(), 0)
        phone_call.assert_called_once_with(
            "13800138000", "session", helper.CONSUMER_FLOW
        )
        otp_mock.assert_called_once()
        verify_call.assert_called_once()

    def test_failed_does_not_read_or_submit_otp(self) -> None:
        phone_call = mock.Mock(
            return_value=result("status=failed\n发送失败", is_error=True)
        )
        with (
            mock.patch.object(helper, "initialize", return_value="session"),
            mock.patch.object(helper, "read_phone", return_value="13800138000"),
            mock.patch.object(helper, "call_phone_login", phone_call),
            mock.patch.object(helper, "read_otp") as otp_mock,
            mock.patch.object(helper, "call_verify_otp") as verify_call,
            mock.patch.object(helper, "print_tool_result"),
            mock.patch.object(helper, "close_session"),
            mock.patch.object(helper.sys, "argv", ["consumer_login_local.py"]),
        ):
            self.assertEqual(helper.run_consumer_flow(), 1)
        otp_mock.assert_not_called()
        verify_call.assert_not_called()

    def test_security_verification_uses_existing_image_and_completion_flow(self) -> None:
        security = {
            "result": {
                "isError": False,
                "content": [
                    {
                        "type": "text",
                        "text": "security_verification_required：需要扫码完成安全验证",
                    },
                    {"type": "image", "mimeType": "image/png", "data": "cG5n"},
                ],
            }
        }
        complete_call = mock.Mock(return_value=result("consumer 登录成功"))
        with (
            mock.patch.object(helper, "initialize", return_value="session"),
            mock.patch.object(helper, "read_phone", return_value="13800138000"),
            mock.patch.object(
                helper,
                "call_phone_login",
                return_value=result("status=confirmed\n验证码已发送"),
            ),
            mock.patch.object(helper, "read_otp", return_value="123456"),
            mock.patch.object(helper, "call_verify_otp", return_value=security),
            mock.patch.object(
                helper, "write_and_open_security_image", return_value="temp.png"
            ) as image_call,
            mock.patch.object(
                helper, "call_complete_security_verification", complete_call
            ),
            mock.patch.object(helper, "print_tool_result"),
            mock.patch.object(helper, "close_session"),
            mock.patch.object(helper, "best_effort_remove_temp_image"),
            mock.patch("builtins.input"),
            mock.patch.object(helper.sys, "argv", ["consumer_login_local.py"]),
        ):
            self.assertEqual(helper.run_consumer_flow(), 0)
        image_call.assert_called_once()
        complete_call.assert_called_once()

    def test_secrets_are_not_written_to_stdout_or_argv(self) -> None:
        output = io.StringIO()
        with (
            mock.patch.object(helper, "initialize", return_value="session"),
            mock.patch.object(helper, "read_phone", return_value="13800138000"),
            mock.patch.object(
                helper,
                "call_phone_login",
                return_value=result("status=confirmed\n验证码已发送"),
            ),
            mock.patch.object(helper, "read_otp", return_value="123456"),
            mock.patch.object(helper, "call_verify_otp", return_value=result("consumer 登录成功")),
            mock.patch.object(helper, "close_session"),
            mock.patch.object(helper.sys, "argv", ["consumer_login_local.py"]),
            contextlib.redirect_stdout(output),
        ):
            self.assertEqual(helper.run_consumer_flow(), 0)
        self.assertNotIn("13800138000", output.getvalue())
        self.assertNotIn("123456", output.getvalue())

    def test_creator_flow_remains_separate(self) -> None:
        creator = sys.modules["creator_verify_otp_local"]
        self.assertEqual(creator.CREATOR_FLOW.verify_tool, "creator_verify_otp")
        self.assertIsNone(creator.CREATOR_FLOW.phone_tool)
        self.assertEqual(creator.CONSUMER_FLOW.phone_tool, "consumer_phone_login")


if __name__ == "__main__":
    unittest.main()
