#!/usr/bin/env python3
"""New API Trae CN login bridge. Python 3.9+; standard library only.

Download only from your own dashboard and inspect before running:
    python -I trae-login-helper.py
This is not a background service and never reads browser cookies or IDE files.
Upstream credentials remain in memory and are sent only to the exact HTTPS
origin that issued this short-lived, one-use helper. No dashboard JWT is used.
The official SOLO callback accepts EXACT /authorize, not a state query;
therefore local confirmation AND verified-account dashboard consent are required.
"""

import base64
import hashlib
import http.server
import ipaddress
import json
import re
import secrets
import socket
import ssl
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
import webbrowser

CONFIG_BASE64 = "__TRAE_BRIDGE_CONFIG_BASE64__"
OFFICIAL_ORIGIN = "https://www.trae.cn"
CLIENT_ID = "ono9krqynydwx5"
UPLOAD_PATH = "/api/channel/self/oauth/trae/bridge/upload"
MAX_CALLBACK = 49152


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("Duplicate callback field")
        result[key] = value
    return result


def load_config(encoded):
    config = json.loads(base64.b64decode(encoded, validate=True), object_pairs_hook=unique_object)
    if set(config) != {"origin", "upload_url", "capability", "expires_at"}:
        raise ValueError("Invalid helper configuration")
    origin = config["origin"]
    parsed = urllib.parse.urlsplit(origin)
    if (parsed.scheme != "https" or not parsed.hostname or parsed.username is not None
            or parsed.password is not None or parsed.path or parsed.query or parsed.fragment
            or any(c.isspace() or ord(c) < 32 for c in origin)
            or origin != "https://" + parsed.netloc.lower()):
        raise ValueError("An exact HTTPS gateway origin is required")
    if parsed.port is not None and not 1 <= parsed.port <= 65535:
        raise ValueError("Invalid gateway port")
    # This one configured origin is the complete allowlist. No redirects, URL
    # overrides, environment proxies, or alternate upload destinations exist.
    if config["upload_url"] != origin + UPLOAD_PATH:
        raise ValueError("Upload URL does not match the issuing gateway")
    if not isinstance(config["capability"], str) or not 32 <= len(config["capability"]) <= 8192:
        raise ValueError("Invalid upload capability")
    if not isinstance(config["expires_at"], int) or not time.time() < config["expires_at"] <= time.time() + 601:
        raise ValueError("This helper expired; restart from the dashboard")
    return config


def callback_payload(target):
    if len(target) > MAX_CALLBACK or not target.startswith("/authorize?") or "#" in target:
        raise ValueError("Invalid callback target")
    parsed = urllib.parse.urlsplit(target)
    if parsed.scheme or parsed.netloc or parsed.path != "/authorize" or parsed.fragment:
        raise ValueError("Invalid callback URL")
    pairs = urllib.parse.parse_qsl(parsed.query, keep_blank_values=True, strict_parsing=True, max_num_fields=24)
    query = unique_object(pairs)
    if "error" in query or "userJwt" not in query or "userInfo" not in query:
        raise ValueError("Official login did not return an account")
    token = json.loads(query["userJwt"], object_pairs_hook=unique_object)
    account = json.loads(query["userInfo"], object_pairs_hook=unique_object)
    if not isinstance(token, dict) or not isinstance(account, dict):
        raise ValueError("Invalid account data")
    client = token.get("ClientID", token.get("clientId", CLIENT_ID))
    access = token.get("Token", token.get("token", ""))
    refresh = token.get("RefreshToken", token.get("refreshToken", ""))
    uid = account.get("UserID", account.get("userId", ""))
    if client != CLIENT_ID or not isinstance(access, str) or not access or not isinstance(refresh, str) or not refresh or not isinstance(uid, str) or not uid:
        raise ValueError("Invalid domestic account callback")
    for key in ("authHost", "auth_host"):
        if key in query and query[key].rstrip("/") != "https://api.trae.cn":
            raise ValueError("Non-domestic authentication host")
    for key in ("AuthHost", "authHost"):
        if key in account and account[key].rstrip("/") != "https://api.trae.cn":
            raise ValueError("Non-domestic authentication host")
    name = account.get("ScreenName", account.get("screenName", ""))
    return uid, name if isinstance(name, str) else ""


