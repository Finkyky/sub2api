import argparse
import hashlib
import json
import os
from pathlib import Path
import signal
import socket
import ssl
import subprocess
import tempfile
import threading
import time

from capture import parse_client_hello


def capture(args):
    if os.name != 'posix' or {name for _, name in socket.if_nameindex()} != {'lo'}:
        raise RuntimeError('Run in an isolated Linux network namespace with only loopback')
    subprocess.run(['ip', 'link', 'set', 'lo', 'up'], check=True)
    binary = Path(args.binary).resolve()
    version = subprocess.check_output([str(binary), '--version'], text=True).strip()
    if version != 'codex-cli 0.153.4':
        raise RuntimeError(f'Unexpected baseline version: {version}')
    output = {'codex_version': version, 'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
              'tls_backend': 'rustls', 'scenario': 'HTTP/1.1 reconnect after local HTTP 503', 'connections': []}
    listener = socket.socket()
    listener.bind(('127.0.0.1', 0))
    listener.listen(2)
    listener.settimeout(30)
    port = listener.getsockname()[1]
    settings = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    settings.load_cert_chain(args.cert, args.key)
    settings.set_alpn_protocols(['http/1.1'])
    done = threading.Event()
    errors = []

    def serve():
        try:
            for _ in range(2):
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
                    result = {'tls_client_hello': parse_client_hello(record)}
                    with settings.wrap_socket(connection, server_side=True) as secured:
                        result['session_reused'] = secured.session_reused
                        result['negotiated_alpn'] = secured.selected_alpn_protocol()
                        data = bytearray()
                        while b'\r\n\r\n' not in data:
                            chunk = secured.recv(65535)
                            if not chunk or len(data) > 1048576:
                                raise RuntimeError('Incomplete or oversized HTTP headers')
                            data.extend(chunk)
                        head, body = bytes(data).split(b'\r\n\r\n', 1)
                        headers = dict(line.lower().split(b':', 1) for line in head.split(b'\r\n')[1:])
                        remaining = int(headers.get(b'content-length', b'0')) - len(body)
                        while remaining > 0:
                            chunk = secured.recv(min(65535, remaining))
                            if not chunk:
                                raise RuntimeError('Incomplete request body')
                            remaining -= len(chunk)
                        response = b'{"error":{"message":"offline retry","type":"server_error"}}'
                        secured.sendall(b'HTTP/1.1 503 Service Unavailable\r\nConnection: close\r\nContent-Type: application/json\r\nContent-Length: ' + str(len(response)).encode() + b'\r\n\r\n' + response)
                        output['connections'].append(result)
        except Exception as error:
            errors.append(str(error))
        finally:
            done.set()

    threading.Thread(target=serve, daemon=True).start()
    with tempfile.TemporaryDirectory(prefix='codex-resumption-') as directory:
        home = Path(directory) / 'codex'
        home.mkdir()
        env = {'PATH': os.environ.get('PATH', '/usr/bin:/bin'), 'HOME': directory, 'CODEX_HOME': str(home),
               'BASELINE_API_KEY': 'offline-dummy-key', 'TERM': 'xterm-256color', 'LANG': 'C.UTF-8',
               'CODEX_CA_CERTIFICATE': '/etc/ssl/cert.pem'}
        options = [f'model_providers.baseline.base_url="https://localhost:{port}/v1"',
                   'model_provider="baseline"', 'model_providers.baseline.name="Baseline"',
                   'model_providers.baseline.env_key="BASELINE_API_KEY"',
                   'model_providers.baseline.wire_api="responses"', 'model_providers.baseline.supports_websockets=false',
                   'model_providers.baseline.request_max_retries=1', 'model_providers.baseline.stream_max_retries=0',
                   'model="gpt-5.4"']
        command = [str(binary), 'exec', '--ephemeral', '--skip-git-repo-check', '--sandbox', 'read-only']
        for option in options:
            command.extend(['-c', option])
        command.append('Reply with pong. Do not use tools.')
        with open(args.output + '.log', 'w') as log:
            process = subprocess.Popen(command, cwd=directory, env=env, stdin=subprocess.DEVNULL, stdout=log, stderr=log, start_new_session=True)
            try:
                if not done.wait(40):
                    raise RuntimeError('Timed out waiting for reconnect')
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
    for name in ('binary', 'cert', 'key', 'output'):
        parser.add_argument('--' + name, required=True)
    capture(parser.parse_args())
