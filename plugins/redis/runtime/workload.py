import binascii
import random
import socket
import sys
import time
from urllib.parse import urlsplit


class RedisError(Exception):
    pass


class Connection:
    def __init__(self, host, port):
        self.socket = socket.create_connection((host, port), CONFIG["timeoutSeconds"])
        self.socket.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
        self.stream = self.socket.makefile("rb")

    def close(self):
        self.stream.close()
        self.socket.close()

    def read(self):
        line = self.stream.readline()
        if not line or not line.endswith(b"\r\n"):
            raise ConnectionError("Redis connection closed or malformed response")
        kind, value = line[:1], line[1:-2]
        if kind == b"-":
            raise RedisError(value.decode("utf-8", "replace"))
        if kind == b"+":
            return value
        if kind == b":":
            return int(value)
        if kind == b"$":
            length = int(value)
            if length == -1:
                return None
            value = self.stream.read(length + 2)
            if len(value) != length + 2 or value[-2:] != b"\r\n":
                raise ConnectionError("Incomplete Redis bulk response")
            return value[:-2]
        if kind == b"*":
            length = int(value)
            return None if length == -1 else [self.read() for _ in range(length)]
        raise RedisError("Unsupported Redis response")

    def execute(self, *args):
        encoded = [arg if isinstance(arg, bytes) else str(arg).encode() for arg in args]
        packet = b"*%d\r\n" % len(encoded) + b"".join(b"$%d\r\n" % len(arg) + arg + b"\r\n" for arg in encoded)
        self.socket.sendall(packet)
        return self.read()


class RedisClusterClient:
    def __init__(self, host, port=6379):
        self.seed = (host, port)
        self.connections = {}
        self.slots = {}

    def close(self):
        for connection in self.connections.values():
            connection.close()
        self.connections.clear()

    def direct(self, address, *args):
        if address not in self.connections:
            self.connections[address] = Connection(*address)
        try:
            return self.connections[address].execute(*args)
        except (OSError, ConnectionError):
            # Never retry writes after an ambiguous connection failure.
            self.connections.pop(address).close()
            raise

    def refresh_slots(self):
        for item in self.direct(self.seed, "CLUSTER", "SLOTS"):
            host = item[2][0].decode() or self.seed[0]
            for slot in range(item[0], item[1] + 1):
                self.slots[slot] = (host, item[2][1])

    def execute(self, command, key, *args):
        encoded = key.encode() if isinstance(key, str) else key
        left = encoded.find(b"{")
        right = encoded.find(b"}", left + 1) if left >= 0 else -1
        hashed = encoded[left + 1:right] if right > left + 1 else encoded
        slot = binascii.crc_hqx(hashed, 0) % 16384
        address = self.slots.get(slot, self.seed)
        asking = False
        for _ in range(8):
            try:
                if asking:
                    self.direct(address, "ASKING")
                return self.direct(address, command, key, *args)
            except RedisError as error:
                parts = str(error).split()
                if len(parts) != 3 or parts[0] not in ("MOVED", "ASK"):
                    raise
                host, port = parts[2].rsplit(":", 1)
                address = (host.strip("[]"), int(port))
                asking = parts[0] == "ASK"
                if not asking:
                    self.slots[int(parts[1])] = address
        raise RedisError("Redis cluster redirect limit exceeded")


def node_address(name):
    return (name + "-0.redis-nodes", 6379)


def wait_until(check, timeout=240):
    deadline = time.monotonic() + timeout
    last_error = None
    while time.monotonic() < deadline:
        try:
            if check():
                return
        except (OSError, ConnectionError, RedisError) as error:
            last_error = error
        time.sleep(0.5)
    raise RuntimeError("Redis bootstrap timed out: " + str(last_error))


