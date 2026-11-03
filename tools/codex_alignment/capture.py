import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import signal
import socket
import ssl
import struct
import subprocess
import tempfile
import threading
import time
import zlib


def uint16s(data):
    return list(struct.unpack('!' + 'H' * (len(data) // 2), data))


def parse_client_hello(record):
    assert record[0] == 22 and record[5] == 1
    payload = memoryview(record)[9:]
    version = int.from_bytes(payload[:2])
    cursor = 34
    session_size = payload[cursor]
    cursor += 1 + session_size
    cipher_size = int.from_bytes(payload[cursor:cursor + 2])
    cursor += 2
    ciphers = uint16s(payload[cursor:cursor + cipher_size])
    cursor += cipher_size
    compression_size = payload[cursor]
    cursor += 1 + compression_size
    extension_size = int.from_bytes(payload[cursor:cursor + 2])
    cursor += 2
    limit = cursor + extension_size
    extensions = []
    details = {}
    while cursor < limit:
        identifier, length = struct.unpack('!HH', payload[cursor:cursor + 4])
        cursor += 4
        value = bytes(payload[cursor:cursor + length])
        cursor += length
        extensions.append(identifier)
        if identifier in (10, 13):
            details[{10: 'supported_groups', 13: 'signature_algorithms'}[identifier]] = uint16s(value[2:])
        elif identifier == 11:
            details['point_formats'] = list(value[1:])
        elif identifier == 43:
            details['supported_versions'] = uint16s(value[1:])
        elif identifier == 45:
            details['psk_modes'] = list(value[1:])
        elif identifier == 16:
            names = []
            offset = 2
            while offset < len(value):
                size = value[offset]
                offset += 1
                names.append(value[offset:offset + size].decode('ascii'))
                offset += size
            details['alpn'] = names
        elif identifier == 51:
            groups = []
            offset = 2
            while offset < len(value):
                group, size = struct.unpack('!HH', value[offset:offset + 4])
                groups.append({'group': group, 'size': size})
                offset += 4 + size
            details['key_shares'] = groups
    return {'legacy_version': version, 'record_version': int.from_bytes(record[1:3]),
            'session_id_size': session_size, 'cipher_suites': ciphers,
            'extensions': extensions, **details}


def sanitized_headers(headers):
    allowed = {'user-agent', 'originator', 'version', 'accept', 'accept-encoding',
               'content-type', 'connection', 'upgrade', 'sec-websocket-version',
               'sec-websocket-extensions', 'openai-beta'}
    return {name.lower(): value for name, value in headers if name.lower() in allowed}


def receive_exact(connection, size):
    result = bytearray()
    while len(result) < size:
        chunk = connection.recv(size - len(result))
        if not chunk:
            raise RuntimeError('Disconnected in WebSocket frame')
        result.extend(chunk)
    return bytes(result)


def capture_websocket_message(connection, headers, window_bits):
    values = {name.lower(): value for name, value in headers}
    accept = base64.b64encode(hashlib.sha1((values['sec-websocket-key'] + '258EAFA5-E914-47DA-95CA-C5AB0DC85B11').encode()).digest()).decode()
    extension = f'permessage-deflate; client_max_window_bits={window_bits}'
    connection.sendall(('HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n'
                        f'Sec-WebSocket-Accept: {accept}\r\nSec-WebSocket-Extensions: {extension}\r\n\r\n').encode())
    frames = []
    payload = bytearray()
    while True:
        flags, size = receive_exact(connection, 2)
        masked = bool(size & 128)
        size &= 127
        if size == 126:
            size = int.from_bytes(receive_exact(connection, 2))
        elif size == 127:
            size = int.from_bytes(receive_exact(connection, 8))
        if len(payload) + size > 16 * 1024 * 1024:
            raise RuntimeError('WebSocket message exceeds capture limit')
        mask = receive_exact(connection, 4) if masked else b''
        chunk = receive_exact(connection, size)
        if masked:
            chunk = bytes(value ^ mask[index % 4] for index, value in enumerate(chunk))
        frames.append({'fin': bool(flags & 128), 'compressed': bool(flags & 64),
                       'opcode': flags & 15, 'masked': masked})
        if flags & 15 not in (0, 1, 2):
            raise RuntimeError('Expected first WebSocket data message')
        payload.extend(chunk)
        if flags & 128:
            break
    if frames[0]['compressed']:
        payload = zlib.decompressobj(-window_bits).decompress(bytes(payload) + b'\x00\x00\xff\xff')
    return {'server_extension': extension, 'frames': frames,
            'trailing_newline': payload.endswith(b'\n'), 'message_type': json.loads(payload).get('type')}


def capture(args):
    if os.name != 'posix' or not Path('/proc/self/ns/net').exists():
        raise RuntimeError('Run in an isolated Linux network namespace')
    interfaces = {name for _, name in socket.if_nameindex()}
    if interfaces != {'lo'}:
        raise RuntimeError('Refusing capture with non-loopback network interfaces')
    subprocess.run(['ip', 'link', 'set', 'lo', 'up'], check=True)
    binary = Path(args.binary).resolve()
    version = subprocess.check_output([str(binary), '--version'], text=True).strip()
    if version != 'codex-cli 0.153.4':
        raise RuntimeError(f'Unexpected baseline version: {version}')
    output = {'codex_version': version, 'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
              'os_release': Path('/etc/os-release').read_text(), 'mode': args.mode,
              'tls_backend': args.tls_backend}
    done = threading.Event()
    errors = []
    listener = socket.socket()
    listener.bind(('127.0.0.1', 0))
    listener.listen(5)
    listener.settimeout(30)
    port = listener.getsockname()[1]
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    context.load_cert_chain(args.cert, args.key)
    context.set_alpn_protocols(['h2', 'http/1.1'] if args.mode == 'http2' else ['http/1.1'])

    def serve():
        try:
            with listener.accept()[0] as connection:
                connection.settimeout(15)
                deadline = time.monotonic() + 15
                while True:
                    record = connection.recv(65535, socket.MSG_PEEK)
                    if len(record) >= 5 and len(record) >= 5 + int.from_bytes(record[3:5]):
                        break
                    if not record or time.monotonic() > deadline:
                        raise RuntimeError('Incomplete ClientHello')
                    time.sleep(0.005)
                output['tls_client_hello'] = parse_client_hello(record)
                with context.wrap_socket(connection, server_side=True) as secured:
                    output['negotiated_alpn'] = secured.selected_alpn_protocol()
                    if secured.selected_alpn_protocol() == 'h2':
                        import h2.config
                        import h2.connection
                        import h2.events
                        protocol = h2.connection.H2Connection(h2.config.H2Configuration(client_side=False, header_encoding='utf-8'))
                        protocol.initiate_connection()
                        secured.sendall(protocol.data_to_send())
                        wire = bytearray()
                        while True:
                            data = secured.recv(65535)
                            if not data:
                                raise RuntimeError('HTTP/2 disconnected before headers')
                            wire.extend(data)
                            events = protocol.receive_data(data)
                            pending = protocol.data_to_send()
                            if pending:
                                secured.sendall(pending)
                            request = next((event for event in events if isinstance(event, h2.events.RequestReceived)), None)
                            if request is not None:
                                output['headers'] = sanitized_headers(request.headers)
                                output['header_names'] = [name for name, _ in request.headers]
                                break
                        cursor = 24
                        output['http2_settings'] = []
                        output['http2_window_updates'] = []
                        while cursor + 9 <= len(wire):
                            length = int.from_bytes(wire[cursor:cursor + 3])
                            kind, flags = wire[cursor + 3:cursor + 5]
                            stream = int.from_bytes(wire[cursor + 5:cursor + 9])
                            frame = wire[cursor + 9:cursor + 9 + length]
                            if len(frame) != length:
                                break
                            if kind == 4 and not flags & 1:
                                output['http2_settings'].extend([list(struct.unpack('!HI', frame[offset:offset + 6])) for offset in range(0, length, 6)])
                            if kind == 8:
                                output['http2_window_updates'].append([stream, int.from_bytes(frame)])
                            cursor += 9 + length
                    else:
                        data = bytearray()
                        while b'\r\n\r\n' not in data:
                            chunk = secured.recv(65535)
                            if not chunk:
                                raise RuntimeError('Disconnected before HTTP headers')
                            data.extend(chunk)
                            if len(data) > 1048576:
                                raise RuntimeError('HTTP headers exceed limit')
                        lines = bytes(data).split(b'\r\n\r\n', 1)[0].decode('latin-1').split('\r\n')
                        headers = [tuple(part.strip() for part in line.split(':', 1)) for line in lines[1:]]
                        output['headers'] = sanitized_headers(headers)
                        output['header_names'] = [name.lower() for name, _ in headers]
                        if args.mode == 'websocket' and args.websocket_message:
                            output['websocket_message'] = capture_websocket_message(secured, headers, args.websocket_window_bits)
        except Exception as error:
            errors.append(str(error))
        finally:
            done.set()

    thread = threading.Thread(target=serve, daemon=True)
    thread.start()
    with tempfile.TemporaryDirectory(prefix='codex-baseline-') as directory:
        home = Path(directory) / 'home'
        workspace = Path(directory) / 'workspace'
        home.mkdir()
        workspace.mkdir()
        env = {'PATH': os.environ.get('PATH', '/usr/bin:/bin'), 'HOME': directory,
               'CODEX_HOME': str(home), 'BASELINE_API_KEY': 'offline-dummy-key',
               'TERM': 'xterm-256color', 'LANG': 'C.UTF-8'}
        if args.tls_backend == 'rustls':
            env['CODEX_CA_CERTIFICATE'] = '/etc/ssl/cert.pem'
        config = [f'model_providers.baseline.base_url="https://localhost:{port}/v1"',
                  'model_provider="baseline"', 'model_providers.baseline.name="Baseline"',
                  'model_providers.baseline.env_key="BASELINE_API_KEY"',
                  'model_providers.baseline.wire_api="responses"',
                  f'model_providers.baseline.supports_websockets={str(args.mode == "websocket").lower()}',
                  'model_providers.baseline.request_max_retries=0',
                  'model_providers.baseline.stream_max_retries=0', 'model="gpt-5.4"']
        command = [str(binary), 'exec', '--ephemeral', '--skip-git-repo-check', '--sandbox', 'read-only']
        for setting in config:
            command.extend(['-c', setting])
        command.append('Reply with pong. Do not use tools.')
        with open(args.output + '.log', 'w') as log:
            process = subprocess.Popen(command, cwd=workspace, env=env, stdin=subprocess.DEVNULL, stdout=log, stderr=log, start_new_session=True)
            try:
                if not done.wait(40):
                    raise RuntimeError('Timed out waiting for CLI; inspect the local capture log')
            finally:
                if process.poll() is None:
                    os.killpg(process.pid, signal.SIGTERM)
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait()
                listener.close()
    if errors:
        raise RuntimeError('; '.join(errors))
    Path(args.output).write_text(json.dumps(output, indent=2) + '\n')
    print(json.dumps(output, indent=2))


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', required=True)
    parser.add_argument('--cert', required=True)
    parser.add_argument('--key', required=True)
    parser.add_argument('--output', required=True)
    parser.add_argument('--mode', choices=['http1', 'http2', 'websocket'], required=True)
    parser.add_argument('--tls-backend', choices=['native', 'rustls'], default='native')
    parser.add_argument('--websocket-message', action='store_true')
    parser.add_argument('--websocket-window-bits', type=int, choices=range(8, 16), default=15)
    capture(parser.parse_args())
