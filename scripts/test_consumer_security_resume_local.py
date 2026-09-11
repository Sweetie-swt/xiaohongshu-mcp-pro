import importlib.util
import pathlib
import sys
import unittest
from unittest import mock


SCRIPT_PATH = pathlib.Path(__file__).with_name("consumer_security_resume_local.py")
SPEC = importlib.util.spec_from_file_location("consumer_security_resume_local", SCRIPT_PATH)
assert SPEC and SPEC.loader
helper = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = helper
SPEC.loader.exec_module(helper)


class ConsumerSecurityResumeLocalTests(unittest.TestCase):
    def test_resume_initializes_and_calls_only_complete_once(self) -> None:
        calls: list[str] = []
        success = {
            "result": {
                "isError": False,
                "content": [{"type": "text", "text": "consumer 登录成功，www session 已通过正向页面验收并保存。"}],
            }
        }

        with (
            mock.patch.object(helper, "initialize", side_effect=lambda flow: calls.append("initialize") or "session"),
            mock.patch.object(
                helper,
                "call_complete_security_verification",
                side_effect=lambda session_id, flow, timeout_seconds: calls.append("complete") or success,
            ),
            mock.patch.object(helper, "print_tool_result"),
            mock.patch.object(helper, "close_session", side_effect=lambda session_id, client: calls.append("close")),
            mock.patch.object(helper.sys, "argv", [str(SCRIPT_PATH)]),
        ):
            self.assertEqual(helper.run_resume_flow(), 0)

        self.assertEqual(calls, ["initialize", "complete", "close"])

    def test_resume_does_not_call_login_or_verify_tools(self) -> None:
        source = SCRIPT_PATH.read_text(encoding="utf-8")
        self.assertNotIn("call_phone_login", source)
        self.assertNotIn("call_verify_otp", source)
        self.assertEqual(source.count("call_complete_security_verification("), 1)


if __name__ == "__main__":
    unittest.main()
