"""国内版 Qoder 的单账号转换程序，直接实现授权、签名与流式协议。"""

import argparse
import base64
import contextlib
import datetime
import hashlib
import hmac
import json
import os
from pathlib import Path
import secrets
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

try:
    import fcntl
except ImportError:
    fcntl = None

from cryptography.hazmat.primitives import padding, serialization
from cryptography.hazmat.primitives.asymmetric import padding as rsa_padding
from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes


WEBSITE = 'https://qoder.com.cn'
OPENAPI = 'https://openapi.qoder.com.cn'
GATEWAY = 'https://gateway.qoder.com.cn'
CLIENT_ID = 'e883ade2-e6e3-4d6d-adf7-f92ceff5fdcb'
CLIENT_VERSION = '1.0.10'
CHAT_PATH = '/algo/api/v2/service/pro/sse/agent_chat_generation'
ALPHABET = '_doRTgHZBKcGVjlvpC,@aFSx#DPuNJme&i*MzLOEn)sUrthbf%Y^w.(kIQyXqWA!'
STANDARD = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/'
PUBLIC_KEY = b'''-----BEGIN PUBLIC KEY-----
MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDA8iMH5c02LilrsERw9t6Pv5Nc
4k6Pz1EaDicBMpdpxKduSZu5OANqUq8er4GM95omAGIOPOh+Nx0spthYA2BqGz+l
6HRkPJ7S236FZz73In/KVuLnwI8JJ2CbuJap8kvheCCZpmAWpb/cPx/3Vr/J6I17
XcW+ML9FoCI6AOvOzwIDAQAB
-----END PUBLIC KEY-----'''


def json_bytes(value):
    return json.dumps(value, ensure_ascii=False, separators=(',', ':')).encode('utf-8')


def b64url(value):
    return base64.urlsafe_b64encode(value).decode().rstrip('=')


def encode_body(value):
    original = base64.b64encode(json_bytes(value)).decode()
    size = len(original) // 3
    rotated = original[-size:] + original[size:-size] + original[:size] if size else original
    return rotated.translate(str.maketrans(STANDARD + '=', ALPHABET + '$')).encode('ascii')


def decode_body(value):
    mapped = value.decode().translate(str.maketrans(ALPHABET + '$', STANDARD + '='))
    size = len(mapped) // 3
    restored = mapped[-size:] + mapped[size:-size] + mapped[:size] if size else mapped
    return json.loads(base64.b64decode(restored))


class ProtocolError(Exception):
    def __init__(self, message, status=502):
        super().__init__(message)
        self.status = status


def open_request(url, payload=None, headers=None, timeout=180):
    request = urllib.request.Request(url, data=payload, headers=headers or {})
    try:
        return urllib.request.urlopen(request, timeout=timeout)
    except urllib.error.HTTPError as error:
        error.close()
        raise ProtocolError(f'上游接口返回 HTTP {error.code}', error.code) from None


