import base64
import importlib.util
import pathlib
import sys
import unittest


SCRIPT_PATH = pathlib.Path(__file__).with_name("creator_verify_otp_local.py")
SPEC = importlib.util.spec_from_file_location("creator_verify_otp_local", SCRIPT_PATH)
assert SPEC and SPEC.loader
helper = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = helper
SPEC.loader.exec_module(helper)


class CreatorVerifyOTPLocalTests(unittest.TestCase):
    def test_otp_validation_is_local_only(self) -> None:
        self.assertTrue(helper.is_valid_otp("111111"))
        self.assertFalse(helper.is_valid_otp("12345"))
        self.assertFalse(helper.is_valid_otp("1234567"))
        self.assertFalse(helper.is_valid_otp("１２３４５６"))

    def test_security_verification_marker_and_image_are_detected(self) -> None:
        message = {
            "result": {
                "isError": False,
                "content": [
                    {
                        "type": "text",
                        "text": "security_verification_required：需要扫码完成安全验证",
                    },
                    {
                        "type": "image",
                        "mimeType": "image/png",
                        "data": base64.b64encode(b"png").decode("ascii"),
                    },
                ],
            }
        }
        is_error, texts, images = helper.tool_result_parts(message)
        self.assertFalse(is_error)
        self.assertTrue(helper.is_security_verification_required(message))
        self.assertEqual(texts, ["security_verification_required：需要扫码完成安全验证"])
        self.assertEqual(images[0][0], "image/png")

    def test_direct_success_and_error_are_distinguished(self) -> None:
        success = {
            "result": {
                "isError": False,
                "content": [{"type": "text", "text": "creator 登录成功，cookies 已保存。"}],
            }
        }
        failure = {
            "result": {
                "isError": True,
                "content": [{"type": "text", "text": "安全验证登录失败"}],
            }
        }
        self.assertTrue(helper.is_direct_success(success))
        self.assertFalse(helper.is_direct_success(failure))

    def test_image_materialization_uses_temp_file_without_invoking_network(self) -> None:
        opened: list[str] = []
        original_startfile = getattr(helper.os, "startfile", None)
        helper.os.startfile = opened.append  # type: ignore[attr-defined]
        path = None
        try:
            encoded = base64.b64encode(b"png").decode("ascii")
            path = helper.write_and_open_security_image([("image/png", encoded)])
            self.assertIsNotNone(path)
            self.assertEqual(opened, [path])
            with open(path, "rb") as image_file:
                self.assertEqual(image_file.read(), b"png")
        finally:
            helper.best_effort_remove_temp_image(path)
            if original_startfile is None:
                delattr(helper.os, "startfile")
            else:
                helper.os.startfile = original_startfile  # type: ignore[attr-defined]


if __name__ == "__main__":
    unittest.main()
