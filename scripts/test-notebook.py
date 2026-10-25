#!/usr/bin/env python3
"""Run the built notebook process with local Telegram, AI and Memos fakes."""
import email.policy
from email.parser import BytesParser
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit


def main(proxy_mode="direct"):
    binary = Path(sys.argv[1] if len(sys.argv) > 1 else "build/memogram").resolve()
    sample = Path(__file__).resolve().parents[1] / "notebook.example.yaml"
    lock = threading.Lock()
    finished = threading.Event()
    user = {"id": 42, "is_bot": False, "first_name": "Local"}
    chat = {"id": 42, "type": "private"}
    def message(number, text):
        return {"message_id": number, "date": 1, "chat": chat, "from": user, "text": text}
    pending = [{"update_id": 1, "message": message(1, "original https://source.invalid")},
               {"update_id": 2, "message": message(2, "2")}]
    counts = {"ai": 0, "memos": 0, "confirm": 0}
    failures = []
    proxy_hosts = set()

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def respond(self, payload):
            content = json.dumps(payload).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(content)))
            self.end_headers()
            self.wfile.write(content)

        def do_GET(self):
            if self.path == "/api/instance/profile":
                self.respond({"version": "mock-main", "instanceUrl": "https://memos.invalid"})
            else:
                failures.append("unexpected GET " + self.path)
                self.send_error(404)

        def do_POST(self):
            destination = urlsplit(self.path)
            if destination.scheme:
                with lock:
                    proxy_hosts.add(destination.hostname)
                self.path = destination.path
            body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
            fields = {}
            if "multipart/form-data" in self.headers.get("Content-Type", ""):
                mime = BytesParser(policy=email.policy.default).parsebytes(
                    ("Content-Type: " + self.headers["Content-Type"] + "\r\n\r\n").encode() + body)
                fields = {p.get_param("name", header="content-disposition"): p.get_payload(decode=True).decode()
                          for p in mime.iter_parts()}
            method = self.path.rsplit("/", 1)[-1]
            if method == "getMe":
                self.respond({"ok": True, "result": {"id": 123, "is_bot": True, "first_name": "Mock", "username": "mock_bot"}})
            elif method in ("setMyCommands", "answerCallbackQuery"):
                self.respond({"ok": True, "result": True})
            elif method == "getUpdates":
                with lock:
                    batch = pending[:]
                    pending.clear()
                if not batch:
                    time.sleep(0.05)
                self.respond({"ok": True, "result": batch})
            elif method == "sendMessage":
                text = fields.get("text", "")
                if "草稿已更新" in text:
                    keyboard = json.loads(fields["reply_markup"])["inline_keyboard"]
                    data = next(b["callback_data"] for row in keyboard for b in row
                                if b["callback_data"].startswith("nb:save:"))
                    with lock:
                        counts["confirm"] += 1
                        for number in (3, 4):
                            pending.append({"update_id": number, "callback_query": {
                                "id": str(number), "from": user, "chat_instance": "local", "data": data,
                                "message": message(99, "preview")}})
                if "保存成功" in text:
                    if "https://memos.invalid/memos/local" not in text:
                        failures.append("wrong public memo link")
                    finished.set()
                self.respond({"ok": True, "result": message(99, text)})
            elif self.path == "/v1/responses":
                request = json.loads(body)
                with lock:
                    counts["ai"] += 1
                    if counts["memos"]:
                        failures.append("Memos write before draft confirmation")
                if request["model"] != "replace-with-your-model-id" or request["store"] is not False:
                    failures.append("wrong model/config wiring")
                self.respond({"id": "resp_local", "status": "completed", "output": [{"type": "message", "role": "assistant",
                    "content": [{"type": "output_text", "text": "Organized notebook\n\n#Go"}]}]})
            elif self.path == "/api/memos":
                memo = json.loads(body)
                with lock:
                    counts["memos"] += 1
                    if not counts["confirm"]:
                        failures.append("saved without confirmation")
                if "#Go" not in memo["content"] or "original" in memo["content"]:
                    failures.append("wrong saved draft")
                self.respond({"name": "memos/local", "content": memo["content"]})
            else:
                failures.append("unexpected POST " + self.path)
                self.send_error(404)

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    base = "http://127.0.0.1:" + str(server.server_port)
    try:
        with tempfile.TemporaryDirectory(prefix="memogram-notebook-") as tmp:
            config = Path(tmp) / "notebook.yaml"
            ai_base = base + "/v1" if proxy_mode == "direct" else "http://ai.invalid/v1"
            config_text = sample.read_text().replace("http://host.docker.internal:8317/v1", ai_base)
            if proxy_mode == "separate":
                config_text = config_text.replace("base_url: " + ai_base, "base_url: " + ai_base + "\n    proxy_url: " + base)
            config.write_text(config_text)
            shared_proxy = {"direct": "direct", "shared": base, "separate": "http://127.0.0.1:1"}[proxy_mode]
            telegram_base = base if proxy_mode == "direct" else "http://telegram.invalid"
            env = dict(os.environ, SERVER_ADDR=base, BOT_PROXY_ADDR=telegram_base, BOT_TOKEN="123:local-test",
                       PROXY_URL=shared_proxy, TELEGRAM_PROXY_URL=base if proxy_mode == "separate" else "",
                       ALLOWED_USERNAMES="", ALLOWED_USER_IDS="", TELEGRAM_USER_ID="42", MEMOS_TOKEN="local-test", CLIPROXY_API_KEY="local-test",
                       DATA=str(Path(tmp) / "bindings.txt"), NOTEBOOK_CONFIG=str(config), WEB_ADDR="127.0.0.1:0", WEB_URL="")
            process = subprocess.Popen([str(binary)], cwd=tmp, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
            try:
                if not finished.wait(10):
                    failures.append("process did not finish local save in 10s")
            finally:
                process.send_signal(signal.SIGTERM)
                try:
                    output, _ = process.communicate(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
                    output, _ = process.communicate()
                    failures.append("process did not stop on SIGTERM")
            if counts != {"ai": 1, "memos": 1, "confirm": 1} or process.returncode != 0:
                failures.append("unexpected counts/exit: " + str(counts) + "/" + str(process.returncode))
            expected_hosts = set() if proxy_mode == "direct" else {"telegram.invalid", "ai.invalid"}
            if proxy_hosts != expected_hosts:
                failures.append("unexpected proxy destinations: " + str(proxy_hosts))
            if failures:
                raise SystemExit("\n".join(failures) + "\n" + output)
            print("PASS [" + proxy_mode + "]: real process startup -> local Telegram collection -> SDK draft -> duplicate confirmations -> one Memos save -> SIGTERM")
            print("All services were local fakes; this is not real-service or Docker validation.")
    finally:
        server.shutdown()
        server.server_close()


if __name__ == "__main__":
    for proxy_mode in ("direct", "shared", "separate"):
        main(proxy_mode)