def http_json(url, body=None, token=None):
    headers = {'Content-Type': 'application/json', 'User-Agent': 'QoderProtocolBridge/1.0'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    with open_request(url, None if body is None else json_bytes(body), headers, 30) as response:
        return json.load(response)


class Store:
    def __init__(self, directory):
        self.directory = Path(directory)
        self.directory.mkdir(mode=0o700, parents=True, exist_ok=True)
        self.path = self.directory / 'state.json'
        self.lock = threading.RLock()
        if not self.path.exists():
            self.update(lambda state: state.update({'api_key': secrets.token_urlsafe(40),
                'machine_id': uuid.uuid4().hex,
                'machine_type': hashlib.md5(b'linux-x86_64-qoder-bridge').hexdigest()[:18],
                'machine_token': secrets.token_urlsafe(32)}) if not state else None)

    def load(self):
        with self.lock:
            return json.loads(self.path.read_text(encoding='utf-8'))

    def save(self, state):
        with self.lock:
            temporary = self.directory / ('state.' + secrets.token_hex(6) + '.tmp')
            descriptor = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(descriptor, 'w', encoding='utf-8') as output:
                json.dump(state, output, ensure_ascii=False, indent=2)
            os.replace(temporary, self.path)

    def update(self, operation):
        # Linux 文件锁覆盖整次读改写，避免服务与授权命令覆盖彼此的凭证。
        with self.lock:
            descriptor = os.open(self.directory / '.state.lock', os.O_RDWR | os.O_CREAT, 0o600)
            with os.fdopen(descriptor, 'a+b') as guard:
                if fcntl is not None:
                    fcntl.flock(guard, fcntl.LOCK_EX)
                try:
                    state = self.load() if self.path.exists() else {}
                    result = operation(state)
                    self.save(state)
                    return result
                finally:
                    if fcntl is not None:
                        fcntl.flock(guard, fcntl.LOCK_UN)


def token_expiry(data):
    if data.get('expires_at'):
        try:
            return datetime.datetime.fromisoformat(data['expires_at'].replace('Z', '+00:00')).timestamp()
        except (ValueError, TypeError):
            pass
    if data.get('expires_in'):
        return time.time() + float(data['expires_in']) / 1000
    return 0


def begin_login(store):
    verifier = b64url(secrets.token_bytes(32))
    pending = {'verifier': verifier, 'nonce': uuid.uuid4().hex, 'started_at': time.time()}
    store.update(lambda state: state.update({'pending': pending}))
    query = urllib.parse.urlencode({'nonce': pending['nonce'], 'client_id': CLIENT_ID,
        'challenge': b64url(hashlib.sha256(verifier.encode()).digest()), 'challenge_method': 'S256'})
    return WEBSITE + '/device/selectAccounts?' + query


def finish_login(store):
    state = store.load()
    pending = state.get('pending')
    if not pending or time.time() - pending['started_at'] > 600:
        raise ProtocolError('授权会话已过期，请重新发起登录。', 401)
    query = urllib.parse.urlencode({'nonce': pending['nonce'], 'verifier': pending['verifier'],
                                   'challenge_method': 'S256'})
    while time.time() - pending['started_at'] < 600:
        try:
            result = http_json(OPENAPI + '/api/v1/deviceToken/poll?' + query)
        except ProtocolError as error:
            if error.status == 404:
                time.sleep(2)
                continue
            raise
        token = result.get('token')
        if not token:
            raise ProtocolError('授权接口没有返回访问令牌。', 401)
        user = http_json(OPENAPI + '/api/v1/userinfo', token=token)
        def commit(state):
            if state.get('pending', {}).get('nonce') != pending['nonce']:
                raise ProtocolError('授权会话已被更新，请使用最新的授权页面。', 409)
            state.update({'access_token': token, 'refresh_token': result.get('refresh_token', ''),
                          'expires_at': token_expiry(result), 'user': user})
            state.pop('pending', None)
        store.update(commit)
        return {'已授权': True, '凭证响应字段': list(result), '用户信息字段': list(user)}
    raise ProtocolError('等待授权超时。', 401)


def signed_headers(state, path, body=b''):
    user = state.get('user', {})
    uid = str(user.get('id') or user.get('userId') or user.get('uid') or '')
    token = state.get('access_token')
    if not token or not uid:
        raise ProtocolError('请先完成服务器上的 Qoder 授权。', 401)
    identity = {'uid': uid, 'aid': uid, 'name': str(user.get('name', '')), 'yx_uid': '',
        'organization_id': str(user.get('organization_id') or user.get('organizationId') or ''),
        'organization_name': str(user.get('organization_name') or user.get('organizationName') or ''),
        'user_type': str(user.get('userType') or 'personal_standard'),
        'security_oauth_token': token, 'refresh_token': state.get('refresh_token', '')}
    session_key = secrets.token_hex(8).encode('ascii')
    rsa_key = serialization.load_pem_public_key(PUBLIC_KEY)
    cosy_key = base64.b64encode(rsa_key.encrypt(session_key, rsa_padding.PKCS1v15())).decode()
    padder = padding.PKCS7(128).padder()
    padded = padder.update(json_bytes(identity)) + padder.finalize()
    encryptor = Cipher(algorithms.AES(session_key), modes.CBC(session_key)).encryptor()
    info = base64.b64encode(encryptor.update(padded) + encryptor.finalize()).decode()
    payload = base64.b64encode(json_bytes({'version': 'v1', 'cosyVersion': CLIENT_VERSION,
        'ideVersion': '', 'requestId': str(uuid.uuid4()), 'info': info})).decode()
    date = str(int(time.time()))
    signature_path = path[5:] if path.startswith('/algo/') else path
    signed = '\n'.join([payload, cosy_key, date, body.decode('ascii'), signature_path])
    signature = hashlib.md5(signed.encode('utf-8')).hexdigest()
    return {'Authorization': 'Bearer COSY.' + payload + '.' + signature,
        'Content-Type': 'application/json', 'Accept': 'text/event-stream',
        'cosy-key': cosy_key, 'cosy-date': date, 'cosy-user': uid,
        'cosy-version': CLIENT_VERSION, 'cosy-clienttype': '5', 'cosy-data-policy': 'agree',
        'cosy-machineid': state['machine_id'], 'cosy-machinetype': state['machine_type'],
        'cosy-machinetoken': state['machine_token'], 'login-version': 'v2',
        'cosy-scene': 'assistant', 'cosy-business-product': 'ide', 'cosy-business-type': 'agent',
        'User-Agent': 'QoderProtocolBridge/1.0'}


def build_body(request, state):
    messages = request.get('messages')
    if not isinstance(messages, list) or not messages:
        raise ProtocolError('messages 必须是非空列表。', 400)
    converted = []
    for message in messages:
        if not isinstance(message, dict) or message.get('role') not in {'system', 'developer', 'user', 'assistant', 'tool'}:
            raise ProtocolError('消息角色不受支持。', 400)
        item = dict(message)
        if item['role'] == 'developer':
            item['role'] = 'system'
        converted.append(item)
    model = request.get('model') or 'auto'
    parameters = {'max_tokens': request.get('max_tokens', request.get('max_completion_tokens', 4096))}
    for name in ['temperature', 'top_p', 'reasoning_effort']:
        if name in request:
            parameters[name] = request[name]
    # 纯文本请求已经实测可只使用这四个顶层字段。
    body = {'stream': True, 'model_config': {'key': model, 'source': 'system', 'format': 'openai'},
            'messages': converted, 'parameters': parameters}
    for name in ['tools', 'tool_choice']:
        if name in request:
            body[name] = request[name]
    return body


def prepare_request(request):
    if request.get('n', 1) not in (None, 1):
        raise ProtocolError('当前只支持 n=1。', 400)
    if request.get('seed') is not None:
        raise ProtocolError('尚未验证上游支持参数：seed', 400)
    for name in ['stop', 'frequency_penalty', 'presence_penalty', 'logit_bias', 'logprobs', 'top_logprobs']:
        if request.get(name) not in (None, 0, False, {}, []):
            raise ProtocolError('尚未验证上游支持参数：' + name, 400)
    if request.get('response_format') not in (None, {'type': 'text'}):
        raise ProtocolError('尚未验证结构化输出约束，当前仅支持普通文本输出。', 400)
    if 'parallel_tool_calls' in request and not isinstance(request['parallel_tool_calls'], bool):
        raise ProtocolError('parallel_tool_calls 必须是布尔值。', 400)
    messages = request.get('messages')
    if not isinstance(messages, list) or not messages:
        raise ProtocolError('messages 必须是非空列表。', 400)
    result = dict(request, messages=list(messages))
    tools = request.get('tools') or []
    if not isinstance(tools, list) or any(not isinstance(t, dict) or t.get('type') != 'function' or
        not isinstance(t.get('function'), dict) or not t['function'].get('name') for t in tools):
        raise ProtocolError('当前只支持标准 function 工具定义。', 400)
    choice = request.get('tool_choice') or 'auto'
    instruction = ''
    if choice == 'none':
        result.pop('tools', None)
        instruction = '本次回答禁止发起工具调用，请根据已有信息直接回答。'
    elif choice == 'required':
        if not tools:
            raise ProtocolError('要求工具调用时必须提供 tools。', 400)
        instruction = '本次回答必须调用已提供的工具，不能用普通文本代替工具调用。'
    elif isinstance(choice, dict):
        name = choice.get('function', {}).get('name')
        selected = [tool for tool in tools if tool['function']['name'] == name]
        if choice.get('type') != 'function' or len(selected) != 1:
            raise ProtocolError('指定的工具名称没有唯一匹配的定义。', 400)
        result['tools'] = selected
        instruction = '本次回答必须调用工具 ' + name + '，不能用普通文本代替工具调用。'
    elif choice != 'auto':
        raise ProtocolError('tool_choice 取值不受支持。', 400)
    if request.get('parallel_tool_calls') is False:
        instruction += '本次回答最多发起一个工具调用；其他工具应在收到结果后再决定是否调用。'
    if instruction:
        # 上游实测忽略原生工具控制，转换层增加约束，并在回传前严格校验。
        existing, remaining = [], []
        for message in result['messages']:
            if message.get('role') in ('system', 'developer'):
                content = message.get('content', '')
                if isinstance(content, list) and all(isinstance(part, dict) and part.get('type') == 'text' for part in content):
                    content = '\n'.join(part.get('text', '') for part in content)
                if not isinstance(content, str):
                    raise ProtocolError('工具约束需要纯文本系统消息。', 400)
                existing.append(content)
            else:
                remaining.append(message)
        result['messages'] = [{'role': 'system', 'content': '\n\n'.join(existing + [instruction])}] + remaining
    return result


def checked_chunks(chunks, request):
    choice = request.get('tool_choice') or 'auto'
    constrained = choice != 'auto' or request.get('parallel_tool_calls') is False
    if not constrained:
        yield from chunks
        return
    buffered = list(chunks)
    result = collect(iter(buffered), request.get('model') or 'auto')
    calls = result['choices'][0]['message'].get('tool_calls', [])
    if choice == 'none' and calls:
        raise ProtocolError('模型没有遵守禁止工具调用的要求。')
    if (choice == 'required' or isinstance(choice, dict)) and not calls:
        raise ProtocolError('模型没有满足强制工具调用要求。')
    if isinstance(choice, dict) and any(call['function']['name'] != choice['function']['name'] for call in calls):
        raise ProtocolError('模型调用了未指定的工具。')
    if request.get('parallel_tool_calls') is False and len(calls) > 1:
        raise ProtocolError('模型未遵守禁止并行工具调用的要求。')
    yield from buffered


def unwrap_frame(payload):
    if payload.strip() == '[DONE]':
        return None
    envelope = json.loads(payload)
    status = int(envelope.get('statusCodeValue', 200))
    if status != 200:
        raise ProtocolError(f'上游流内返回错误状态 {status}', status)
    inner = envelope.get('body', envelope)
    if isinstance(inner, str):
        if inner.strip() == '[DONE]':
            return None
        inner = json.loads(inner)
    if not isinstance(inner, dict):
        raise ProtocolError('上游返回了无法识别的流内容。')
    if inner.get('error') or (inner.get('code') not in (None, '', 0, '0')):
        code = str(inner.get('code', '未知'))
        expired = code in {'12153', 'TOKEN_EXPIRE'} or 'TOKEN_EXPIRE' in str(inner.get('message', ''))
        raise ProtocolError('上游返回业务错误，代码：' + code, 401 if expired else 502)
    return inner


class Client:
    def __init__(self, store):
        self.store = store
        self.refresh_lock = threading.Lock()
        self.catalog_cache = None
        self.catalog_time = 0

    def state(self, force_refresh=False):
        with self.refresh_lock:
            state = self.store.load()
            expires = state.get('expires_at', 0)
            if state.get('refresh_token') and (force_refresh or (expires and expires < time.time() + 3600)):
                old_token = state.get('access_token')
                def refresh(latest):
                    if latest.get('access_token') != old_token:
                        return dict(latest)
                    result = http_json(OPENAPI + '/api/v1/deviceToken/refresh',
                                       {'refresh_token': latest['refresh_token']})
                    # 实测授权轮询返回 token，续期接口返回 device_token。
                    renewed_token = result.get('device_token') or result.get('token')
                    if not renewed_token:
                        raise ProtocolError('续期接口没有返回访问令牌，请重新授权。', 401)
                    latest.update({'access_token': renewed_token,
                        'refresh_token': result.get('refresh_token', latest['refresh_token']),
                        'expires_at': token_expiry(result)})
                    return dict(latest)
                state = self.store.update(refresh)
            return state

    def catalog(self):
        if self.catalog_cache is not None and time.time() - self.catalog_time < 300:
            return self.catalog_cache
        path = '/algo/api/v2/model/list'
        for attempt in range(2):
            state = self.state(force_refresh=attempt > 0)
            try:
                with open_request(GATEWAY + path + '?Encode=1', headers=signed_headers(state, path), timeout=30) as response:
                    raw = json.load(response)
                break
            except ProtocolError as error:
                if error.status != 401 or attempt:
                    raise
        models = []
        for category in ['chat', 'assistant', 'developer']:
            if isinstance(raw.get(category), list):
                models = [entry for entry in raw[category] if entry.get('key')]
                if models:
                    break
        if not models:
            raise ProtocolError('上游没有返回可用模型清单。')
        self.catalog_cache, self.catalog_time = models, time.time()
        return models

    def resolve_model(self, value):
        normalized = str(value).strip().casefold().removeprefix('qoder/').replace('_', '-')
        for entry in self.catalog():
            aliases = [entry['key'], entry.get('display_name', '')]
            if normalized in [str(alias).strip().casefold().replace('_', '-') for alias in aliases]:
                if not entry.get('enable'):
                    raise ProtocolError('该模型当前未对账号开放。', 403)
                return entry
        raise ProtocolError('模型不可用，请使用 /v1/models 返回的名称或原生标识。', 400)

    def stream(self, request):
        request = prepare_request(request)
        entry = self.resolve_model(request.get('model') or 'auto')
        request = dict(request, model=entry['key'])
        for attempt in range(2):
            state = self.state(force_refresh=attempt > 0)
            emitted = False
            try:
                upstream = self.stream_once(request, state)
                for chunk in checked_chunks(upstream, request):
                    emitted = True
                    yield chunk
                return
            except ProtocolError as error:
                if error.status != 401 or attempt or emitted:
                    raise
            finally:
                upstream.close()

    def stream_once(self, request, state):
        body = encode_body(build_body(request, state))
        headers = signed_headers(state, CHAT_PATH, body)
        headers.update({'x-model-key': request['model'], 'x-model-source': 'system'})
        query = '?FetchKeys=llm_model_result&AgentId=agent_common&Encode=1'
        response = open_request(GATEWAY + CHAT_PATH + query, body, headers)
        with response:
            parts, terminal, seen = [], False, False
            for line in response:
                if len(line) > 2 * 1024 * 1024:
                    raise ProtocolError('上游流帧超过大小限制。')
                line = line.decode('utf-8').rstrip('\r\n')
                if line.startswith('data:'):
                    parts.append(line[5:].lstrip())
                elif not line and parts:
                    chunk = unwrap_frame('\n'.join(parts))
                    parts = []
                    if chunk is None:
                        terminal = True
                        break
                    if chunk.get('choices') or chunk.get('usage'):
                        seen = True
                        if any(choice.get('finish_reason') for choice in chunk.get('choices', [])):
                            terminal = True
                        yield chunk
            if parts:
                chunk = unwrap_frame('\n'.join(parts))
                if chunk:
                    seen = True
                    terminal |= any(choice.get('finish_reason') for choice in chunk.get('choices', []))
                    yield chunk
                else:
                    terminal = True
            if not seen or not terminal:
                raise ProtocolError('上游返回空流或在结束标志前断开连接。')


def collect(chunks, model):
    message = {'role': 'assistant', 'content': ''}
    calls, usage, finish = {}, {}, 'stop'
    for chunk in chunks:
        if chunk.get('usage'):
            usage = chunk['usage']
        for choice in chunk.get('choices', []):
            delta = choice.get('delta') or {}
            for name in ['content', 'reasoning_content']:
                if delta.get(name):
                    message[name] = message.get(name, '') + delta[name]
            for call in delta.get('tool_calls', []):
                target = calls.setdefault(call.get('index', 0), {'id': '', 'type': 'function', 'function': {'name': '', 'arguments': ''}})
                if call.get('id'):
                    target['id'] = call['id']
                for name, value in call.get('function', {}).items():
                    if name in ('name', 'arguments') and value:
                        target['function'][name] += value
            finish = choice.get('finish_reason') or finish
    if calls:
        message['tool_calls'] = [calls[index] for index in sorted(calls)]
    return {'id': 'chatcmpl-' + uuid.uuid4().hex, 'object': 'chat.completion',
            'created': int(time.time()), 'model': model, 'choices': [{'index': 0, 'message': message,
            'finish_reason': finish}], 'usage': usage}


def serve(store, address):
    client = Client(store)
    slots = threading.BoundedSemaphore(2)

    class Handler(BaseHTTPRequestHandler):
        protocol_version = 'HTTP/1.1'

        def log_message(self, *arguments):
            pass

        def reply(self, status, body):
            encoded = json_bytes(body)
            if status >= 400:
                self.close_connection = True
            self.send_response(status)
            self.send_header('Content-Type', 'application/json; charset=utf-8')
            self.send_header('Content-Length', str(len(encoded)))
            if self.close_connection:
                self.send_header('Connection', 'close')
            self.end_headers()
            self.wfile.write(encoded)

        def authenticated(self):
            supplied = self.headers.get('Authorization', '').removeprefix('Bearer ')
            expected = store.load()['api_key']
            if hmac.compare_digest(supplied.encode('utf-8'), expected.encode('utf-8')):
                return True
            self.reply(401, {'error': {'message': '转换服务密钥无效。', 'type': 'authentication_error'}})
            return False

        def do_GET(self):
            if self.path == '/health':
                self.reply(200, {'状态': '正常', '已授权': bool(store.load().get('access_token'))})
                return
            if not self.authenticated():
                return
            if self.path != '/v1/models':
                self.reply(404, {'error': {'message': '接口不存在。'}})
                return
            try:
                models = [{'id': entry.get('display_name', entry['key']).casefold(), 'object': 'model',
                           'created': 0, 'owned_by': 'qoder', 'upstream_key': entry['key'],
                           'name': entry.get('display_name', entry['key']), 'available': bool(entry.get('enable')),
                           'max_input_tokens': entry.get('max_input_tokens'),
                           'catalog_vision': bool(entry.get('is_vl')),
                           'thinking_config': entry.get('thinking_config')} for entry in client.catalog()]
                self.reply(200, {'object': 'list', 'data': models})
            except Exception as error:
                self.reply(getattr(error, 'status', 502), {'error': {'message': str(error), 'type': 'upstream_error'}})

        def do_POST(self):
            if not self.authenticated():
                return
            if self.path != '/v1/chat/completions':
                self.reply(404, {'error': {'message': '仅支持聊天补全接口。'}})
                return
            if not slots.acquire(blocking=False):
                self.reply(429, {'error': {'message': '转换服务并发已满。'}})
                return
            sent = False
            chunks = None
            try:
                size = int(self.headers.get('Content-Length', '0'))
                if size <= 0 or size > 8 * 1024 * 1024:
                    raise ProtocolError('请求正文为空或超过 8 MB。', 413)
                request = json.loads(self.rfile.read(size))
                if not isinstance(request, dict):
                    raise ProtocolError('请求正文必须是 JSON 对象。', 400)
                chunks = client.stream(request)
                if not request.get('stream', False):
                    self.reply(200, collect(chunks, request.get('model') or 'auto'))
                    return
                first = next(chunks)
                self.send_response(200)
                self.send_header('Content-Type', 'text/event-stream')
                self.send_header('Cache-Control', 'no-cache')
                self.send_header('Transfer-Encoding', 'chunked')
                self.send_header('X-Accel-Buffering', 'no')
                self.end_headers()
                sent = True

                def emit(value):
                    content = ('data: ' + value + '\n\n').encode('utf-8')
                    self.wfile.write(f'{len(content):X}\r\n'.encode() + content + b'\r\n')
                    self.wfile.flush()

                first = dict(first, model=request.get('model') or 'auto')
                emit(json_bytes(first).decode())
                for chunk in chunks:
                    chunk = dict(chunk, model=request.get('model') or 'auto')
                    emit(json_bytes(chunk).decode())
                emit('[DONE]')
                self.wfile.write(b'0\r\n\r\n')
                self.wfile.flush()
            except (BrokenPipeError, ConnectionResetError):
                self.close_connection = True
            except Exception as error:
                status = getattr(error, 'status', 400 if isinstance(error, (ValueError, json.JSONDecodeError)) else 502)
                body = {'error': {'message': str(error), 'type': 'upstream_error'}}
                if sent:
                    with contextlib.suppress(OSError):
                        emit(json_bytes(body).decode())
                        self.wfile.write(b'0\r\n\r\n')
                        self.wfile.flush()
                else:
                    self.reply(status, body)
                self.close_connection = True
            finally:
                if chunks is not None:
                    chunks.close()
                slots.release()

    host, port = address.rsplit(':', 1)
    server = ThreadingHTTPServer((host, int(port)), Handler)
    server.daemon_threads = True
    print('转换服务已启动：' + address, flush=True)
    server.serve_forever()


def main():
    parser = argparse.ArgumentParser(description='国内版 Qoder 授权与协议转换。')
    parser.add_argument('action', choices=['login-start', 'login-wait', 'catalog', 'probe', 'serve', 'show-key'])
    parser.add_argument('--data', required=True)
    parser.add_argument('--listen', default='127.0.0.1:8963')
    parser.add_argument('--model', default='qfmodel')
    arguments = parser.parse_args()
    store = Store(arguments.data)
    if arguments.action == 'login-start':
        print(begin_login(store))
    elif arguments.action == 'login-wait':
        print(json.dumps(finish_login(store), ensure_ascii=False))
    elif arguments.action == 'show-key':
        print(store.load()['api_key'])
    elif arguments.action == 'catalog':
        print(json.dumps(Client(store).catalog(), ensure_ascii=False))
    elif arguments.action == 'probe':
        request = {'model': arguments.model, 'messages': [{'role': 'user', 'content': '请只回复：连接成功。'}], 'max_tokens': 128}
        print(json.dumps(collect(Client(store).stream(request), arguments.model), ensure_ascii=False))
    else:
        serve(store, arguments.listen)


if __name__ == '__main__':
    try:
        main()
    except (ProtocolError, urllib.error.URLError) as error:
        print('错误：' + str(error), file=sys.stderr)
        raise SystemExit(1)
