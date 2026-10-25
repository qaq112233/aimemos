"""Regression checks for the Telegram mock used by the container smoke test."""
import http.server
import importlib.util
import json
from pathlib import Path
import socket
import struct
import threading
import unittest
import urllib.request


spec = importlib.util.spec_from_file_location("container_smoke", Path(__file__).with_name("test-container.py"))
smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(smoke)


class PollCondition(threading.Condition):
    def __init__(self):
        super().__init__()
        self.waiting = threading.Event()

    def wait(self, timeout=None):
        self.waiting.set()
        return super().wait(timeout)


class TelegramMockTest(unittest.TestCase):
    def setUp(self):
        finished = threading.Event()

        class Handler(smoke.MockAPI):
            updates = []
            replies = []
            condition = PollCondition()

            def respond(self, body):
                try:
                    super().respond(body)
                finally:
                    finished.set()

        self.handler = Handler
        self.finished = finished
        self.errors = []
        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.server.handle_error = lambda *args: self.errors.append("mock request failed")
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()

    def request(self, offset):
        body = (
            "--poll\r\nContent-Disposition: form-data; name=\"offset\"\r\n\r\n"
            f"{offset}\r\n--poll--\r\n"
        ).encode()
        return urllib.request.Request(
            f"http://127.0.0.1:{self.server.server_port}/bot123:test/getUpdates",
            data=body, headers={"Content-Type": "multipart/form-data; boundary=poll"},
        )

    def poll(self, offset):
        with urllib.request.urlopen(self.request(offset), timeout=3) as response:
            return json.load(response)["result"]

    def test_updates_are_replayed_until_acknowledged_by_offset(self):
        first, second = {"update_id": 1}, {"update_id": 2}
        with self.handler.condition:
            self.handler.updates[:] = [first, second]
        self.assertEqual(self.poll(1), [first, second])
        self.assertEqual(self.poll(1), [first, second])
        self.assertEqual(self.poll(2), [second])
        self.assertEqual(self.poll(3), [])

    def test_disconnected_old_poll_does_not_consume_new_container_update(self):
        request = self.request(1)
        connection = socket.create_connection(self.server.server_address, timeout=3)
        try:
            connection.sendall((
                f"POST {request.selector} HTTP/1.0\r\n"
                f"Content-Type: {request.get_header('Content-type')}\r\n"
                f"Content-Length: {len(request.data)}\r\n\r\n"
            ).encode() + request.data)
            self.assertTrue(self.handler.condition.waiting.wait(2), "old poll did not start")
            # A container stopping cancels its in-flight long poll. Reset the
            # connection before adding the next container's update.
            connection.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, struct.pack("ii", 1, 0))
        finally:
            connection.close()
        update = {"update_id": 2}
        with self.handler.condition:
            self.handler.updates[:] = [update]
            self.handler.condition.notify_all()
        self.assertTrue(self.finished.wait(2), "old poll did not finish")
        self.assertEqual(self.poll(1), [update])
        self.assertEqual(self.errors, [])


if __name__ == "__main__":
    unittest.main()