def bootstrap():
    primaries = [node_address("redis-primary-%d" % p) for p in range(1, CONFIG["primaries"] + 1)]
    pairs = [(node_address("redis-replica-%d-%d" % (p, r)), primaries[p-1]) for p in range(1, CONFIG["primaries"] + 1) for r in range(1, CONFIG["replicasPerPrimary"] + 1)]
    nodes = primaries + [replica for replica, _ in pairs]
    client = RedisClusterClient(*primaries[0])
    try:
        for address in nodes:
            wait_until(lambda: client.direct(address, "PING") == b"PONG")
        for address in nodes[1:]:
            # MEET requires an IP, not a Kubernetes DNS name.
            ip = socket.gethostbyname(address[0])
            client.direct(primaries[0], "CLUSTER", "MEET", ip, address[1])
        wait_until(lambda: all(len(client.direct(address, "CLUSTER", "NODES").splitlines()) == len(nodes) for address in nodes))
        for index, address in enumerate(primaries):
            start, end = index * 16384 // len(primaries), (index + 1) * 16384 // len(primaries) - 1
            # A restarted Job must retain an existing, compatible slot assignment.
            existing = client.direct(address, "CLUSTER", "SLOTS")
            own_id = client.direct(address, "CLUSTER", "MYID")
            own_ranges = [(row[0], row[1]) for row in existing if len(row[2]) >= 3 and row[2][2] == own_id]
            if not own_ranges:
                client.direct(address, "CLUSTER", "ADDSLOTSRANGE", start, end)
            elif own_ranges != [(start, end)]:
                raise RuntimeError("Redis primary has an incompatible slot assignment")
        # Wait for gossip to propagate primary identities and assigned slots.
        wait_until(lambda: all(sum(row[1]-row[0]+1 for row in client.direct(address, "CLUSTER", "SLOTS")) == 16384 for address in nodes))
        for replica, primary in pairs:
            primary_id = client.direct(primary, "CLUSTER", "MYID")
            client.direct(replica, "CLUSTER", "REPLICATE", primary_id)
        wait_until(lambda: all(b"cluster_state:ok" in client.direct(address, "CLUSTER", "INFO") for address in nodes))
        wait_until(lambda: all(b"master_link_status:up" in client.direct(replica, "INFO", "replication") for replica, _ in pairs))
        client.refresh_slots()
        payload = b"x" * CONFIG["payloadBytes"]
        operation = CONFIG["operation"]
        for index in range(CONFIG["keyCount"]):
            key = "kubephos:%d" % index
            if operation in ("mixed", "SET", "GET"):
                client.execute("SET", key, payload)
            elif operation == "HSET":
                client.execute("HSET", "hash:" + key, "field", payload)
            else:
                key = "list:" + key
                client.execute("DEL", key)
                # Bound each command's memory/packet size independently of list length.
                batch = max(1, min(100, 1048576 // max(1, len(payload))))
                for offset in range(0, CONFIG["listLength"], batch):
                    client.execute("RPUSH", key, *([payload] * min(batch, CONFIG["listLength"] - offset)))
        for primary in set(client.slots.values()):
            if CONFIG["replicasPerPrimary"] and client.direct(primary, "WAIT", CONFIG["replicasPerPrimary"], 30000) < CONFIG["replicasPerPrimary"]:
                raise RuntimeError("Redis fixture replication did not complete")
        print("Redis Cluster initialized: %d primaries, %d replicas per primary" % (len(primaries), CONFIG["replicasPerPrimary"]), flush=True)
    finally:
        client.close()


if __name__ == "__main__" and "--bootstrap" in sys.argv:
    bootstrap()
else:
    from locust import User, task

    class RedisUser(User):
        def on_start(self):
            target = urlsplit(self.host)
            self.redis = RedisClusterClient(target.hostname, target.port or 6379)
            self.redis.refresh_slots()
            self.payload = b"x" * CONFIG["payloadBytes"]

        def on_stop(self):
            self.redis.close()

        @task
        def operation(self):
            # The KubePhos wrapper injects its common RPS limiter for custom protocols.
            pace_request = globals().get("kubephos_wait_for_request_slot")
            if pace_request:
                pace_request()
            command = CONFIG["operation"]
            if command == "mixed":
                command = "GET" if random.randrange(100) < CONFIG["readPercentage"] else "SET"
            key = "kubephos:%d" % random.randrange(CONFIG["keyCount"])
            args = []
            if command == "SET":
                args = [self.payload]
            elif command == "HSET":
                key, args = "hash:" + key, ["field", self.payload]
            elif command == "LPUSH":
                key, args = "list:" + key, [self.payload]
            elif command == "LPOP":
                key = "list:" + key
            elif command.startswith("LRANGE_"):
                key, args = "list:" + key, [0, int(command.split("_")[1]) - 1]
            started = time.perf_counter()
            response, error = None, None
            try:
                response = self.redis.execute(command.split("_")[0], key, *args)
            except (OSError, ConnectionError, RedisError) as exception:
                error = exception
            self.environment.events.request.fire(request_type="redis", name=command, response_time=(time.perf_counter()-started)*1000, response_length=len(response) if isinstance(response, bytes) else 0, exception=error, context={})