def safe_label(value):
    # Provider callback data is untrusted: never allow terminal escape/control
    # sequences, bidi spoofing, or credential URLs to appear in console output.
    return re.sub(r"[^a-zA-Z0-9 @._+\-\u4e00-\u9fff]", "?", value)[:128]


class LoginServer(http.server.HTTPServer):
    allow_reuse_address = False

    def __init__(self, deadline):
        # Exclusive bind on Windows prevents a second process reusing this port.
        super().__init__(("127.0.0.1", 0), CallbackHandler, bind_and_activate=False)
        if hasattr(socket, "SO_EXCLUSIVEADDRUSE"):
            self.socket.setsockopt(socket.SOL_SOCKET, socket.SO_EXCLUSIVEADDRUSE, 1)
        self.server_bind()
        self.server_activate()
        self.timeout = 1
        self.deadline = deadline
        self.authority = "127.0.0.1:" + str(self.server_port)
        self.callback = None
        self.account = None

    def get_request(self):
        connection, address = super().get_request()
        connection.settimeout(min(3, max(0.1, self.deadline - time.time())))
        return connection, address

    def handle_error(self, request, client_address):
        # BaseServer prints tracebacks with sensitive request state by default.
        pass


class CallbackHandler(http.server.BaseHTTPRequestHandler):
    server_version = "TraeLocalBridge"
    sys_version = ""

    def log_message(self, fmt, *args):
        # The loopback callback query contains credentials. Never log it.
        pass

    def allowed_request(self, preflight=False):
        if (self.client_address[0] != "127.0.0.1"
                or self.headers.get_all("Host", []) != [self.server.authority]
                or any(ord(c) < 33 or ord(c) > 126 for c in self.headers.get("Host", ""))
                or time.time() >= self.server.deadline or self.server.callback is not None
                or self.headers.get("Transfer-Encoding") is not None
                or self.headers.get("Content-Length", "0") != "0"):
            return False
        origins = self.headers.get_all("Origin", [])
        if origins:
            return origins == [OFFICIAL_ORIGIN]
        # An absent Origin is permitted ONLY for an actual top-level browser
        # navigation from the official HTTPS page, never an image/script fetch.
        return (not preflight and self.headers.get("Sec-Fetch-Mode") == "navigate"
                and self.headers.get("Sec-Fetch-Dest") == "document"
                and self.headers.get("Referer") in (OFFICIAL_ORIGIN, OFFICIAL_ORIGIN + "/")
                or not preflight and self.headers.get("Sec-Fetch-Mode") == "navigate"
                and self.headers.get("Sec-Fetch-Dest") == "document"
                and self.headers.get("Referer", "").startswith(OFFICIAL_ORIGIN + "/authorization"))

    def respond(self, status, body):
        encoded = body.encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Content-Length", str(len(encoded)))
        self.send_header("Cache-Control", "no-store")
        self.send_header("Pragma", "no-cache")
        self.send_header("Referrer-Policy", "no-referrer")
        self.send_header("X-Content-Type-Options", "nosniff")
        self.send_header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
        if self.headers.get_all("Origin", []) == [OFFICIAL_ORIGIN]:
            self.send_header("Access-Control-Allow-Origin", OFFICIAL_ORIGIN)
            self.send_header("Vary", "Origin")
            self.send_header("Access-Control-Allow-Methods", "GET, OPTIONS")
            self.send_header("Access-Control-Allow-Headers", "Content-Type")
            if self.headers.get("Access-Control-Request-Private-Network") == "true":
                self.send_header("Access-Control-Allow-Private-Network", "true")
        self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.write(encoded)
        self.close_connection = True

    def do_OPTIONS(self):
        requested = self.headers.get("Access-Control-Request-Headers", "").lower().split(",")
        if (not self.allowed_request(preflight=True) or self.path != "/authorize"
                or self.headers.get("Access-Control-Request-Method") != "GET"
                or any(value.strip() not in ("", "content-type") for value in requested)):
            self.respond(403, "Local login request rejected.")
            return
        self.respond(204, "")

    def do_GET(self):
        if not self.allowed_request():
            self.respond(403, "Local login request rejected.")
            return
        try:
            account = callback_payload(self.path)
        except (ValueError, TypeError, AttributeError, UnicodeError):
            self.respond(400, "Invalid domestic login callback. Restart from the dashboard.")
            return
        self.server.account = account
        self.server.callback = "http://" + self.server.authority + self.path
        self.respond(200, "Login received. Confirm the account in the helper console, then return to your original dashboard to approve the verified account. No account is bound yet.")

    def do_POST(self):
        self.respond(405, "Only the official browser callback is accepted.")


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise ValueError("Gateway redirects are not allowed")


