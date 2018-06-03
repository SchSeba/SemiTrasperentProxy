#!/usr/bin/bash

set -o errexit
set -o nounset
set -o pipefail
set -x # echo on

until ifconfig vnet0; do
    echo vm not up wait 5 sec and retry
    sleep 5
done

echo Forwarding
sysctl -w net.ipv4.ip_forward=1
sysctl -w net.ipv6.conf.all.forwarding=1

sysctl -w net.ipv6.conf.all.disable_ipv6=1
sysctl -w net.ipv6.conf.default.disable_ipv6=1

echo Kernel Parameters
echo Allow ip_forward

echo 1 > /proc/sys/net/ipv4/ip_forward

echo rp_Filters
echo 0 > /proc/sys/net/ipv4/conf/default/rp_filter
echo 0 > /proc/sys/net/ipv4/conf/all/rp_filter
echo 0 > /proc/sys/net/ipv4/conf/eth0/rp_filter
echo 0 > /proc/sys/net/ipv4/conf/virbr0/rp_filter
echo 0 > /proc/sys/net/ipv4/conf/virbr0-nic/rp_filter
echo 0 > /proc/sys/net/ipv4/conf/vnet0/rp_filter

echo Bridge Section
if test -d /proc/sys/net/bridge/ ; then
  for i in /proc/sys/net/bridge/*
  do
    echo 0 > $i
  done
  unset i
fi

LoadIptables() 
{
    # Echo need to load the kernel module on the host
    echo Load Bridge Kernel Module
    modprobe bridge


    echo Config ebtables rolues

    ebtables -t broute -F # Flush the table
    # inbound traffic
    ebtables -t broute -A BROUTING -p IPv4 --ip-dst 10.0.1.2 \
    -j redirect --redirect-target DROP
    # returning outbound traffic
    ebtables -t broute -A BROUTING -p IPv4 --ip-src 10.0.1.2 \
    -j redirect --redirect-target DROP


    echo Routing table
    # IPv4-only
    ip -f inet rule add fwmark 8 lookup 108
    ip -f inet route add local default dev lo table 108

    # echo Clean Iptables
    iptables -t mangle -F ISTIO_INBOUND 2>/dev/null
    iptables -t mangle -X ISTIO_INBOUND 2>/dev/null
    iptables -t mangle -F ISTIO_DIVERT 2>/dev/null
    iptables -t mangle -X ISTIO_DIVERT 2>/dev/null
    iptables -t mangle -F ISTIO_TPROXY 2>/dev/null
    iptables -t mangle -X ISTIO_TPROXY 2>/dev/null
    iptables -t mangle -F PREROUTING 2>/dev/null
    iptables -t mangle -X PREROUTING 2>/dev/null
    iptables -t mangle -F OUTPUT 2>/dev/null
    iptables -t mangle -X OUTPUT 2>/dev/null

    echo "Iptables for tproxy"

    iptables -t mangle -vL
    iptables -t mangle -N ISTIO_DIVERT
    iptables -t mangle -A ISTIO_DIVERT -j MARK --set-mark 8
    iptables -t mangle -A ISTIO_DIVERT -j ACCEPT

    table=mangle
    iptables -t ${table} -N ISTIO_INBOUND
    iptables -t ${table} -A PREROUTING -p tcp -m comment --comment "Kubevirt Spice"  --dport 5900 -j RETURN
    iptables -t ${table} -A PREROUTING -p tcp -m comment --comment "Kubevirt virt-manager"  --dport 16509 -j RETURN
    iptables -t ${table} -A PREROUTING -p tcp -i vnet0 -j ISTIO_INBOUND

    iptables -t ${table} -N ISTIO_TPROXY
    iptables -t ${table} -A ISTIO_TPROXY ! -d 127.0.0.1/32 -p tcp -j TPROXY --tproxy-mark 8/0xffffffff --on-port 9401
    #iptables -t mangle -A ISTIO_TPROXY ! -d 127.0.0.1/32 -p udp -j TPROXY --tproxy-mark 8/0xffffffff --on-port 8080

    # If an inbound packet belongs to an established socket, route it to the
    # loopback interface.
    iptables -t ${table} -A ISTIO_INBOUND -p tcp -m socket -j ISTIO_DIVERT
    #iptables -t mangle -A ISTIO_INBOUND -p udp -m socket -j ISTIO_DIVERT

    # Otherwise, it's a new connection. Redirect it using TPROXY.
    iptables -t ${table} -A ISTIO_INBOUND -p tcp -j ISTIO_TPROXY
    #iptables -t mangle -A ISTIO_INBOUND -p udp -j ISTIO_TPROXY

    table=nat
    # Remove vm Connection from iptables rules
    iptables -t ${table} -I PREROUTING 1 -s 10.0.1.2 -j ACCEPT
    iptables -t ${table} -I OUTPUT 1 -d 10.0.1.2 -j ACCEPT

    # Allow guest -> world -- using nat for UDP
    iptables -t ${table} -I POSTROUTING 1 -s 10.0.1.2 -p udp -j MASQUERADE

    table=mangle
    iptables -t ${table} -I OUTPUT 1 -d 10.0.1.2 -j ACCEPT

    return 0
}

while ! LoadIptables
do
    echo Fail to Load Iptables
    sleep 5
done

cat > tcp.cfg <<EOF
defaults
  mode                    tcp
frontend main
  bind *:9080
  default_backend guest
backend guest
  server guest 10.0.1.2:9080 maxconn 2048
EOF

haproxy -f tcp.cfg -d &

tproxy_example `ifconfig eth0 | grep inet | awk '{print $2}'` 10.0.1.2