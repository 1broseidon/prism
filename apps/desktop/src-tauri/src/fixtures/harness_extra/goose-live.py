"""Optional Goose 1.49 native-loop probe; invoked by the harness_extra ignored test.

Arguments: downloaded Goose executable, isolated root prepared by edits(), original
fixture hook URL. Only local HTTP endpoints are used. Output is an evidence summary,
never the model messages or client configuration. The child environment is explicit.
"""

import json
import pathlib
import subprocess
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

binary = pathlib.Path(sys.argv[1]).resolve(strict=True)
home = pathlib.Path(sys.argv[2]).resolve(strict=True)
folder = str(home)
old_hook = sys.argv[3]
work = home / "work"
work.mkdir()
observed = []
requests = []
schemas = []
tool_results = []


def structural_schema(value):
    if isinstance(value, dict):
        return {
            k: structural_schema(v)
            for k, v in value.items()
            if k not in ("description", "title", "default", "examples")
        }
    if isinstance(value, list):
        return [structural_schema(v) for v in value]
    return value


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_GET(self):
        body = json.dumps(
            {
                "object": "list",
                "data": [{"id": "fixture", "object": "model", "owned_by": "fixture"}],
            }
        ).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        if self.path.startswith("/hooks/"):
            observed.append(body)
            payload = b"{}"
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)
            return
        requests.append(self.path)
        results = [m for m in body.get("messages", []) if m.get("role") == "tool"]
        tool_results.extend(results)
        tools = body.get("tools", [])
        names = [t["function"]["name"] for t in tools if "function" in t]
        shell = next((n for n in names if n.endswith("__shell") or n == "shell"), None)
        if not schemas:
            schemas.extend([t["function"] for t in tools if "function" in t])
        delta = {"role": "assistant", "content": "Fixture complete."}
        finish = "stop"
        if shell and not results:
            delta = {
                "role": "assistant",
                "tool_calls": [
                    {
                        "index": 0,
                        "id": "fixture-shell-call",
                        "type": "function",
                        "function": {
                            "name": shell,
                            "arguments": json.dumps(
                                {
                                    "command": "printf PRISM_GOOSE_OBSERVER_OK",
                                    "description": "Print fixture marker",
                                }
                            ),
                        },
                    }
                ],
            }
            finish = "tool_calls"
        if body.get("stream"):
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.end_headers()
            for d, f in [(delta, None), ({}, finish)]:
                chunk = {
                    "id": "fixture",
                    "object": "chat.completion.chunk",
                    "created": int(time.time()),
                    "model": "fixture",
                    "choices": [{"index": 0, "delta": d, "finish_reason": f}],
                    "usage": {
                        "prompt_tokens": 1,
                        "completion_tokens": 1,
                        "total_tokens": 2,
                    },
                }
                self.wfile.write(("data: " + json.dumps(chunk) + "\n\n").encode())
            self.wfile.write(b"data: [DONE]\n\n")
        else:
            for call in delta.get("tool_calls", []):
                call.pop("index", None)
            payload = json.dumps(
                {
                    "id": "fixture",
                    "object": "chat.completion",
                    "model": "fixture",
                    "choices": [
                        {"index": 0, "message": delta, "finish_reason": finish}
                    ],
                    "usage": {
                        "prompt_tokens": 1,
                        "completion_tokens": 1,
                        "total_tokens": 2,
                    },
                }
            ).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)


server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
threading.Thread(target=server.serve_forever, daemon=True).start()
origin = "http://127.0.0.1:" + str(server.server_port)
helper = home / ".agents/plugins/prism-goose/scripts/observe.sh"
source = helper.read_text()
assert source.count(old_hook) == 1, "Expected generated observer endpoint"
helper.write_text(source.replace(old_hook, origin + "/hooks/goose/fixture_token"))
env = {
    "PATH": "/usr/local/bin:/usr/bin:/bin",
    "HOME": folder,
    "GOOSE_PATH_ROOT": folder,
    "USERPROFILE": folder,
    "PWD": str(work),
    "XDG_CONFIG_HOME": str(home / ".config"),
    "XDG_DATA_HOME": str(home / ".data"),
    "XDG_CACHE_HOME": str(home / ".cache"),
    "XDG_STATE_HOME": str(home / ".state"),
    "GOOSE_TELEMETRY_ENABLED": "false",
    "GOOSE_DISABLE_KEYRING": "1",
    "GOOSE_PROVIDER": "openai",
    "GOOSE_MODEL": "fixture",
    "GOOSE_MODE": "auto",
    "OPENAI_HOST": origin,
    "OPENAI_BASE_URL": origin + "/v1",
    "OPENAI_BASE_PATH": "v1/chat/completions",
    "OPENAI_API_KEY": "fixture",
    "OPENAI_TIMEOUT": "10",
    "NO_PROXY": "*",
}
try:
    version = subprocess.run(
        [str(binary), "--version"],
        cwd=work,
        env=env,
        capture_output=True,
        text=True,
        timeout=5,
    )
    assert version.returncode == 0 and "1.49.0" in version.stdout, (
        "This fixture targets Goose 1.49.0"
    )
    result = subprocess.run(
        [
            str(binary),
            "run",
            "--text",
            "Run the fixture shell call, then finish.",
            "--max-turns",
            "3",
            "--output-format",
            "json",
        ],
        cwd=work,
        env=env,
        capture_output=True,
        text=True,
        timeout=35,
    )
    print(
        json.dumps(
            {
                "exit": result.returncode,
                "requests": requests,
                "tools": [s["name"] for s in schemas],
                "observed": [
                    {
                        "event": e.get("event"),
                        "tool_name": e.get("tool_name"),
                        "decision": e.get("decision"),
                        "policy_evaluated": e.get("policy_evaluated"),
                        "session_present": bool(e.get("session_id")),
                        "call_present": bool(e.get("tool_call_id")),
                        "cwd_correct": e.get("working_dir") == str(work),
                        "input_correct": e.get("tool_input", {}).get("command")
                        == "printf PRISM_GOOSE_OBSERVER_OK",
                    }
                    for e in observed
                ],
                "marker_in_result": any(
                    "PRISM_GOOSE_OBSERVER_OK" in json.dumps(r) for r in tool_results
                ),
                "native_fixture": (
                    {
                        **observed[0],
                        "session_id": "goose-fixture-session",
                        "working_dir": "/fixture/work",
                    }
                    if observed
                    else None
                ),
                "tool_catalog": [
                    {
                        "name": s["name"],
                        "parameters": structural_schema(s.get("parameters", {})),
                    }
                    for s in schemas
                ],
            }
        )
    )
    if result.returncode or not observed:
        print(
            "Fixture failed to complete the native loop; client output retained only in memory."
        )
    assert result.returncode == 0
    assert len(observed) == 1, "expected one actual Goose observation"
    e = observed[0]
    assert (
        e["event"] == "PreToolUseResult"
        and e["tool_name"] in ("shell", "developer__shell")
        and e["decision"] == "allow"
    )
    assert e["session_id"] and e["tool_call_id"] and e["working_dir"] == str(work)
    assert any("PRISM_GOOSE_OBSERVER_OK" in json.dumps(r) for r in tool_results)
finally:
    server.shutdown()
    server.server_close()