def authorization_url(server, machine, device):
    # Do NOT add state or nonce: official SOLO validates the exact /authorize
    # callback. Correlation/consent is enforced locally and by the dashboard.
    query = urllib.parse.urlencode({
        "login_version": "1", "auth_from": "solo", "login_channel": "native_ide",
        "plugin_version": "3.3.65", "auth_type": "local", "client_id": CLIENT_ID,
        "redirect": "0", "login_trace_id": str(uuid.uuid4()),
        "auth_callback_url": "http://" + server.authority + "/authorize",
        "machine_id": machine, "device_id": device, "x_machine_id": machine,
        "x_device_id": device, "x_device_brand": "Trae", "x_device_type": "windows",
        "x_os_version": "Windows 10 Pro", "x_env": "", "x_app_version": "3.3.65",
        "x_app_type": "stable", "hide_saas_login": "false",
    })
    return OFFICIAL_ORIGIN + "/authorization?" + query


def upload_callback(config, callback, machine, device):
    if time.time() >= config["expires_at"]:
        raise ValueError("The login expired")
    body = json.dumps({"callbackUrl": callback, "machineId": machine, "deviceId": device}, separators=(",", ":")).encode()
    request = urllib.request.Request(config["upload_url"], data=body, method="POST", headers={
        "Content-Type": "application/json", "Authorization": "Bearer " + config["capability"],
        "User-Agent": "NewAPI-Trae-Local-Bridge/1",
    })
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect(), urllib.request.HTTPSHandler(context=ssl.create_default_context()))
    with opener.open(request, timeout=min(20, max(1, config["expires_at"] - time.time()))) as response:
        result = json.loads(response.read(8193), object_pairs_hook=unique_object)
        if response.status != 202 or result.get("success") is not True:
            raise ValueError("The dashboard rejected this helper session")


def main():
    server = None
    config = None
    try:
        config = load_config(CONFIG_BASE64)
        print("Trae CN local login helper")
        print("Issuing gateway: " + config["origin"])
        print("This opens only www.trae.cn and uploads only to this gateway.")
        print("No browser cookies, local IDE files, or manual tokens are read.")
        print("Token renewal may sign out another Trae client.")
        if input("Type LOGIN to open the official sign-in page, or close to cancel: ").strip() != "LOGIN":
            print("Cancelled. No account was bound.")
            return 1
        if time.time() >= config["expires_at"]:
            raise ValueError("Expired helper")
        machine = hashlib.sha256(secrets.token_bytes(32)).hexdigest()
        device = str(uuid.uuid4())
        server = LoginServer(config["expires_at"])
        if not webbrowser.open(authorization_url(server, machine, device), new=1, autoraise=True):
            raise ValueError("Could not open the browser")
        print("Complete the official browser login. Waiting for its local callback...")
        while server.callback is None and time.time() < server.deadline:
            server.handle_request()
        server.server_close()
        if server.callback is None:
            raise ValueError("Login timed out")
        uid, name = server.account
        print("Callback account (not yet provider-verified): " + safe_label(name) + " [" + safe_label(uid) + "]")
        print("Only continue if this is the Trae account you just selected.")
        if input("Type SEND to send this account for dashboard verification, or close to cancel: ").strip() != "SEND":
            print("Cancelled. No account was bound.")
            return 1
        upload_callback(config, server.callback, machine, device)
        print("Sent securely. Return to the ORIGINAL dashboard and confirm its provider-verified account. No account is bound until you approve there.")
        return 0
    except (EOFError, KeyboardInterrupt):
        print("Cancelled. Return to the dashboard to cancel this login.")
        return 1
    except Exception:
        # Network errors can include sensitive URLs; never print their text or
        # tracebacks. The dashboard has the non-secret error/status code.
        print("Login failed or expired. Return to the dashboard and restart. No automatic account binding occurred.")
        return 1
    finally:
        if server is not None:
            server.server_close()
            server.callback, server.account = None, None
        if config is not None:
            config.clear()


if __name__ == "__main__":
    sys.exit(main())
