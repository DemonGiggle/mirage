# Routed Host-Sandbox Networking

This document explains the routed network backend used when a `networkPolicy`
needs egress allow semantics without falling back to full host-network
passthrough. For the broader runtime model, see
[architecture.md](architecture.md). For operator-facing behavior, see
[isolation.md](isolation.md).

## High-Level View

Mirage uses three different network shapes today:

- allow-all policy: the workload shares the host network stack
- deny-only IP/CIDR policy: the workload gets its own network namespace with
  packet-filter rules only
- routed IP/CIDR policy with egress allow rules: the workload gets its own
  network namespace plus a host-managed uplink

This document covers the third case.

At a high level:

1. the host creates a `veth` pair
2. one end stays on the host and acts as the sandbox gateway
3. the other end moves into the sandbox network namespace
4. the sandbox uses the host-side address as its default route
5. the host kernel forwards packets out to the normal uplink
6. host NAT rewrites the sandbox source address for off-subnet traffic
7. return traffic is accepted and sent back through the same `veth`

## Communication Chart

```text
                         control plane sync
                parent process writes 1 byte on FD 3
                                  |
                                  v
    +-------------------------------------------------------------+
    | Host process                                                |
    |                                                             |
    |  setupRoutedNetworkHost(pid, cfg)                           |
    |    ip link add <host-veth> type veth peer name <guest-veth>|
    |    ip addr add 198.19.X.1/30 dev <host-veth>               |
    |    ip link set <host-veth> up                              |
    |    ip link set <guest-veth> netns <sandbox-pid>            |
    |    iptables -A FORWARD ...                                 |
    |    iptables -t nat -A POSTROUTING ... -j MASQUERADE        |
    +--------------------------+----------------------------------+
                               |
                               | veth pair
                               |
    +--------------------------v----------------------------------+
    | Sandbox network namespace                                   |
    |                                                             |
    |  waitForRoutedNetworkReady(fd=3)                            |
    |  configureRoutedPolicyNetworkBackend(...)                   |
    |    ip link set lo up                                        |
    |    ip link set <guest-veth> up                              |
    |    ip addr add 198.19.X.2/30 dev <guest-veth>              |
    |    ip route replace default via 198.19.X.1 dev <guest-veth>|
    |    iptables/ip6tables policy rules                          |
    |                                                             |
    |  workload process                                            |
    +--------------------------+----------------------------------+
                               |
                               | forwarded packets
                               v
                      host uplink / external network
```

## How Host And Sandbox Communicate

There are two communication paths to keep separate.

### 1. Control-plane coordination

The host and sandbox coordinate setup with a pipe passed as an extra file
descriptor.

- The parent process creates a pipe before starting the backend process.
- The read end is passed into the sandbox as FD `3` and exposed through
  `--network-ready-fd 3`.
- The sandbox blocks in `waitForRoutedNetworkReady()` until it reads one byte.
- The host performs host-side network setup first.
- Once setup completes, the host writes one byte to the pipe and the sandbox
  configures its own interface and packet filters.

This prevents the sandbox from programming its default route before the host
has finished moving the guest `veth` into the sandbox namespace and enabling
forwarding/NAT on the host.

### 2. Data-plane packet movement

Actual packet traffic moves over a Linux `veth` pair.

- `<host-veth>` stays on the host
- `<guest-veth>` moves into the sandbox namespace
- the host-side IP becomes the sandbox gateway
- the sandbox-side IP becomes the workload-facing interface

Because the subnet is `/30`, the link is point-to-point in practice:

- subnet base: `198.19.N.0/30`
- host address: `198.19.N.1`
- guest address: `198.19.N.2`

## Linux Commands Behind The Flow

The routed backend is small enough that the important behavior can be described
directly in Linux command terms.

In the real implementation, Mirage generates interface names like these:

- `<host-veth>`: the host-side interface, with a prefix such as `mrgh...`
- `<guest-veth>`: the sandbox-side interface, with a prefix such as `mrgg...`

This document uses `<host-veth>` and `<guest-veth>` for readability.

