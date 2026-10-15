"""协议转换中的错误恢复、工具约束与持久化回归检查，不调用外部接口。"""

import io
import json
import multiprocessing
import os
from pathlib import Path
import tempfile
import time
import unittest
from unittest import mock

import qoder_bridge as bridge


def increment_state(directory):
    store = bridge.Store(directory)
    for _ in range(20):
        def increment(state):
            value = state.get('counter', 0)
            time.sleep(0.001)
            state['counter'] = value + 1
        store.update(increment)


class ProtocolTests(unittest.TestCase):
    def test_nested_error(self):
        with self.assertRaises(bridge.ProtocolError) as caught:
            bridge.unwrap_frame(json.dumps({'statusCodeValue': '401', 'body': '{}'}))
        self.assertEqual(caught.exception.status, 401)

    def test_tool_fragments(self):
        chunks = [
            {'choices': [{'delta': {'tool_calls': [
                {'index': 0, 'id': 'first', 'function': {'name': 'weather', 'arguments': '{"city":'}},
                {'index': 1, 'id': 'second', 'function': {'name': 'clock', 'arguments': '{'}}]}}]},
            {'choices': [{'delta': {'tool_calls': [
                {'index': 1, 'function': {'arguments': '}'}},
                {'index': 0, 'function': {'arguments': '"南京"}'}}]}, 'finish_reason': 'tool_calls'}],
             'usage': {'prompt_tokens': 12, 'completion_tokens': 5}},
        ]
        result = bridge.collect(iter(chunks), 'qfmodel')
        calls = result['choices'][0]['message']['tool_calls']
        self.assertEqual([call['id'] for call in calls], ['first', 'second'])
        self.assertEqual(json.loads(calls[0]['function']['arguments']), {'city': '南京'})
        self.assertEqual(json.loads(calls[1]['function']['arguments']), {})
        self.assertEqual(result['usage']['prompt_tokens'], 12)

    def test_constraints_fail_without_partial_delivery(self):
        chunks = iter([{'choices': [{'delta': {'content': '普通回答'}, 'finish_reason': 'stop'}]}])
        with self.assertRaises(bridge.ProtocolError):
            next(bridge.checked_chunks(chunks, {'tool_choice': 'required'}))

    def test_parallel_tools_are_rejected(self):
        calls = [{'index': index, 'id': str(index), 'function': {'name': 'tool', 'arguments': '{}'}} for index in range(2)]
        chunks = iter([{'choices': [{'delta': {'tool_calls': calls}, 'finish_reason': 'tool_calls'}]}])
        with self.assertRaises(bridge.ProtocolError):
            list(bridge.checked_chunks(chunks, {'parallel_tool_calls': False}))

    def test_catalog_refresh(self):
        client = bridge.Client(None)
        attempts = []
        client.state = lambda force_refresh=False: attempts.append(force_refresh) or {}
        response = io.StringIO(json.dumps({'chat': [{'key': 'qfmodel', 'enable': True}]}))
        with mock.patch.object(bridge, 'signed_headers', return_value={}), \
             mock.patch.object(bridge, 'open_request', side_effect=[bridge.ProtocolError('失效', 401), response]):
            self.assertEqual(client.catalog()[0]['key'], 'qfmodel')
        self.assertEqual(attempts, [False, True])

    def test_actual_refresh_response_field(self):
        class Store:
            def __init__(self):
                self.value = {'access_token': '旧访问令牌', 'refresh_token': '旧续期令牌'}
            def load(self):
                return dict(self.value)
            def update(self, operation):
                return operation(self.value)
        store = Store()
        with mock.patch.object(bridge, 'http_json', return_value={'device_token': '新访问令牌', 'refresh_token': '新续期令牌'}):
            result = bridge.Client(store).state(force_refresh=True)
        self.assertEqual(result['access_token'], '新访问令牌')
        self.assertEqual(store.load()['refresh_token'], '新续期令牌')

    def test_stream_refresh_before_output_only(self):
        client = bridge.Client(None)
        client.resolve_model = lambda name: {'key': 'qfmodel'}
        attempts = []
        client.state = lambda force_refresh=False: attempts.append(force_refresh) or {}
        def first_failure(request, state):
            if len(attempts) == 1:
                raise bridge.ProtocolError('失效', 401)
            yield {'choices': [{'delta': {'content': '恢复成功'}, 'finish_reason': 'stop'}]}
        client.stream_once = first_failure
        request = {'messages': [{'role': 'user', 'content': '你好'}]}
        self.assertEqual(list(client.stream(request))[0]['choices'][0]['delta']['content'], '恢复成功')
        self.assertEqual(attempts, [False, True])
        attempts.clear()
        def after_output(request, state):
            yield {'choices': [{'delta': {'content': '已经输出'}}]}
            raise bridge.ProtocolError('后续失效', 401)
        client.stream_once = after_output
        with self.assertRaises(bridge.ProtocolError):
            list(client.stream(request))
        self.assertEqual(attempts, [False])

    def test_complete_alias_mapping(self):
        client = bridge.Client(None)
        client.catalog = lambda: [{'key': 'qfmodel', 'display_name': 'Qwen3.8-Flash', 'enable': True},
                                  {'key': 'blocked', 'display_name': '未开通', 'enable': False}]
        for name in ['qfmodel', 'qwen3.8-flash', 'QODER/Qwen3.8-Flash']:
            self.assertEqual(client.resolve_model(name)['key'], 'qfmodel')
        with self.assertRaises(bridge.ProtocolError) as caught:
            client.resolve_model('未开通')
        self.assertEqual(caught.exception.status, 403)

    def test_zero_seed_is_not_silently_ignored(self):
        with self.assertRaises(bridge.ProtocolError) as caught:
            bridge.prepare_request({'messages': [{'role': 'user', 'content': '你好'}], 'seed': 0})
        self.assertEqual(caught.exception.status, 400)

    def test_early_error_closes_connection(self):
        class Store:
            def load(self):
                return {'api_key': '测试密钥'}
        class Server:
            def __init__(self, address, handler):
                self.server_address, self.handler = address, handler
                Server.current = self
            def serve_forever(self):
                pass
        class Socket:
            def __init__(self, data):
                self.input, self.output = io.BytesIO(data), bytearray()
            def makefile(self, *arguments):
                return self.input
            def sendall(self, data):
                self.output.extend(data)
        with mock.patch.object(bridge, 'ThreadingHTTPServer', Server):
            bridge.serve(Store(), '127.0.0.1:8963')
        connection = Socket(b'POST /v1/chat/completions HTTP/1.1\r\nHost: test\r\nContent-Length: 2\r\n\r\n{}GET /health HTTP/1.1\r\nHost: test\r\n\r\n')
        Server.current.handler(connection, ('127.0.0.1', 1000), Server.current)
        response = connection.output.decode()
        self.assertIn('401', response)
        self.assertIn('Connection: close', response)
        self.assertNotIn('501', response)

    @unittest.skipIf(bridge.fcntl is None, '跨进程文件锁面向实际部署的 Linux 环境。')
    def test_process_transactions(self):
        parent = Path(os.environ.get('QODER_TEST_WORK', Path(__file__).resolve().parent.parent / 'work/test-scratch'))
        parent.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(dir=parent) as directory:
            self.assertTrue(Path(directory).resolve().is_relative_to(parent.resolve()))
            store = bridge.Store(directory)
            processes = [multiprocessing.Process(target=increment_state, args=(directory,)) for _ in range(2)]
            for process in processes:
                process.start()
            for process in processes:
                process.join(15)
                self.assertEqual(process.exitcode, 0)
            self.assertEqual(store.load()['counter'], 40)


if __name__ == '__main__':
    unittest.main()