### Host-side setup

Mirage programs the host side with commands equivalent to:

```bash
ip link add <host-veth> type veth peer name <guest-veth>
ip addr add 198.19.N.1/30 dev <host-veth>
ip link set <host-veth> up
ip link set <guest-veth> netns <sandbox-pid>
```

Meaning:

- `ip link add ... type veth peer name ...` creates a connected virtual cable
- `ip addr add ... dev <host-veth>` assigns the host-side gateway address
- `ip link set <host-veth> up` makes the host-side interface active
- `ip link set <guest-veth> netns <sandbox-pid>` hands the peer interface to the
  sandbox network namespace

### Host-side packet forwarding and NAT

Mirage then installs rules equivalent to:

```bash
iptables -w -A FORWARD -i <host-veth> -s 198.19.N.0/30 -j ACCEPT
iptables -w -A FORWARD -o <host-veth> -d 198.19.N.0/30 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
iptables -w -t nat -A POSTROUTING -s 198.19.N.0/30 ! -d 198.19.N.0/30 -j MASQUERADE
```

Meaning:

- the first `FORWARD` rule allows packets leaving the sandbox subnet
- the second `FORWARD` rule allows return traffic back into the sandbox only
  for established or related flows
- the `POSTROUTING MASQUERADE` rule rewrites sandbox source addresses when
  packets leave the `/30` subnet, so they can exit through the host's uplink

The host must also have IPv4 forwarding enabled:

```bash
cat /proc/sys/net/ipv4/ip_forward
```

Mirage expects the value to be `1`. If it is not, routed networking fails
closed instead of silently bypassing the requested policy.

To enable it immediately:

```bash
sudo sysctl -w net.ipv4.ip_forward=1
```

To keep it enabled across reboots, add a sysctl drop-in such as
`/etc/sysctl.d/99-mirage.conf` containing:

```text
net.ipv4.ip_forward = 1
```

Then reload sysctl settings with `sudo sysctl --system`.

### Sandbox-side setup

After the host signals readiness, the sandbox runs commands equivalent to:

```bash
ip link set lo up
ip link set <guest-veth> up
ip addr add 198.19.N.2/30 dev <guest-veth>
ip route replace default via 198.19.N.1 dev <guest-veth>
```

Meaning:

- loopback is enabled explicitly
- the sandbox-side `veth` is brought up
- the sandbox receives its point-to-point address
- the default route points at the host-side `veth` address

Mirage then applies the ordered `iptables` and `ip6tables` rules derived from
the resolved `networkPolicy`, including a top-of-chain
`ESTABLISHED,RELATED` accept rule for inbound return traffic.

## Packet Walkthrough

For a typical outbound TCP connection:

1. the workload opens a socket inside the sandbox
2. the sandbox routing table sends the packet to default gateway `198.19.N.1`
3. the packet exits via `<guest-veth>`
4. the host receives the packet on `<host-veth>`
5. the host `FORWARD` rule accepts the packet because it came from the sandbox
   subnet
6. the host `POSTROUTING` NAT rule rewrites the source address
7. the packet leaves through the host's normal uplink
8. the reply packet returns to the host
9. conntrack marks it as `ESTABLISHED`
10. the return `FORWARD` rule accepts it back toward `<host-veth>`
11. the packet crosses the `veth` pair and reaches `<guest-veth>`
12. the sandbox packet-filter rules allow the return traffic into the workload

The key point is that the sandbox does not directly own a physical uplink. It
only owns one end of a `veth` pair. The host does the forwarding and NAT on its
behalf.

## Practical Mental Model

If you need a short explanation, use this:

- the sandbox gets a private point-to-point link to the host
- the host acts like a tiny router for that sandbox
- `FORWARD` rules decide which packets may traverse the host
- `MASQUERADE` lets sandbox traffic appear as host-originated traffic on the
  outside network
- sandbox-local packet-filter rules still enforce the requested policy inside
  the sandbox namespace

That is why this backend is stricter than host-network passthrough but more
capable than a fully offline namespace.
